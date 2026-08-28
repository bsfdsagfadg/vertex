package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	responseproto "github.com/bsfdsagfadg/vertex/internal/responses"
	"github.com/bsfdsagfadg/vertex/internal/transform"
	"github.com/bsfdsagfadg/vertex/internal/vertex"
)

// handleCompletions is a deliberately thin legacy adapter over Chat
// Completions; all model conversion, racing and error handling stay shared.
func (s *Server) handleCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
		return
	}
	var in map[string]any
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		oaiError(w, http.StatusBadRequest, "invalid JSON", "invalid_request_error")
		return
	}
	if v, ok := in["best_of"].(float64); ok && v > 1 {
		oaiError(w, http.StatusBadRequest, "best_of > 1 is unsupported", "invalid_request_error")
		return
	}
	if _, ok := in["logprobs"]; ok {
		oaiError(w, http.StatusBadRequest, "logprobs is unsupported", "invalid_request_error")
		return
	}
	prompt := in["prompt"]
	var text string
	switch p := prompt.(type) {
	case string:
		text = p
	case []any:
		var parts []string
		for _, v := range p {
			if str, ok := v.(string); ok {
				parts = append(parts, str)
			} else {
				oaiError(w, http.StatusBadRequest, "prompt array must contain only strings", "invalid_request_error")
				return
			}
		}
		text = strings.Join(parts, "\n")
	default:
		oaiError(w, http.StatusBadRequest, "prompt must be a string or array", "invalid_request_error")
		return
	}
	if strings.TrimSpace(text) == "" {
		oaiError(w, http.StatusBadRequest, "prompt is required", "invalid_request_error")
		return
	}
	body := map[string]any{"model": in["model"], "messages": []any{map[string]any{"role": "user", "content": text}}}
	for _, k := range []string{"max_tokens", "temperature", "top_p", "stop", "stream", "n", "presence_penalty", "frequency_penalty", "seed", "user", "stream_options"} {
		if v, ok := in[k]; ok {
			body[k] = v
		}
	}
	reqBytes, _ := json.Marshal(body)
	cr := r.Clone(r.Context())
	cr.Body = io.NopCloser(bytes.NewReader(reqBytes))
	cr.ContentLength = int64(len(reqBytes))
	rec := newResponseRecorder()
	s.chat.handleChatCompletions(rec, cr)
	if rec.code < 200 || rec.code >= 300 {
		w.Header().Set("Content-Type", rec.Header().Get("Content-Type"))
		w.WriteHeader(rec.code)
		_, _ = w.Write(rec.Body.Bytes())
		return
	}
	if strings.Contains(rec.Header().Get("Content-Type"), "text/event-stream") || body["stream"] == true {
		writeCompletionStream(w, rec.Body.Bytes())
		return
	}
	var chat map[string]any
	if json.Unmarshal(rec.Body.Bytes(), &chat) != nil {
		oaiError(w, 502, "invalid upstream response", "server_error")
		return
	}
	writeJSON(w, http.StatusOK, completionFromChat(chat, text, in["echo"] == true))
}

func completionFromChat(chat map[string]any, prompt string, echo bool) map[string]any {
	choices := []any{}
	if raw, ok := chat["choices"].([]any); ok {
		for _, rv := range raw {
			c, _ := rv.(map[string]any)
			msg, _ := c["message"].(map[string]any)
			text, _ := msg["content"].(string)
			if echo {
				text = prompt + text
			}
			choices = append(choices, map[string]any{"index": c["index"], "text": text, "logprobs": nil, "finish_reason": c["finish_reason"]})
		}
	}
	out := map[string]any{"id": "cmpl-" + reqID24(), "object": "text_completion", "created": time.Now().Unix(), "model": chat["model"], "choices": choices}
	if u, ok := chat["usage"]; ok {
		out["usage"] = u
	}
	_ = prompt
	return out
}

func writeCompletionStream(w http.ResponseWriter, data []byte) {
	sw := newSSEWriter(w, "text/event-stream")
	done := false
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			_ = sw.write("data: [DONE]\n\n")
			done = true
			continue
		}
		var chunk map[string]any
		if json.Unmarshal([]byte(payload), &chunk) != nil {
			continue
		}
		out := map[string]any{"id": chunk["id"], "object": "text_completion", "created": chunk["created"], "model": chunk["model"]}
		if cs, ok := chunk["choices"].([]any); ok {
			outChoices := []any{}
			for _, rv := range cs {
				c, _ := rv.(map[string]any)
				d, _ := c["delta"].(map[string]any)
				txt, _ := d["content"].(string)
				outChoices = append(outChoices, map[string]any{"index": c["index"], "text": txt, "logprobs": nil, "finish_reason": c["finish_reason"]})
			}
			out["choices"] = outChoices
		}
		if usage, ok := chunk["usage"]; ok {
			out["usage"] = usage
		}
		_ = sw.write(sseEvent(out))
	}
	if !done {
		_ = sw.write("data: [DONE]\n\n")
	}
}

