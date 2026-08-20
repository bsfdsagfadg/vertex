package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/core/model"
	"github.com/bsfdsagfadg/vertex/internal/repository"
	responseadapter "github.com/bsfdsagfadg/vertex/internal/responses"
	"github.com/bsfdsagfadg/vertex/internal/strutil"
	"github.com/bsfdsagfadg/vertex/internal/transform"
	"github.com/bsfdsagfadg/vertex/internal/vertex"
)

const (
	storedResourceTTL    = 30 * 24 * time.Hour
	transientResourceTTL = time.Hour
	backgroundJobTTL     = 24 * time.Hour
	idempotencyTTL       = 24 * time.Hour
)

type ResponsesHandler struct {
	handler
	mu         sync.Mutex
	runtimeCtx context.Context
	cancel     context.CancelFunc
	jobs       map[string]context.CancelFunc
	wg         sync.WaitGroup
}

func NewResponsesHandler(h handler) *ResponsesHandler {
	if h.repository == nil {
		return nil
	}
	return &ResponsesHandler{handler: h, jobs: map[string]context.CancelFunc{}}
}

func (h *ResponsesHandler) Start(parent context.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cancel != nil {
		return
	}
	h.runtimeCtx, h.cancel = context.WithCancel(parent)
}

func (h *ResponsesHandler) Close() {
	h.mu.Lock()
	if h.cancel != nil {
		h.cancel()
	}
	for _, cancel := range h.jobs {
		cancel()
	}
	h.jobs = map[string]context.CancelFunc{}
	h.cancel = nil
	h.runtimeCtx = nil
	h.mu.Unlock()
	h.wg.Wait()
}

func (h *ResponsesHandler) handleResponses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
		return
	}
	var request responseadapter.CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		oaiResourceError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON", nil)
		return
	}
	if request.Background && request.Stream {
		oaiResourceError(w, http.StatusBadRequest, "invalid_parameter", "background and stream cannot both be true", "stream")
		return
	}
	cfg := h.requestConfig(r)
	if strings.EqualFold(cfg.OpenAIParameterPolicy(), "strict") {
		if fields := request.UnknownFields(); len(fields) > 0 {
			oaiResourceError(w, http.StatusBadRequest, "unknown_parameter", "unsupported request field: "+fields[0], fields[0])
			return
		}
	}
	actualModel, _, ok := resolveConfiguredModel(request.Model, cfg)
	if !ok {
		oaiModelNotFound(w, request.Model)
		return
	}
	request.Model = actualModel
	history, conversationID, err := h.responseHistory(r.Context(), request)
	if err != nil {
		h.writeResponseError(w, err)
		return
	}
	payload, inputItems, err := responseadapter.BuildGemini(request, history)
	if err != nil {
		h.writeResponseError(w, err)
		return
	}
	if h.platform != nil {
		if err := h.platform.expandLocalResources(r.Context(), payload); err != nil {
			h.writeResponseError(w, &responseadapter.AdapterError{Code: "state_not_available", Param: "input", Message: err.Error()})
			return
		}
	}
	if h.toolStates != nil {
		if err := h.toolStates.RestoreOpenAIChat(r.Context(), payload); err != nil {
			h.writeResponseError(w, err)
			return
		}
	}
	if _, err := transform.ApplyModelPolicy(payload, actualModel, model.ParsePolicy(cfg.OpenAIParameterPolicy(), model.PolicyAdaptive)); err != nil {
		h.writeResponseError(w, err)
		return
	}
	requestJSON, _ := json.Marshal(request)
	inputJSON, _ := json.Marshal(inputItems)
	metadataJSON, _ := json.Marshal(request.Metadata)
	store := request.Store == nil || *request.Store
	now := time.Now().UTC()
	ttl := transientResourceTTL
	if store {
		ttl = storedResourceTTL
	}
	resource := repository.ResponseResource{
		ID: "resp_" + strutil.ReqID(), Status: "in_progress", Model: actualModel,
		RequestJSON: requestJSON, InputJSON: inputJSON, OutputJSON: []byte("[]"), UsageJSON: []byte("{}"), ErrorJSON: []byte("null"), IncompleteJSON: []byte("null"), MetadataJSON: metadataJSON,
		PreviousResponseID: request.PreviousResponseID, ConversationID: conversationID,
		Store: store, Background: request.Background, CreatedAt: now.Unix(), ExpiresAt: now.Add(ttl).Unix(),
	}
	bodyHash := sha256Hex(requestJSON)
	idempotency := repository.IdempotencyRecord{
		Endpoint: "POST /v1/responses", Key: strings.TrimSpace(r.Header.Get("Idempotency-Key")), BodyHash: bodyHash,
		ResourceKind: "response", ResourceID: resource.ID, CreatedAt: now.Unix(), ExpiresAt: now.Add(idempotencyTTL).Unix(),
	}
	resourceID, replay, conflict, err := h.repository.CreateResponseIdempotent(r.Context(), resource, idempotency)
	if err != nil {
		h.writeResponseError(w, err)
		return
	}
	if conflict {
		oaiResourceError(w, http.StatusConflict, "idempotency_conflict", "Idempotency-Key was already used with a different request body", nil)
		return
	}
	if replay {
		existing, getErr := h.repository.GetResponse(r.Context(), resourceID, time.Now())
		if getErr != nil {
			h.writeResponseError(w, getErr)
			return
		}
		writeJSON(w, http.StatusOK, responseObject(existing))
		return
	}
	if request.Conversation != nil && conversationID != "" {
		items := makeConversationItems(conversationID, inputItems, now)
		if err := h.repository.AddConversationItems(r.Context(), conversationID, items); err != nil {
			h.failResponse(r.Context(), &resource, err)
			h.writeResponseError(w, err)
			return
		}
	}
	if request.Background {
		if err := h.startBackgroundResponse(resource, payload); err != nil {
			h.failResponse(r.Context(), &resource, err)
			h.writeResponseError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, responseObject(resource))
		return
	}
	executionCtx, executionDone := h.trackExecution(r.Context(), "response:"+resource.ID)
	defer executionDone()
	if request.Stream {
		h.executeResponseStream(executionCtx, w, &resource, payload)
		return
	}
	emitter := responseEventEmitter{repo: h.repository, resource: &resource}
	if !emitter.emit("response.created", map[string]any{"response": responseObject(resource)}) ||
		!emitter.emit("response.in_progress", map[string]any{"response": responseObject(resource)}) {
		err := errors.New("persist response lifecycle events")
		h.failResponse(executionCtx, &resource, err)
		h.writeResponseError(w, err)
		return
	}
	if err := h.executeResponse(executionCtx, &resource, payload); err != nil {
		_ = emitter.emit(responseTerminalEvent(resource.Status), map[string]any{"response": responseObject(resource)})
		h.writeResponseError(w, err)
		return
	}
	emitStoredResponseOutput(&emitter, resource.OutputJSON)
	_ = emitter.emit(responseTerminalEvent(resource.Status), map[string]any{"response": responseObject(resource)})
	writeJSON(w, http.StatusOK, responseObject(resource))
}

func (h *ResponsesHandler) executeResponse(ctx context.Context, resource *repository.ResponseResource, payload map[string]any) error {
	response, err := h.vc.CompleteChat(ctx, resource.Model, payload)
	if err != nil {
		h.failResponse(ctx, resource, err)
		return err
	}
	transform.AssignExternalFunctionCallIDs(response)
	items, usage, status := responseadapter.OutputItems(response)
	resource.OutputJSON, _ = json.Marshal(items)
	resource.UsageJSON, _ = json.Marshal(usage)
	resource.Status = status
	resource.CompletedAt = time.Now().UTC().Unix()
	if status == "incomplete" {
		resource.IncompleteJSON = []byte(`{"reason":"max_output_or_provider_limit"}`)
	}
	if h.toolStates != nil {
		if err := h.toolStates.CaptureResponse(ctx, response, resource.ID, resource.ConversationID, "responses"); err != nil {
			h.failResponse(ctx, resource, err)
			return err
		}
	}
	completed, err := h.repository.CompleteResponseIfInProgress(ctx, *resource)
	if err != nil {
		return err
	}
	if !completed {
		_ = h.repository.DeleteToolStatesForResource(context.Background(), "response", resource.ID)
		if current, getErr := h.repository.GetResponse(context.Background(), resource.ID, time.Now()); getErr == nil {
			*resource = current
		}
		return context.Canceled
	}
	if resource.ConversationID != "" {
		_ = h.repository.AddConversationItems(ctx, resource.ConversationID, makeConversationItems(resource.ConversationID, items, time.Now().UTC()))
	}
	return nil
}