func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		oaiError(w, 405, "method not allowed", "invalid_request_error")
		return
	}
	var in responseproto.CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		oaiError(w, 400, "invalid JSON", "invalid_request_error")
		return
	}
	if len(in.UnknownFields()) > 0 {
		// Compatibility mode preserves forward-compatible SDK payloads. Clients
		// that opt into strict validation get an explicit parameter error.
		if strings.EqualFold(r.Header.Get("X-OpenAI-Strict"), "true") {
			oaiError(w, 400, "unknown parameter: "+strings.Join(in.UnknownFields(), ", "), "unsupported_parameter")
			return
		}
	}
	if in.Stream && in.Background {
		oaiError(w, 400, "background and stream cannot be used together", "invalid_parameter")
		return
	}
	if in.Store != nil && !*in.Store && strings.TrimSpace(r.Header.Get("Idempotency-Key")) != "" {
		oaiError(w, 400, "Idempotency-Key requires store=true", "invalid_parameter")
		return
	}
	if in.Store != nil && !*in.Store && (in.PreviousResponseID != "" || in.Conversation != nil) {
		oaiError(w, 400, "store=false cannot be used with response history", "invalid_parameter")
		return
	}
	for _, pair := range []struct {
		name  string
		value any
	}{} {
		if pair.value != nil && pair.value != false && pair.value != "" {
			oaiError(w, 400, "unsupported parameter: "+pair.name, "unsupported_parameter")
			return
		}
	}
	model := in.Model
	if strings.TrimSpace(model) == "" {
		oaiError(w, 400, "model is required", "invalid_request_error")
		return
	}
	var history []any
	if in.PreviousResponseID != "" && s.responseStore != nil {
		prev, e := s.responseStore.Get(r.Context(), in.PreviousResponseID, time.Now())
		if e != nil {
			oaiError(w, 404, "previous response not found", "invalid_parameter")
			return
		}
		var prior []any
		_ = json.Unmarshal(prev.Input, &prior)
		history = append(history, prior...)
		var priorOut []any
		if json.Unmarshal(prev.Output, &priorOut) == nil {
			history = append(history, priorOut...)
		}
	} else if in.PreviousResponseID != "" {
		oaiError(w, http.StatusNotImplemented, "response persistence is not configured", "unsupported_endpoint")
		return
	}
	if convID, ok := in.Conversation.(string); ok && convID != "" && s.responseStore != nil {
		if _, e := s.responseStore.GetConversation(r.Context(), convID); e != nil {
			oaiError(w, 404, "conversation not found", "invalid_parameter")
			return
		}
		if convItems, e := s.responseStore.ListConversationItems(r.Context(), convID, 0, 1000); e == nil {
			for _, item := range convItems {
				var v any
				if json.Unmarshal(item.Data, &v) == nil {
					history = append(history, v)
				}
			}
		}
	} else if in.Conversation != nil {
		oaiError(w, http.StatusNotImplemented, "conversation persistence is not configured", "unsupported_endpoint")
		return
	}
	payload, normalizedInput, err := responseproto.BuildGemini(in, history)
	if err != nil {
		if ae, ok := err.(*responseproto.AdapterError); ok {
			oaiError(w, http.StatusBadRequest, ae.Message, ae.Code)
		} else {
			oaiError(w, 400, err.Error(), "invalid_request_error")
		}
		return
	}
	actual, _, ok := resolveConfiguredModel(model, s.mw.cfg)
	if !ok {
		oaiModelNotFound(w, model)
		return
	}
	responseID := "resp_" + reqID24()
	storeEnabled := in.Store == nil || *in.Store
	var stored *responseproto.ResponseRecord
	if storeEnabled && s.responseStore != nil {
		reqb, _ := json.Marshal(in)
		ib, _ := json.Marshal(normalizedInput)
		now := time.Now().Unix()
		rec := responseproto.ResponseRecord{ID: responseID, Status: "in_progress", Model: model, Request: reqb, Input: ib, CreatedAt: now, ExpiresAt: now + 24*60*60, Store: true, Background: in.Background}
		rec.PreviousResponseID = in.PreviousResponseID
		if cid, ok := in.Conversation.(string); ok {
			rec.ConversationID = cid
		}
		if len(in.Metadata) > 0 {
			rec.Metadata, _ = json.Marshal(in.Metadata)
		}
		key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		h := sha256.Sum256(reqb)
		if key != "" {
			id, reused, conflict, e := s.responseStore.CreateIdempotent(r.Context(), rec, responseproto.IdempotencyRecord{Endpoint: "/v1/responses", Key: key, BodyHash: hex.EncodeToString(h[:]), ResourceKind: "response", ResourceID: responseID, CreatedAt: now, ExpiresAt: rec.ExpiresAt})
			if e != nil {
				oaiError(w, 500, "failed to create response", "server_error")
				return
			}
			if conflict {
				oaiError(w, 409, "idempotency key reused with different request", "idempotency_conflict")
				return
			}
			if reused {
				old, e := s.responseStore.Get(r.Context(), id, time.Now())
				if e != nil {
					oaiError(w, 409, "response is not available", "idempotency_conflict")
					return
				}
				writeResponseRecord(w, old)
				return
			}
		} else if e := s.responseStore.Create(r.Context(), rec); e != nil {
			oaiError(w, 500, "failed to create response", "server_error")
			return
		}
		stored = &rec
		if !in.Background && !in.Stream {
			createdData, _ := json.Marshal(map[string]any{"id": responseID, "status": "in_progress", "model": model})
			seq, _ := s.responseStore.AllocateEventSequence(r.Context(), "response", responseID)
			_ = s.responseStore.AppendEvent(r.Context(), responseproto.Event{ResourceKind: "response", ResourceID: responseID, Sequence: seq, EventID: responseID + "_created", EventType: "response.created", Data: createdData, CreatedAt: now})
		}
	}
	if in.Background {
		if in.Store != nil && !*in.Store {
			oaiError(w, 400, "background requires store=true", "invalid_parameter")
			return
		}
		id := responseID
		now := time.Now().Unix()
		reqb, _ := json.Marshal(in)
		ib, _ := json.Marshal(normalizedInput)
		rec := responseproto.ResponseRecord{ID: id, Status: "in_progress", Model: model, Request: reqb, Input: ib, CreatedAt: now, ExpiresAt: now + 24*60*60, Store: true, Background: true}
		rec.PreviousResponseID = in.PreviousResponseID
		if cid, ok := in.Conversation.(string); ok {
			rec.ConversationID = cid
		}
		if len(in.Metadata) > 0 {
			rec.Metadata, _ = json.Marshal(in.Metadata)
		}
		if s.responseStore == nil || (stored == nil && s.responseStore.Create(r.Context(), rec) != nil) {
			oaiError(w, 500, "failed to create background response", "server_error")
			return
		}
		if convID, ok := in.Conversation.(string); ok && convID != "" {
			userData, _ := json.Marshal(normalizedInput)
			_ = s.responseStore.AddConversationItems(r.Context(), convID, []responseproto.InputItem{{ID: "item_" + reqID24(), Data: userData, CreatedAt: now}})
		}
		createdPayload, _ := json.Marshal(map[string]any{"id": id, "status": "in_progress", "model": in.Model})
		seq, _ := s.responseStore.AllocateEventSequence(r.Context(), "response", id)
		_ = s.responseStore.AppendEvent(r.Context(), responseproto.Event{ResourceKind: "response", ResourceID: id, Sequence: seq, EventID: id + "_created", EventType: "response.created", Data: createdPayload, CreatedAt: now})
		bgCtx, cancel := context.WithTimeout(context.Background(), time.Duration(s.mw.cfg.RequestTimeout())*time.Second)
		s.backgroundMu.Lock()
		s.backgroundCancels[id] = cancel
		s.backgroundMu.Unlock()
		go s.runBackgroundResponse(bgCtx, cancel, rec, payload, actual)
		writeJSON(w, 200, map[string]any{"id": id, "object": "response", "created_at": now, "status": "in_progress", "model": in.Model, "output": []any{}})
		return
	}
	transform.ApplyImageConfig(payload, payload, actual)
	transform.ApplyImageDefaults(payload, actual, s.mw.cfg.DefaultImageSize(), s.mw.cfg.DefaultResponseModalities())
	if in.Stream {
		writeResponsesStreamFromVertex(w, r, s, actual, payload, model, stored)
		return
	}
	resp, vErr := s.chat.vc.CompleteChat(r.Context(), actual, payload)
	if vErr != nil {
		ve := toVertexError(vErr)
		if isSafetyBlock(ve) {
			out := map[string]any{"id": responseID, "object": "response", "status": "incomplete", "model": model, "output": []any{}, "incomplete_details": map[string]any{"reason": "safety"}}
			if stored != nil {
				stored.Status = "incomplete"
				stored.CompletedAt = time.Now().Unix()
				stored.Incomplete, _ = json.Marshal(out["incomplete_details"])
				stored.Output = []byte("[]")
				_, _ = s.responseStore.UpdateTerminalCAS(r.Context(), *stored)
				data, _ := json.Marshal(out)
				seq, _ := s.responseStore.AllocateEventSequence(r.Context(), "response", responseID)
				_ = s.responseStore.AppendEvent(r.Context(), responseproto.Event{ResourceKind: "response", ResourceID: responseID, Sequence: seq, EventID: responseID + "_incomplete", EventType: "response.incomplete", Data: data, CreatedAt: stored.CompletedAt})
			}
			writeJSON(w, 200, out)
		} else {
			if stored != nil {
				stored.Status = "failed"
				stored.CompletedAt = time.Now().Unix()
				stored.Error, _ = json.Marshal(map[string]any{"message": ve.Error(), "code": ve.Code})
				_, _ = s.responseStore.UpdateTerminalCAS(r.Context(), *stored)
				data, _ := json.Marshal(map[string]any{"id": responseID, "status": "failed", "error": map[string]any{"message": ve.Error(), "code": ve.Code}})
				seq, _ := s.responseStore.AllocateEventSequence(r.Context(), "response", responseID)
				_ = s.responseStore.AppendEvent(r.Context(), responseproto.Event{ResourceKind: "response", ResourceID: responseID, Sequence: seq, EventID: responseID + "_failed", EventType: "response.failed", Data: data, CreatedAt: stored.CompletedAt})
			}
			writeJSON(w, ve.Code, vertexErrorToOAI(ve))
		}
		return
	}
	items, usage, status := responseproto.OutputItems(resp)
	out := map[string]any{"id": responseID, "object": "response", "created_at": time.Now().Unix(), "status": status, "model": model, "output": items}
	if len(usage) > 0 {
		out["usage"] = usage
	}
	if stored != nil {
		stored.Status = status
		stored.CompletedAt = time.Now().Unix()
		stored.Output, _ = json.Marshal(items)
		stored.Usage, _ = json.Marshal(usage)
		_, _ = s.responseStore.UpdateTerminalCAS(r.Context(), *stored)
		for _, item := range items {
			if m, ok := item.(map[string]any); ok && m["type"] == "function_call" {
				callID := stringValueAny(m["call_id"])
				if callID != "" {
					state, _ := json.Marshal(m)
					_ = s.responseStore.PutToolState(r.Context(), responseproto.ToolState{CallID: callID, ExternalCallID: callID, ResponseID: responseID, ConversationID: stored.ConversationID, State: state, ExpiresAt: stored.ExpiresAt})
				}
			}
		}
		data, _ := json.Marshal(out)
		seq, _ := s.responseStore.AllocateEventSequence(r.Context(), "response", responseID)
		_ = s.responseStore.AppendEvent(r.Context(), responseproto.Event{ResourceKind: "response", ResourceID: responseID, Sequence: seq, EventID: responseID + "_completed", EventType: "response.completed", Data: data, CreatedAt: stored.CompletedAt})
	}
	if convID, ok := in.Conversation.(string); ok && convID != "" && s.responseStore != nil {
		userData, _ := json.Marshal(normalizedInput)
		_ = s.responseStore.AddConversationItems(r.Context(), convID, []responseproto.InputItem{{ID: "item_" + reqID24(), Data: userData, CreatedAt: time.Now().Unix()}})
		ob, _ := json.Marshal(map[string]any{"type": "message", "role": "assistant", "content": items})
		_ = s.responseStore.AddConversationItems(r.Context(), convID, []responseproto.InputItem{{ID: "item_" + reqID24(), Data: ob, CreatedAt: time.Now().Unix()}})
	}
	writeJSON(w, 200, out)
}

func (s *Server) runBackgroundResponse(ctx context.Context, cancel context.CancelFunc, rec responseproto.ResponseRecord, payload map[string]any, model string) {
	defer cancel()
	defer func() { s.backgroundMu.Lock(); delete(s.backgroundCancels, rec.ID); s.backgroundMu.Unlock() }()
	resp, err := s.chat.vc.CompleteChat(ctx, model, payload)
	if err != nil {
		b, _ := json.Marshal(map[string]any{"message": err.Error()})
		rec.Status = "failed"
		rec.Error = b
	} else {
		items, usage, status := responseproto.OutputItems(resp)
		rec.Status = status
		rec.Output, _ = json.Marshal(items)
		rec.Usage, _ = json.Marshal(usage)
	}
	rec.CompletedAt = time.Now().Unix()
	updated, _ := s.responseStore.UpdateTerminalCAS(context.Background(), rec)
	if !updated {
		return
	}
	if rec.ConversationID != "" && rec.Status == "completed" {
		ob, _ := json.Marshal(map[string]any{"type": "message", "role": "assistant", "content": json.RawMessage(rec.Output)})
		_ = s.responseStore.AddConversationItems(context.Background(), rec.ConversationID, []responseproto.InputItem{{ID: "item_" + reqID24(), Data: ob, CreatedAt: rec.CompletedAt}})
	}
	eventData, _ := json.Marshal(map[string]any{"id": rec.ID, "status": rec.Status})
	seq, _ := s.responseStore.AllocateEventSequence(context.Background(), "response", rec.ID)
	_ = s.responseStore.AppendEvent(context.Background(), responseproto.Event{ResourceKind: "response", ResourceID: rec.ID, Sequence: seq, EventID: rec.ID + "_terminal", EventType: "response." + rec.Status, Data: eventData, CreatedAt: rec.CompletedAt})
}