func (h *ResponsesHandler) executeResponseStream(ctx context.Context, w http.ResponseWriter, resource *repository.ResponseResource, payload map[string]any) {
	sw := newSSEWriter(w, "text/event-stream")
	emitter := responseEventEmitter{repo: h.repository, resource: resource, write: sw.write}
	if !emitter.emit("response.created", map[string]any{"response": responseObject(*resource)}) ||
		!emitter.emit("response.in_progress", map[string]any{"response": responseObject(*resource)}) {
		return
	}
	tracker := transform.NewStreamToolCallTracker()
	messageID := "msg_" + strutil.ReqID()
	messageAdded := false
	messageIndex := -1
	nextOutputIndex := 0
	contentAdded := false
	var textOutput strings.Builder
	toolItems := map[string]map[string]any{}
	toolIndexes := map[string]int{}
	toolOrder := make([]string, 0)
	lastUsage := map[string]any{}
	var streamErr error
	h.vc.StreamChat(ctx, resource.Model, payload, func(chunk vertex.StreamChunk) bool {
		if chunk.Err != nil {
			streamErr = chunk.Err
			return false
		}
		_ = transform.ConvertRealtimeChunkWithUsage(chunk.Data, resource.Model, resource.ID, false, true, tracker)
		if usage, ok := chunk.Data["usageMetadata"].(map[string]any); ok {
			converted := transform.ConvertUsage(usage)
			lastUsage = map[string]any{"input_tokens": converted["prompt_tokens"], "output_tokens": converted["completion_tokens"], "total_tokens": converted["total_tokens"]}
		}
		for _, candidate := range responseCandidatesLocal(chunk.Data) {
			for _, rawPart := range candidatePartsLocal(candidate) {
				part, _ := rawPart.(map[string]any)
				if text, _ := part["text"].(string); text != "" && !boolValue(part["thought"]) {
					if !messageAdded {
						messageAdded = true
						messageIndex = nextOutputIndex
						nextOutputIndex++
						if !emitter.emit("response.output_item.added", map[string]any{"output_index": messageIndex, "item": map[string]any{"id": messageID, "type": "message", "status": "in_progress", "role": "assistant", "content": []any{}}}) {
							return false
						}
					}
					if !contentAdded {
						contentAdded = true
						if !emitter.emit("response.content_part.added", map[string]any{"item_id": messageID, "output_index": messageIndex, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}}) {
							return false
						}
					}
					textOutput.WriteString(text)
					if !emitter.emit("response.output_text.delta", map[string]any{"item_id": messageID, "output_index": messageIndex, "content_index": 0, "delta": text}) {
						return false
					}
				}
				if call, ok := part["functionCall"].(map[string]any); ok {
					callID := stringValueLocal(call["id"])
					if callID == "" {
						continue
					}
					arguments, _ := json.Marshal(call["args"])
					item, exists := toolItems[callID]
					if !exists {
						item = map[string]any{"id": "fc_" + strutil.ReqID(), "type": "function_call", "status": "in_progress", "call_id": callID, "name": stringValueLocal(call["name"]), "arguments": ""}
						toolItems[callID] = item
						toolOrder = append(toolOrder, callID)
						toolIndexes[callID] = nextOutputIndex
						nextOutputIndex++
						if !emitter.emit("response.output_item.added", map[string]any{"output_index": toolIndexes[callID], "item": item}) {
							return false
						}
					}
					previousArguments, _ := item["arguments"].(string)
					currentArguments := string(arguments)
					item["arguments"] = currentArguments
					delta := incrementalDelta(previousArguments, currentArguments)
					if delta != "" && !emitter.emit("response.function_call_arguments.delta", map[string]any{"item_id": item["id"], "output_index": toolIndexes[callID], "delta": delta}) {
						return false
					}
				}
			}
		}
		return true
	})
	if streamErr != nil || tracker.CaptureError() != nil {
		if streamErr == nil {
			streamErr = tracker.CaptureError()
		}
		h.failResponse(ctx, resource, streamErr)
		_ = emitter.emit("response.failed", map[string]any{"response": responseObject(*resource)})
		return
	}
	output := make([]any, nextOutputIndex)
	if messageAdded {
		item := map[string]any{"id": messageID, "type": "message", "status": "completed", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": textOutput.String(), "annotations": []any{}}}}
		_ = emitter.emit("response.output_text.done", map[string]any{"item_id": messageID, "output_index": messageIndex, "content_index": 0, "text": textOutput.String()})
		_ = emitter.emit("response.content_part.done", map[string]any{"item_id": messageID, "output_index": messageIndex, "content_index": 0, "part": item["content"].([]any)[0]})
		_ = emitter.emit("response.output_item.done", map[string]any{"output_index": messageIndex, "item": item})
		output[messageIndex] = item
	}
	for _, callID := range toolOrder {
		item := toolItems[callID]
		item["status"] = "completed"
		index := toolIndexes[callID]
		_ = emitter.emit("response.function_call_arguments.done", map[string]any{"item_id": item["id"], "output_index": index, "arguments": item["arguments"]})
		_ = emitter.emit("response.output_item.done", map[string]any{"output_index": index, "item": item})
		output[index] = item
	}
	resource.OutputJSON, _ = json.Marshal(output)
	resource.UsageJSON, _ = json.Marshal(lastUsage)
	resource.Status = "completed"
	resource.CompletedAt = time.Now().UTC().Unix()
	if h.toolStates != nil {
		if err := h.toolStates.CaptureResponse(ctx, tracker.CapturedResponse(), resource.ID, resource.ConversationID, "responses"); err != nil {
			h.failResponse(ctx, resource, err)
			_ = emitter.emit("response.failed", map[string]any{"response": responseObject(*resource)})
			return
		}
	}
	completed, err := h.repository.CompleteResponseIfInProgress(ctx, *resource)
	if err != nil {
		h.failResponse(ctx, resource, err)
		_ = emitter.emit("response.failed", map[string]any{"response": responseObject(*resource)})
		return
	}
	if !completed {
		_ = h.repository.DeleteToolStatesForResource(context.Background(), "response", resource.ID)
		if current, getErr := h.repository.GetResponse(context.Background(), resource.ID, time.Now()); getErr == nil {
			*resource = current
		}
		_ = emitter.emit("response.cancelled", map[string]any{"response": responseObject(*resource)})
		return
	}
	_ = emitter.emit("response.completed", map[string]any{"response": responseObject(*resource)})
}