func (s *Server) persistResponse(r *http.Request, in responseproto.CreateRequest, id string, input []any, out map[string]any) {
	if s.responseStore == nil {
		return
	}
	b, _ := json.Marshal(out)
	ib, _ := json.Marshal(input)
	reqb, _ := json.Marshal(in)
	now := time.Now().Unix()
	rec := responseproto.ResponseRecord{ID: id, Status: stringValueAny(out["status"]), Model: in.Model, Request: reqb, Input: ib, Output: json.RawMessage(outJSON(out, "output")), Usage: json.RawMessage(outJSON(out, "usage")), CreatedAt: now, CompletedAt: now, ExpiresAt: now + 24*60*60, Store: true}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	h := sha256.Sum256(reqb)
	if key != "" {
		_, _, _, _ = s.responseStore.CreateIdempotent(r.Context(), rec, responseproto.IdempotencyRecord{Endpoint: "/v1/responses", Key: key, BodyHash: hex.EncodeToString(h[:]), ResourceKind: "response", ResourceID: id, CreatedAt: now, ExpiresAt: rec.ExpiresAt})
	} else {
		_ = s.responseStore.Create(r.Context(), rec)
	}
	seq, _ := s.responseStore.AllocateEventSequence(r.Context(), "response", id)
	_ = s.responseStore.AppendEvent(r.Context(), responseproto.Event{ResourceKind: "response", ResourceID: id, Sequence: seq, EventID: id + "_completed", EventType: "response.completed", Data: b, CreatedAt: now})
	_ = b
}

func stringValueAny(v any) string                   { s, _ := v.(string); return s }
func outJSON(out map[string]any, key string) []byte { b, _ := json.Marshal(out[key]); return b }