func (h *ResponsesHandler) startBackgroundResponse(resource repository.ResponseResource, payload map[string]any) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.runtimeCtx == nil || h.runtimeCtx.Err() != nil {
		return errors.New("resource runtime is not started")
	}
	ctx, cancel := context.WithCancel(h.runtimeCtx)
	h.jobs["response:"+resource.ID] = cancel
	now := time.Now().UTC()
	job := repository.BackgroundJob{ID: "job_" + strutil.ReqID(), ResourceKind: "response", ResourceID: resource.ID, Status: "in_progress", CreatedAt: now.Unix(), UpdatedAt: now.Unix(), ExpiresAt: now.Add(backgroundJobTTL).Unix()}
	if err := h.repository.PutBackgroundJob(ctx, job); err != nil {
		delete(h.jobs, "response:"+resource.ID)
		cancel()
		return err
	}
	emitter := responseEventEmitter{repo: h.repository, resource: &resource}
	_ = emitter.emit("response.created", map[string]any{"response": responseObject(resource)})
	_ = emitter.emit("response.in_progress", map[string]any{"response": responseObject(resource)})
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		err := h.executeResponse(ctx, &resource, payload)
		h.mu.Lock()
		delete(h.jobs, "response:"+resource.ID)
		h.mu.Unlock()
		job.UpdatedAt = time.Now().UTC().Unix()
		current, getErr := h.repository.GetResponse(context.Background(), resource.ID, time.Now())
		if errors.Is(getErr, sql.ErrNoRows) {
			return
		}
		if getErr == nil {
			resource = current
		}
		job.Status = resource.Status
		if err != nil && resource.Status != "cancelled" {
			job.Status = "failed"
			job.ErrorJSON, _ = json.Marshal(map[string]any{"message": err.Error()})
		}
		if err == nil {
			emitStoredResponseOutput(&emitter, resource.OutputJSON)
			_ = emitter.emit(responseTerminalEvent(resource.Status), map[string]any{"response": responseObject(resource)})
		} else if resource.Status == "cancelled" {
			_ = emitter.emit("response.cancelled", map[string]any{"response": responseObject(resource)})
		} else {
			_ = emitter.emit("response.failed", map[string]any{"response": responseObject(resource)})
		}
		_ = h.repository.PutBackgroundJob(context.Background(), job)
	}()
	return nil
}

func responseTerminalEvent(status string) string {
	switch status {
	case "cancelled":
		return "response.cancelled"
	case "failed":
		return "response.failed"
	case "incomplete":
		return "response.incomplete"
	default:
		return "response.completed"
	}
}

func emitStoredResponseOutput(emitter *responseEventEmitter, outputJSON []byte) {
	var items []any
	if json.Unmarshal(outputJSON, &items) != nil {
		return
	}
	for index, rawItem := range items {
		item, _ := rawItem.(map[string]any)
		_ = emitter.emit("response.output_item.added", map[string]any{"output_index": index, "item": item})
		switch item["type"] {
		case "message":
			for contentIndex, rawContent := range anySliceLocal(item["content"]) {
				content, _ := rawContent.(map[string]any)
				_ = emitter.emit("response.content_part.added", map[string]any{"item_id": item["id"], "output_index": index, "content_index": contentIndex, "part": content})
				if text := stringValueLocal(content["text"]); text != "" {
					_ = emitter.emit("response.output_text.delta", map[string]any{"item_id": item["id"], "output_index": index, "content_index": contentIndex, "delta": text})
					_ = emitter.emit("response.output_text.done", map[string]any{"item_id": item["id"], "output_index": index, "content_index": contentIndex, "text": text})
				}
				_ = emitter.emit("response.content_part.done", map[string]any{"item_id": item["id"], "output_index": index, "content_index": contentIndex, "part": content})
			}
		case "function_call":
			arguments := stringValueLocal(item["arguments"])
			_ = emitter.emit("response.function_call_arguments.delta", map[string]any{"item_id": item["id"], "output_index": index, "delta": arguments})
			_ = emitter.emit("response.function_call_arguments.done", map[string]any{"item_id": item["id"], "output_index": index, "arguments": arguments})
		}
		_ = emitter.emit("response.output_item.done", map[string]any{"output_index": index, "item": item})
	}
}

func (h *ResponsesHandler) handleResponsesSubtree(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/responses/")
	if path == "compact" {
		oaiResourceError(w, http.StatusNotImplemented, "unsupported_endpoint", "response compaction is not available through the anonymous upstream", nil)
		return
	}
	if path == "input_tokens" {
		h.handleResponseInputTokens(w, r)
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		oaiResourceError(w, http.StatusNotFound, "not_found", "response not found", nil)
		return
	}
	id := parts[0]
	if len(parts) == 2 && parts[1] == "cancel" {
		if r.Method != http.MethodPost {
			oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
			return
		}
		h.cancelResource(w, r, "response", id)
		return
	}
	if len(parts) == 2 && parts[1] == "input_items" {
		if r.Method != http.MethodGet {
			oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
			return
		}
		resource, err := h.repository.GetResponse(r.Context(), id, time.Now())
		if err != nil {
			h.writeResponseError(w, err)
			return
		}
		var items []any
		_ = json.Unmarshal(resource.InputJSON, &items)
		writeJSON(w, http.StatusOK, pageItems(items, r))
		return
	}
	if len(parts) != 1 {
		oaiResourceError(w, http.StatusNotFound, "not_found", "response endpoint not found", nil)
		return
	}
	switch r.Method {
	case http.MethodGet:
		if boolQuery(r, "stream") || strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/event-stream") {
			h.replayResponseEvents(w, r, id)
			return
		}
		resource, err := h.repository.GetResponse(r.Context(), id, time.Now())
		if err != nil {
			h.writeResponseError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, responseObject(resource))
	case http.MethodDelete:
		h.cancelJobOnly("response:" + id)
		if err := h.repository.DeleteResponse(r.Context(), id); err != nil {
			h.writeResponseError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "object": "response.deleted", "deleted": true})
	default:
		oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
	}
}

func (h *ResponsesHandler) replayResponseEvents(w http.ResponseWriter, r *http.Request, id string) {
	if _, err := h.repository.GetResponse(r.Context(), id, time.Now()); err != nil {
		h.writeResponseError(w, err)
		return
	}
	sw := newSSEWriter(w, "text/event-stream")
	after := r.URL.Query().Get("last_event_id")
	if after == "" {
		after = strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	}
	for {
		events, err := h.repository.ListResourceEvents(r.Context(), "response", id, after)
		if err != nil {
			h.writeResponseError(w, err)
			return
		}
		for _, event := range events {
			if !sw.write("id: " + event.EventID + "\nevent: " + event.EventType + "\ndata: " + string(event.EventJSON) + "\n\n") {
				return
			}
			after = event.EventID
		}
		resource, err := h.repository.GetResponse(r.Context(), id, time.Now())
		if err != nil || resource.Status != "in_progress" {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (h *ResponsesHandler) handleResponseInputTokens(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
		return
	}
	var request responseadapter.CreateRequest
	if json.NewDecoder(r.Body).Decode(&request) != nil {
		oaiResourceError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON", nil)
		return
	}
	cfg := h.requestConfig(r)
	if strings.EqualFold(cfg.OpenAIParameterPolicy(), "strict") {
		if fields := request.UnknownFields(); len(fields) > 0 {
			oaiResourceError(w, http.StatusBadRequest, "unknown_parameter", "unsupported request field: "+fields[0], fields[0])
			return
		}
	}
	actualModel, _, ok := resolveConfiguredModel(request.Model, cfg)
	if !ok {
		oaiModelNotFound(w, request.Model)
		return
	}
	request.Model = actualModel
	history, _, err := h.responseHistory(r.Context(), request)
	if err != nil {
		h.writeResponseError(w, err)
		return
	}
	payload, _, err := responseadapter.BuildGemini(request, history)
	if err != nil {
		h.writeResponseError(w, err)
		return
	}
	if h.platform != nil {
		if err := h.platform.expandLocalResources(r.Context(), payload); err != nil {
			h.writeResponseError(w, &responseadapter.AdapterError{Code: "state_not_available", Param: "input", Message: err.Error()})
			return
		}
	}
	contents, _ := payload["contents"].([]any)
	count, err := h.vc.CountTokens(r.Context(), actualModel, contents)
	if err != nil {
		h.writeResponseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "response.input_tokens", "input_tokens": count})
}

func (h *ResponsesHandler) cancelResource(w http.ResponseWriter, r *http.Request, kind, id string) {
	key := kind + ":" + id
	h.cancelJobOnly(key)
	if kind == "response" {
		resource, err := h.repository.CancelResponse(r.Context(), id, time.Now().UTC().Unix())
		if err != nil {
			h.writeResponseError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, responseObject(resource))
	}
}

func (h *ResponsesHandler) cancelJobOnly(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cancel := h.jobs[key]; cancel != nil {
		cancel()
		delete(h.jobs, key)
	}
}

func (h *ResponsesHandler) trackExecution(parent context.Context, key string) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	h.mu.Lock()
	h.jobs[key] = cancel
	h.mu.Unlock()
	return ctx, func() {
		cancel()
		h.mu.Lock()
		delete(h.jobs, key)
		h.mu.Unlock()
	}
}