func (s *Server) handleResponseResource(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/responses/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		oaiError(w, 404, "response not found", "not_found")
		return
	}
	id := parts[0]
	if id == "compact" {
		oaiError(w, http.StatusNotImplemented, "responses compact is not supported", "unsupported_endpoint")
		return
	}
	if s.responseStore == nil {
		oaiError(w, http.StatusNotImplemented, "response persistence is not configured", "unsupported_endpoint")
		return
	}
	if len(parts) == 2 && parts[1] == "input_items" {
		if r.Method != http.MethodGet {
			oaiError(w, 405, "method not allowed", "invalid_request_error")
			return
		}
		items, err := s.responseStore.InputItems(r.Context(), id)
		if err != nil {
			oaiError(w, 404, "response not found", "not_found")
			return
		}
		data := make([]any, 0, len(items))
		for _, it := range items {
			var v any
			_ = json.Unmarshal(it.Data, &v)
			data = append(data, v)
		}
		writeJSON(w, 200, map[string]any{"object": "list", "data": data, "has_more": false})
		return
	}
	if len(parts) == 2 && parts[1] == "cancel" {
		if r.Method != http.MethodPost {
			oaiError(w, 405, "method not allowed", "invalid_request_error")
			return
		}
		s.backgroundMu.Lock()
		if cancel := s.backgroundCancels[id]; cancel != nil {
			cancel()
		}
		s.backgroundMu.Unlock()
		before, beforeErr := s.responseStore.Get(r.Context(), id, time.Now())
		rec, err := s.responseStore.Cancel(r.Context(), id, time.Now())
		if err != nil {
			oaiError(w, 404, "response not found", "not_found")
			return
		}
		if beforeErr == nil && before.Status == "in_progress" && rec.Status == "cancelled" {
			seq, _ := s.responseStore.AllocateEventSequence(r.Context(), "response", id)
			data, _ := json.Marshal(map[string]any{"id": id, "status": "cancelled"})
			_ = s.responseStore.AppendEvent(r.Context(), responseproto.Event{ResourceKind: "response", ResourceID: id, Sequence: seq, EventID: id + "_cancelled", EventType: "response.cancelled", Data: data, CreatedAt: time.Now().Unix()})
		}
		writeResponseRecord(w, rec)
		return
	}
	if len(parts) != 1 {
		oaiError(w, 404, "response not found", "not_found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		if r.URL.Query().Get("stream") == "true" || r.Header.Get("Last-Event-ID") != "" {
			after := r.Header.Get("Last-Event-ID")
			events, err := s.responseStore.ReplayEvents(r.Context(), "response", id, after)
			if err != nil {
				oaiError(w, 404, "response not found", "not_found")
				return
			}
			sw := newSSEWriter(w, "text/event-stream")
			for _, ev := range events {
				payload := map[string]any{"type": ev.EventType, "sequence": ev.Sequence, "event_id": ev.EventID}
				var data any
				if json.Unmarshal(ev.Data, &data) == nil {
					payload["data"] = data
				}
				_ = sw.write("id: " + ev.EventID + "\nevent: " + ev.EventType + "\n" + sseEvent(payload))
			}
			return
		}
		rec, err := s.responseStore.Get(r.Context(), id, time.Now())
		if err != nil {
			oaiError(w, 404, "response not found", "not_found")
			return
		}
		writeResponseRecord(w, rec)
	case http.MethodDelete:
		if err := s.responseStore.Delete(r.Context(), id); err != nil && !errors.Is(err, sql.ErrNoRows) {
			oaiError(w, 404, "response not found", "not_found")
			return
		}
		writeJSON(w, 200, map[string]any{"id": id, "object": "response", "deleted": true})
	default:
		oaiError(w, 405, "method not allowed", "invalid_request_error")
	}
}