func (h *ResponsesHandler) responseHistory(ctx context.Context, request responseadapter.CreateRequest) ([]any, string, error) {
	history := make([]any, 0)
	if request.PreviousResponseID != "" {
		previous, err := h.repository.GetResponse(ctx, request.PreviousResponseID, time.Now())
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, "", &responseadapter.AdapterError{Code: "state_not_available", Param: "previous_response_id", Message: "previous response state is not available"}
			}
			return nil, "", err
		}
		if previous.Status != "completed" && previous.Status != "incomplete" {
			return nil, "", &responseadapter.AdapterError{Code: "state_not_available", Param: "previous_response_id", Message: "previous response has no reusable terminal state"}
		}
		var input, output []any
		if err := json.Unmarshal(previous.InputJSON, &input); err != nil {
			return nil, "", errors.New("stored previous response input is invalid")
		}
		if err := json.Unmarshal(previous.OutputJSON, &output); err != nil {
			return nil, "", errors.New("stored previous response output is invalid")
		}
		history = append(history, input...)
		history = append(history, output...)
	}
	conversationID := conversationIDFrom(request.Conversation)
	if conversationID != "" {
		if _, err := h.repository.GetConversation(ctx, conversationID); err != nil {
			return nil, "", err
		}
		after := int64(-1)
		for {
			items, err := h.repository.ListConversationItems(ctx, conversationID, after, 100, false)
			if err != nil {
				return nil, "", err
			}
			for _, item := range items {
				var value any
				if err := json.Unmarshal(item.ItemJSON, &value); err != nil {
					return nil, "", errors.New("stored conversation item is invalid")
				}
				history = append(history, value)
				after = item.Ordinal
			}
			if len(items) < 100 {
				break
			}
		}
	}
	return history, conversationID, nil
}

func (h *ResponsesHandler) failResponse(ctx context.Context, resource *repository.ResponseResource, err error) {
	if errors.Is(err, context.Canceled) {
		if current, getErr := h.repository.GetResponse(context.Background(), resource.ID, time.Now()); getErr == nil && current.Status == "cancelled" {
			*resource = current
			return
		}
	}
	resource.Status = "failed"
	resource.CompletedAt = time.Now().UTC().Unix()
	resource.ErrorJSON, _ = json.Marshal(map[string]any{"message": err.Error(), "code": "upstream_error"})
	updated, _ := h.repository.CompleteResponseIfInProgress(ctx, *resource)
	if !updated {
		if current, getErr := h.repository.GetResponse(context.Background(), resource.ID, time.Now()); getErr == nil {
			*resource = current
		}
	}
}

func (h *ResponsesHandler) writeResponseError(w http.ResponseWriter, err error) {
	var adapterErr *responseadapter.AdapterError
	if errors.As(err, &adapterErr) {
		status := http.StatusBadRequest
		if adapterErr.Code == "tool_state_missing" || adapterErr.Code == "state_not_available" {
			status = http.StatusConflict
		}
		oaiResourceError(w, status, adapterErr.Code, adapterErr.Message, adapterErr.Param)
		return
	}
	var policyErr *transform.PolicyError
	if errors.As(err, &policyErr) {
		oaiResourceError(w, http.StatusBadRequest, policyErr.Code, policyErr.Message, policyErr.Param)
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		oaiResourceError(w, http.StatusNotFound, "not_found", "resource not found", nil)
		return
	}
	var vertexErr *vertex.VertexError
	if errors.As(err, &vertexErr) {
		writeJSON(w, vertexErr.Code, vertexErrorToOAI(vertexErr))
		return
	}
	oaiResourceError(w, http.StatusInternalServerError, "internal_error", "internal resource error", nil)
}