func writeResponseRecord(w http.ResponseWriter, rec responseproto.ResponseRecord) {
	out := map[string]any{}
	var output any
	if len(rec.Output) > 0 && json.Unmarshal(rec.Output, &output) == nil {
		out["output"] = output
	}
	out["id"] = rec.ID
	out["status"] = rec.Status
	out["model"] = rec.Model
	out["object"] = "response"
	out["created_at"] = rec.CreatedAt
	if len(rec.Usage) > 0 {
		var v any
		if json.Unmarshal(rec.Usage, &v) == nil {
			out["usage"] = v
		}
	}
	if len(rec.Metadata) > 0 {
		var v any
		if json.Unmarshal(rec.Metadata, &v) == nil {
			out["metadata"] = v
		}
	}
	if rec.PreviousResponseID != "" {
		out["previous_response_id"] = rec.PreviousResponseID
	}
	if rec.ConversationID != "" {
		out["conversation_id"] = rec.ConversationID
	}
	if len(rec.Error) > 0 {
		var v any
		if json.Unmarshal(rec.Error, &v) == nil {
			out["error"] = v
		}
	}
	if len(rec.Incomplete) > 0 {
		var v any
		if json.Unmarshal(rec.Incomplete, &v) == nil {
			out["incomplete_details"] = v
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleConversations(w http.ResponseWriter, r *http.Request) {
	if s.responseStore == nil {
		oaiError(w, http.StatusNotImplemented, "conversation persistence is not configured", "unsupported_endpoint")
		return
	}
	if r.Method != http.MethodPost {
		oaiError(w, 405, "method not allowed", "invalid_request_error")
		return
	}
	var in struct {
		Metadata map[string]any `json:"metadata"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		oaiError(w, 400, "invalid JSON", "invalid_request_error")
		return
	}
	id := "conv_" + reqID24()
	b, _ := json.Marshal(in.Metadata)
	now := time.Now().Unix()
	if err := s.responseStore.CreateConversation(r.Context(), responseproto.Conversation{ID: id, Metadata: b, CreatedAt: now, UpdatedAt: now}); err != nil {
		oaiError(w, 500, "failed to create conversation", "server_error")
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "object": "conversation", "metadata": in.Metadata, "created_at": now})
}

func (s *Server) handleConversationResource(w http.ResponseWriter, r *http.Request) {
	if s.responseStore == nil {
		oaiError(w, http.StatusNotImplemented, "conversation persistence is not configured", "unsupported_endpoint")
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/conversations/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		oaiError(w, 404, "conversation not found", "not_found")
		return
	}
	id := parts[0]
	if len(parts) == 2 && parts[1] == "items" {
		if r.Method == http.MethodPost {
			var body struct {
				Items []any `json:"items"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				oaiError(w, 400, "invalid JSON", "invalid_request_error")
				return
			}
			if len(body.Items) == 0 {
				oaiError(w, 400, "items is required", "missing_required_parameter")
				return
			}
			items := make([]responseproto.InputItem, 0, len(body.Items))
			now := time.Now().Unix()
			for _, item := range body.Items {
				data, _ := json.Marshal(item)
				itemID := "item_" + reqID24()
				if m, ok := item.(map[string]any); ok {
					if id, ok := m["id"].(string); ok && id != "" {
						itemID = id
					}
				}
				items = append(items, responseproto.InputItem{ID: itemID, Data: data, CreatedAt: now})
			}
			if err := s.responseStore.AddConversationItems(r.Context(), id, items); err != nil {
				oaiError(w, 404, "conversation not found", "not_found")
				return
			}
			writeJSON(w, http.StatusCreated, map[string]any{"object": "list", "data": body.Items, "has_more": false})
			return
		}
		if r.Method != http.MethodGet {
			oaiError(w, 405, "method not allowed", "invalid_request_error")
			return
		}
		after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 {
			limit = 100
		}
		items, err := s.responseStore.ListConversationItems(r.Context(), id, after, limit+1)
		if err != nil {
			oaiError(w, 404, "conversation not found", "not_found")
			return
		}
		hasMore := len(items) > limit
		if hasMore {
			items = items[:limit]
		}
		data := make([]any, 0, len(items))
		for _, it := range items {
			var v any
			_ = json.Unmarshal(it.Data, &v)
			data = append(data, v)
		}
		writeJSON(w, 200, map[string]any{"object": "list", "data": data, "has_more": hasMore})
		return
	}
	switch r.Method {
	case http.MethodGet:
		c, err := s.responseStore.GetConversation(r.Context(), id)
		if err != nil {
			oaiError(w, 404, "conversation not found", "not_found")
			return
		}
		var meta any
		_ = json.Unmarshal(c.Metadata, &meta)
		writeJSON(w, 200, map[string]any{"id": c.ID, "object": "conversation", "metadata": meta, "created_at": c.CreatedAt})
	case http.MethodDelete:
		if err := s.responseStore.DeleteConversation(r.Context(), id); err != nil {
			oaiError(w, 404, "conversation not found", "not_found")
			return
		}
		writeJSON(w, 200, map[string]any{"id": id, "object": "conversation", "deleted": true})
	default:
		oaiError(w, 405, "method not allowed", "invalid_request_error")
	}
}

func writeResponsesStreamFromVertex(w http.ResponseWriter, r *http.Request, s *Server, model string, payload map[string]any, requestedModel string, stored *responseproto.ResponseRecord) {
	sw := newSSEWriter(w, "text/event-stream")
	id := "resp_" + reqID24()
	if stored != nil {
		id = stored.ID
	}
	var eventSeq int64
	emit := func(event string, obj map[string]any) bool {
		eventSeq++
		eid := id + "_" + strconv.FormatInt(eventSeq, 10)
		obj["sequence"] = eventSeq
		obj["event_id"] = eid
		if stored != nil && s.responseStore != nil {
			seq, err := s.responseStore.AllocateEventSequence(context.Background(), "response", id)
			if err == nil {
				eid = id + "_" + strconv.FormatInt(seq, 10)
				obj["sequence"] = seq
				obj["event_id"] = eid
				data, _ := json.Marshal(obj)
				_ = s.responseStore.AppendEvent(context.Background(), responseproto.Event{ResourceKind: "response", ResourceID: id, Sequence: seq, EventID: eid, EventType: event, Data: data, CreatedAt: time.Now().Unix()})
			}
		}
		return sw.write("id: " + eid + "\nevent: " + event + "\n" + sseEvent(obj))
	}
	emit("response.created", map[string]any{"type": "response.created", "response": map[string]any{"id": id, "object": "response", "model": requestedModel, "status": "in_progress"}})
	emit("response.in_progress", map[string]any{"type": "response.in_progress", "response": map[string]any{"id": id, "object": "response", "model": requestedModel, "status": "in_progress"}})
	var final map[string]any
	var text strings.Builder
	var streamErr *vertex.VertexError
	itemID := "msg_" + id
	callItems := map[string]string{}
	emit("response.output_item.added", map[string]any{"type": "response.output_item.added", "item": map[string]any{"id": itemID, "type": "message", "role": "assistant", "status": "in_progress"}})
	emit("response.content_part.added", map[string]any{"type": "response.content_part.added", "item_id": itemID, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}})
	s.chat.vc.StreamChat(r.Context(), model, payload, func(ch vertex.StreamChunk) bool {
		if ch.Err != nil {
			streamErr = toVertexError(ch.Err)
			return false
		}
		final = ch.Data
		for _, cand := range responseCandidatesForStream(ch.Data) {
			parts, _ := cand["content"].(map[string]any)
			raw, _ := parts["parts"].([]any)
			for _, p := range raw {
				pm, _ := p.(map[string]any)
				if t, _ := pm["text"].(string); t != "" && !isTruthyMap(pm["thought"]) {
					text.WriteString(t)
					if !emit("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "delta": t, "item_id": id}) {
						return false
					}
				}
				if fc, ok := pm["functionCall"].(map[string]any); ok {
					callID := stringValueAny(fc["id"])
					if callID == "" {
						callID = "call_" + reqID24()
					}
					callItem := "fc_" + callID
					if _, seen := callItems[callID]; !seen {
						callItems[callID] = callItem
						emit("response.output_item.added", map[string]any{"type": "response.output_item.added", "item": map[string]any{"id": callItem, "type": "function_call", "call_id": callID, "name": stringValueAny(fc["name"]), "status": "in_progress"}})
					}
					args, _ := json.Marshal(fc["args"])
					emit("response.function_call_arguments.delta", map[string]any{"type": "response.function_call_arguments.delta", "item_id": callItem, "delta": string(args)})
					emit("response.function_call_arguments.done", map[string]any{"type": "response.function_call_arguments.done", "item_id": callItem, "arguments": string(args)})
					emit("response.output_item.done", map[string]any{"type": "response.output_item.done", "item": map[string]any{"id": callItem, "type": "function_call", "call_id": callID, "name": stringValueAny(fc["name"]), "arguments": string(args), "status": "completed"}})
				}
			}
		}
		return true
	})
	if streamErr != nil {
		failed := map[string]any{"id": id, "object": "response", "created_at": time.Now().Unix(), "status": "failed", "model": requestedModel, "output": []any{}, "error": map[string]any{"message": vertex.FriendlyErrorMessage(streamErr), "type": "server_error", "code": streamErr.Code}}
		emit("response.failed", map[string]any{"type": "response.failed", "response": failed})
		if stored != nil {
			stored.Status = "failed"
			stored.CompletedAt = time.Now().Unix()
			stored.Error, _ = json.Marshal(failed["error"])
			_, _ = s.responseStore.UpdateTerminalCAS(context.Background(), *stored)
			data, _ := json.Marshal(failed)
			seq, _ := s.responseStore.AllocateEventSequence(context.Background(), "response", id)
			_ = s.responseStore.AppendEvent(context.Background(), responseproto.Event{ResourceKind: "response", ResourceID: id, Sequence: seq, EventID: id + "_failed", EventType: "response.failed", Data: data, CreatedAt: stored.CompletedAt})
		}
		return
	}
	emit("response.output_text.done", map[string]any{"type": "response.output_text.done", "item_id": itemID, "text": text.String()})
	emit("response.content_part.done", map[string]any{"type": "response.content_part.done", "item_id": itemID, "content_index": 0, "part": map[string]any{"type": "output_text", "text": text.String(), "annotations": []any{}}})
	emit("response.output_item.done", map[string]any{"type": "response.output_item.done", "item": map[string]any{"id": itemID, "type": "message", "role": "assistant", "status": "completed"}})
	streamItems := []any{map[string]any{"type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": text.String(), "annotations": []any{}}}}}
	if final != nil {
		if rendered, _, _ := responseproto.OutputItems(final); len(rendered) > 0 {
			streamItems = rendered
		}
	}
	completed := map[string]any{"id": id, "object": "response", "created_at": time.Now().Unix(), "status": "completed", "model": requestedModel, "output": streamItems}
	if final != nil {
		if _, usage, _ := responseproto.OutputItems(final); len(usage) > 0 {
			completed["usage"] = usage
		}
	}
	emit("response.completed", map[string]any{"type": "response.completed", "response": completed})
	if stored != nil {
		stored.Status = "completed"
		stored.CompletedAt = time.Now().Unix()
		stored.Output, _ = json.Marshal(completed["output"])
		if u, ok := completed["usage"]; ok {
			stored.Usage, _ = json.Marshal(u)
		}
		_, _ = s.responseStore.UpdateTerminalCAS(context.Background(), *stored)
		for _, item := range streamItems {
			if m, ok := item.(map[string]any); ok && m["type"] == "function_call" {
				if callID := stringValueAny(m["call_id"]); callID != "" {
					state, _ := json.Marshal(m)
					_ = s.responseStore.PutToolState(context.Background(), responseproto.ToolState{CallID: callID, ExternalCallID: callID, ResponseID: id, ConversationID: stored.ConversationID, State: state, ExpiresAt: stored.ExpiresAt})
				}
			}
		}
		data, _ := json.Marshal(completed)
		seq, _ := s.responseStore.AllocateEventSequence(context.Background(), "response", id)
		_ = s.responseStore.AppendEvent(context.Background(), responseproto.Event{ResourceKind: "response", ResourceID: id, Sequence: seq, EventID: id + "_completed", EventType: "response.completed", Data: data, CreatedAt: stored.CompletedAt})
	}
}