func responseObject(resource repository.ResponseResource) map[string]any {
	var input, output []any
	var usage, metadata map[string]any
	var errorValue, incomplete any
	_ = json.Unmarshal(resource.InputJSON, &input)
	_ = json.Unmarshal(resource.OutputJSON, &output)
	_ = json.Unmarshal(resource.UsageJSON, &usage)
	_ = json.Unmarshal(resource.MetadataJSON, &metadata)
	_ = json.Unmarshal(resource.ErrorJSON, &errorValue)
	_ = json.Unmarshal(resource.IncompleteJSON, &incomplete)
	result := map[string]any{
		"id": resource.ID, "object": "response", "created_at": resource.CreatedAt, "status": resource.Status,
		"completed_at": nil, "error": errorValue, "incomplete_details": incomplete, "model": resource.Model,
		"output": output, "parallel_tool_calls": true, "previous_response_id": nilIfEmpty(resource.PreviousResponseID),
		"conversation": conversationRef(resource.ConversationID), "store": resource.Store, "background": resource.Background,
		"usage": usage, "metadata": metadata,
	}
	if resource.CompletedAt != 0 {
		result["completed_at"] = resource.CompletedAt
	}
	return result
}

type responseEventEmitter struct {
	repo     *repository.SQLite
	resource *repository.ResponseResource
	write    func(string) bool
}

func (e *responseEventEmitter) emit(eventType string, fields map[string]any) bool {
	sequence, err := e.repo.AllocateResourceEventSequence(context.Background(), "response", e.resource.ID)
	if err != nil {
		return false
	}
	fields["type"] = eventType
	fields["sequence_number"] = sequence
	data, err := json.Marshal(fields)
	if err != nil {
		return false
	}
	eventID := "evt_" + strutil.ReqID()
	if err := e.repo.AppendResourceEvent(context.Background(), repository.ResourceEvent{
		ResourceKind: "response", ResourceID: e.resource.ID, Sequence: sequence, EventID: eventID,
		EventType: eventType, EventJSON: data, CreatedAt: time.Now().UTC().Unix(),
	}); err != nil {
		return false
	}
	if e.write == nil {
		return true
	}
	return e.write("id: " + eventID + "\nevent: " + eventType + "\ndata: " + string(data) + "\n\n")
}

func oaiResourceError(w http.ResponseWriter, status int, code, message string, param any) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"message": message, "type": "invalid_request_error", "param": param, "code": code}})
}

func pageItems(items []any, r *http.Request) map[string]any {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	start := 0
	if after := r.URL.Query().Get("after"); after != "" {
		for index, rawItem := range items {
			item, _ := rawItem.(map[string]any)
			if stringValueLocal(item["id"]) == after {
				start = index + 1
				break
			}
		}
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	data := items[start:end]
	firstID, lastID := "", ""
	if len(data) > 0 {
		firstID = itemID(data[0])
		lastID = itemID(data[len(data)-1])
	}
	return map[string]any{"object": "list", "data": data, "first_id": nilIfEmpty(firstID), "last_id": nilIfEmpty(lastID), "has_more": end < len(items)}
}

func makeConversationItems(conversationID string, values []any, now time.Time) []repository.ConversationItem {
	items := make([]repository.ConversationItem, 0, len(values))
	for _, value := range values {
		encoded, _ := json.Marshal(value)
		id := itemID(value)
		if id == "" {
			id = "item_" + strutil.ReqID()
		}
		items = append(items, repository.ConversationItem{ID: id, ConversationID: conversationID, ItemJSON: encoded, CreatedAt: now.Unix()})
	}
	return items
}

func conversationIDFrom(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	if object, ok := value.(map[string]any); ok {
		return strings.TrimSpace(stringValueLocal(object["id"]))
	}
	return ""
}

func conversationRef(id string) any {
	if id == "" {
		return nil
	}
	return map[string]any{"id": id}
}

func itemID(value any) string {
	item, _ := value.(map[string]any)
	return stringValueLocal(item["id"])
}

func responseCandidatesLocal(response map[string]any) []map[string]any {
	raw, _ := response["candidates"].([]any)
	result := make([]map[string]any, 0, len(raw))
	for _, value := range raw {
		if candidate, ok := value.(map[string]any); ok {
			result = append(result, candidate)
		}
	}
	return result
}

func candidatePartsLocal(candidate map[string]any) []any {
	content, _ := candidate["content"].(map[string]any)
	parts, _ := content["parts"].([]any)
	return parts
}

func stringValueLocal(value any) string {
	text, _ := value.(string)
	return text
}

func incrementalDelta(previous, current string) string {
	if strings.HasPrefix(current, previous) {
		return strings.TrimPrefix(current, previous)
	}
	if current == previous {
		return ""
	}
	return current
}

func sha256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func (h *ResponsesHandler) handleConversations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
		return
	}
	var body struct {
		Items    []any          `json:"items"`
		Metadata map[string]any `json:"metadata"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Items) > 20 {
		oaiResourceError(w, http.StatusBadRequest, "invalid_parameter", "items must contain at most 20 valid items", "items")
		return
	}
	now := time.Now().UTC()
	metadata, _ := json.Marshal(body.Metadata)
	conversation := repository.Conversation{ID: "conv_" + strutil.ReqID(), MetadataJSON: metadata, CreatedAt: now.Unix(), UpdatedAt: now.Unix()}
	if err := h.repository.CreateConversation(r.Context(), conversation, makeConversationItems(conversation.ID, body.Items, now)); err != nil {
		h.writeResponseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, conversationObject(conversation))
}

func (h *ResponsesHandler) handleConversationsSubtree(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/conversations/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		oaiResourceError(w, http.StatusNotFound, "not_found", "conversation not found", nil)
		return
	}
	conversationID := parts[0]
	if len(parts) >= 2 && parts[1] == "items" {
		h.handleConversationItems(w, r, conversationID, parts[2:])
		return
	}
	if len(parts) != 1 {
		oaiResourceError(w, http.StatusNotFound, "not_found", "conversation endpoint not found", nil)
		return
	}
	switch r.Method {
	case http.MethodGet:
		value, err := h.repository.GetConversation(r.Context(), conversationID)
		if err != nil {
			h.writeResponseError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, conversationObject(value))
	case http.MethodPost:
		var body struct {
			Metadata map[string]any `json:"metadata"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			oaiResourceError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON", nil)
			return
		}
		metadata, _ := json.Marshal(body.Metadata)
		if err := h.repository.UpdateConversation(r.Context(), conversationID, metadata, time.Now().UTC().Unix()); err != nil {
			h.writeResponseError(w, err)
			return
		}
		value, _ := h.repository.GetConversation(r.Context(), conversationID)
		writeJSON(w, http.StatusOK, conversationObject(value))
	case http.MethodDelete:
		if err := h.repository.DeleteConversation(r.Context(), conversationID); err != nil {
			h.writeResponseError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": conversationID, "object": "conversation.deleted", "deleted": true})
	default:
		oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
	}
}

func (h *ResponsesHandler) handleConversationItems(w http.ResponseWriter, r *http.Request, conversationID string, rest []string) {
	if len(rest) == 1 && rest[0] != "" {
		itemID := rest[0]
		switch r.Method {
		case http.MethodGet:
			item, err := h.repository.GetConversationItem(r.Context(), conversationID, itemID)
			if err != nil {
				h.writeResponseError(w, err)
				return
			}
			var value any
			_ = json.Unmarshal(item.ItemJSON, &value)
			writeJSON(w, http.StatusOK, value)
		case http.MethodDelete:
			if err := h.repository.DeleteConversationItem(r.Context(), conversationID, itemID); err != nil {
				h.writeResponseError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"id": itemID, "object": "conversation.item.deleted", "deleted": true})
		default:
			oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
		}
		return
	}
	switch r.Method {
	case http.MethodGet:
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		items, err := h.repository.ListConversationItems(r.Context(), conversationID, -1, limit, strings.EqualFold(r.URL.Query().Get("order"), "desc"))
		if err != nil {
			h.writeResponseError(w, err)
			return
		}
		values := make([]any, 0, len(items))
		for _, item := range items {
			var value any
			_ = json.Unmarshal(item.ItemJSON, &value)
			values = append(values, value)
		}
		writeJSON(w, http.StatusOK, pageItems(values, r))
	case http.MethodPost:
		var body struct {
			Items []any `json:"items"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Items) == 0 || len(body.Items) > 20 {
			oaiResourceError(w, http.StatusBadRequest, "invalid_parameter", "items must contain 1 to 20 items", "items")
			return
		}
		if err := h.repository.AddConversationItems(r.Context(), conversationID, makeConversationItems(conversationID, body.Items, time.Now().UTC())); err != nil {
			h.writeResponseError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": body.Items, "has_more": false})
	default:
		oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
	}
}

func conversationObject(value repository.Conversation) map[string]any {
	metadata := map[string]any{}
	_ = json.Unmarshal(value.MetadataJSON, &metadata)
	return map[string]any{"id": value.ID, "object": "conversation", "created_at": value.CreatedAt, "metadata": metadata}
}