func responseCandidatesForStream(chunk map[string]any) []map[string]any {
	raw, _ := chunk["candidates"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, v := range raw {
		if m, ok := v.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}
func isTruthyMap(v any) bool { b, _ := v.(bool); return b }

func (s *Server) handleResponsesInputTokens(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
		return
	}
	var in map[string]any
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		oaiError(w, 400, "invalid JSON", "invalid_request_error")
		return
	}
	model, _ := in["model"].(string)
	if strings.TrimSpace(model) == "" {
		oaiError(w, 400, "model is required", "invalid_request_error")
		return
	}
	actual, _, ok := resolveConfiguredModel(model, s.mw.cfg)
	if !ok {
		oaiModelNotFound(w, model)
		return
	}
	raw, _ := json.Marshal(in)
	var req responseproto.CreateRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		oaiError(w, 400, "invalid input", "invalid_request_error")
		return
	}
	payload, _, err := responseproto.BuildGemini(req, nil)
	if err != nil {
		if ae, ok := err.(*responseproto.AdapterError); ok {
			oaiError(w, 400, ae.Message, ae.Code)
		} else {
			oaiError(w, 400, err.Error(), "invalid_request_error")
		}
		return
	}
	contents, _ := payload["contents"].([]any)
	count := s.chat.vc.CountTokens(r.Context(), actual, contents)
	writeJSON(w, http.StatusOK, map[string]any{"object": "response.input_tokens", "input_tokens": count})
}

func responsesInputMessages(v any) []any {
	switch x := v.(type) {
	case string:
		return []any{map[string]any{"role": "user", "content": x}}
	case []any:
		out := []any{}
		for _, item := range x {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			typ, _ := m["type"].(string)
			if typ == "input_text" {
				if text, ok := m["text"].(string); ok {
					out = append(out, map[string]any{"role": "user", "content": text})
				}
				continue
			}
			if typ == "message" || typ == "" {
				role, _ := m["role"].(string)
				if role == "" {
					role = "user"
				}
				content := m["content"]
				if content == nil {
					content = m["text"]
				}
				out = append(out, map[string]any{"role": role, "content": content})
			}
		}
		return out
	}
	return nil
}

func responseFromChat(chat map[string]any, model string) map[string]any {
	text := ""
	var usage any
	if cs, ok := chat["choices"].([]any); ok && len(cs) > 0 {
		c, _ := cs[0].(map[string]any)
		m, _ := c["message"].(map[string]any)
		text, _ = m["content"].(string)
	}
	usage = chat["usage"]
	out := map[string]any{"id": "resp_" + reqID24(), "object": "response", "created_at": time.Now().Unix(), "status": "completed", "model": model, "output": []any{map[string]any{"type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}}}}}
	if usage != nil {
		out["usage"] = usage
	}
	return out
}

func writeResponsesStream(w http.ResponseWriter, data []byte, model string) {
	sw := newSSEWriter(w, "text/event-stream")
	id := "resp_" + reqID24()
	_ = sw.write("event: response.created\n" + sseEvent(map[string]any{"type": "response.created", "response": map[string]any{"id": id, "object": "response", "model": model, "status": "in_progress"}}))
	_ = sw.write("event: response.in_progress\n" + sseEvent(map[string]any{"type": "response.in_progress", "response": map[string]any{"id": id, "object": "response", "model": model, "status": "in_progress"}}))
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		p := strings.TrimPrefix(line, "data: ")
		if p == "[DONE]" {
			continue
		}
		var c map[string]any
		if json.Unmarshal([]byte(p), &c) != nil {
			continue
		}
		cs, _ := c["choices"].([]any)
		if len(cs) == 0 {
			continue
		}
		ch, _ := cs[0].(map[string]any)
		d, _ := ch["delta"].(map[string]any)
		txt, _ := d["content"].(string)
		if txt != "" {
			_ = sw.write("event: response.output_text.delta\n" + sseEvent(map[string]any{"type": "response.output_text.delta", "delta": txt, "item_id": id}))
		}
	}
	_ = sw.write("event: response.completed\n" + sseEvent(map[string]any{"type": "response.completed", "response": responseFromChat(map[string]any{"model": model, "choices": []any{map[string]any{"message": map[string]any{"content": ""}}}}, model)}))
}

type responseRecorder struct {
	header http.Header
	code   int
	Body   bytes.Buffer
}

func newResponseRecorder() *responseRecorder {
	return &responseRecorder{header: make(http.Header), code: 200}
}
func (r *responseRecorder) Header() http.Header         { return r.header }
func (r *responseRecorder) WriteHeader(code int)        { r.code = code }
func (r *responseRecorder) Write(p []byte) (int, error) { return r.Body.Write(p) }
func (r *responseRecorder) Flush()                      {}
