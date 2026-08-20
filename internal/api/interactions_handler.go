package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/core/model"
	interactionadapter "github.com/bsfdsagfadg/vertex/internal/interactions"
	"github.com/bsfdsagfadg/vertex/internal/repository"
	"github.com/bsfdsagfadg/vertex/internal/strutil"
	"github.com/bsfdsagfadg/vertex/internal/transform"
	"github.com/bsfdsagfadg/vertex/internal/vertex"
)

func (h *ResponsesHandler) handleInteractions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		geminiResourceError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	var request interactionadapter.CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		geminiResourceError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "request body must be valid JSON")
		return
	}
	if request.Background && request.Stream {
		geminiResourceError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "background and stream cannot both be true")
		return
	}
	cfg := h.requestConfig(r)
	if strings.EqualFold(cfg.GeminiParameterPolicy(), "strict") {
		if fields := request.UnknownFields(); len(fields) > 0 {
			geminiResourceError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "unsupported request field: "+fields[0])
			return
		}
	}
	actualModel, _, ok := resolveConfiguredModel(request.Model, cfg)
	if !ok {
		geminiModelNotFound(w, request.Model)
		return
	}
	request.Model = actualModel
	history, err := h.interactionHistory(r.Context(), request.PreviousInteractionID)
	if err != nil {
		h.writeInteractionError(w, err)
		return
	}
	payload, inputItems, err := interactionadapter.BuildGemini(request, history)
	if err != nil {
		h.writeInteractionError(w, err)
		return
	}
	if h.platform != nil {
		if err := h.platform.expandLocalResources(r.Context(), payload); err != nil {
			h.writeInteractionError(w, &interactionadapter.AdapterError{Code: "state_not_available", Param: "input", Message: err.Error()})
			return
		}
	}
	if h.toolStates != nil {
		if err := h.toolStates.RestoreOpenAIChat(r.Context(), payload); err != nil {
			h.writeInteractionError(w, err)
			return
		}
	}
	if _, err := transform.ApplyModelPolicy(payload, actualModel, model.ParsePolicy(cfg.GeminiParameterPolicy(), model.PolicyPassthrough)); err != nil {
		h.writeInteractionError(w, err)
		return
	}
	requestJSON, _ := json.Marshal(request)
	labelsJSON, _ := json.Marshal(request.Labels)
	inputSteps := interactionInputSteps(inputItems)
	stepsJSON, _ := json.Marshal(inputSteps)
	store := request.Store == nil || *request.Store
	now := time.Now().UTC()
	ttl := transientResourceTTL
	if store {
		ttl = storedResourceTTL
	}
	resource := repository.InteractionResource{
		ID: "int_" + strutil.ReqID(), Status: "in_progress", Model: actualModel, RequestJSON: requestJSON,
		StepsJSON: stepsJSON, UsageJSON: []byte("{}"), ErrorJSON: []byte("null"), PreviousInteractionID: request.PreviousInteractionID, LabelsJSON: labelsJSON,
		Store: store, Background: request.Background, CreatedAt: now.Unix(), UpdatedAt: now.Unix(), ExpiresAt: now.Add(ttl).Unix(),
	}
	idempotency := repository.IdempotencyRecord{
		Endpoint: "POST /v1beta/interactions", Key: strings.TrimSpace(r.Header.Get("Idempotency-Key")), BodyHash: sha256Hex(requestJSON),
		ResourceKind: "interaction", ResourceID: resource.ID, CreatedAt: now.Unix(), ExpiresAt: now.Add(idempotencyTTL).Unix(),
	}
	resourceID, replay, conflict, err := h.repository.CreateInteractionIdempotent(r.Context(), resource, idempotency)
	if err != nil {
		h.writeInteractionError(w, err)
		return
	}
	if conflict {
		geminiResourceError(w, http.StatusConflict, "ALREADY_EXISTS", "Idempotency-Key was already used with a different request body")
		return
	}
	if replay {
		existing, getErr := h.repository.GetInteraction(r.Context(), resourceID, time.Now())
		if getErr != nil {
			h.writeInteractionError(w, getErr)
			return
		}
		writeJSON(w, http.StatusOK, interactionObject(existing))
		return
	}
	if request.Background {
		if err := h.startBackgroundInteraction(resource, payload); err != nil {
			h.failInteraction(r.Context(), &resource, err)
			h.writeInteractionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, interactionObject(resource))
		return
	}
	executionCtx, executionDone := h.trackExecution(r.Context(), "interaction:"+resource.ID)
	defer executionDone()
	if request.Stream {
		h.executeInteractionStream(executionCtx, w, &resource, payload)
		return
	}
	emitter := interactionEventEmitter{repo: h.repository, resource: &resource}
	if !emitter.emit("interaction.created", map[string]any{"interaction": interactionObject(resource)}) ||
		!emitter.emit("interaction.status_update", map[string]any{"interaction_id": resource.ID, "status": "in_progress"}) {
		err := errors.New("persist interaction lifecycle events")
		h.failInteraction(executionCtx, &resource, err)
		h.writeInteractionError(w, err)
		return
	}
	if err := h.executeInteraction(executionCtx, &resource, payload); err != nil {
		if resource.Status == "cancelled" {
			_ = emitter.emit("interaction.status_update", map[string]any{"interaction_id": resource.ID, "status": "cancelled"})
		} else {
			_ = emitter.emit("error", map[string]any{"error": map[string]any{"message": err.Error(), "code": "upstream_error"}})
		}
		h.writeInteractionError(w, err)
		return
	}
	var initialSteps []any
	_ = json.Unmarshal(stepsJSON, &initialSteps)
	emitStoredInteractionSteps(&emitter, resource.StepsJSON, len(initialSteps))
	_ = emitter.emit("interaction.completed", map[string]any{"interaction": interactionObject(resource)})
	writeJSON(w, http.StatusOK, interactionObject(resource))
}

func (h *ResponsesHandler) executeInteraction(ctx context.Context, resource *repository.InteractionResource, payload map[string]any) error {
	response, err := h.vc.CompleteChat(ctx, resource.Model, payload)
	if err != nil {
		h.failInteraction(ctx, resource, err)
		return err
	}
	transform.AssignExternalFunctionCallIDs(response)
	steps, usage, status := interactionadapter.Steps(response)
	var existing []any
	_ = json.Unmarshal(resource.StepsJSON, &existing)
	resource.StepsJSON, _ = json.Marshal(append(existing, steps...))
	resource.UsageJSON, _ = json.Marshal(usage)
	resource.Status = status
	resource.UpdatedAt = time.Now().UTC().Unix()
	if h.toolStates != nil {
		if err := h.toolStates.CaptureResponse(ctx, response, "", resource.ID, "interactions"); err != nil {
			h.failInteraction(ctx, resource, err)
			return err
		}
	}
	completed, err := h.repository.CompleteInteractionIfInProgress(ctx, *resource)
	if err != nil {
		return err
	}
	if !completed {
		_ = h.repository.DeleteToolStatesForResource(context.Background(), "interaction", resource.ID)
		if current, getErr := h.repository.GetInteraction(context.Background(), resource.ID, time.Now()); getErr == nil {
			*resource = current
		}
		return context.Canceled
	}
	return nil
}

func (h *ResponsesHandler) executeInteractionStream(ctx context.Context, w http.ResponseWriter, resource *repository.InteractionResource, payload map[string]any) {
	sw := newSSEWriter(w, "text/event-stream")
	emitter := interactionEventEmitter{repo: h.repository, resource: resource, write: sw.write}
	if !emitter.emit("interaction.created", map[string]any{"interaction": interactionObject(*resource)}) ||
		!emitter.emit("interaction.status_update", map[string]any{"interaction_id": resource.ID, "status": "in_progress"}) {
		return
	}
	tracker := transform.NewStreamToolCallTracker()
	stepRecords := map[int]any{}
	nextStepIndex := 0
	var currentText strings.Builder
	textStepID := "step_" + strutil.ReqID()
	textStarted := false
	textStepIndex := -1
	toolSteps := map[string]map[string]any{}
	toolIndexes := map[string]int{}
	toolOrder := make([]string, 0)
	var streamErr error
	lastUsage := map[string]any{}
	h.vc.StreamChat(ctx, resource.Model, payload, func(chunk vertex.StreamChunk) bool {
		if chunk.Err != nil {
			streamErr = chunk.Err
			return false
		}
		_ = transform.ConvertRealtimeChunkWithUsage(chunk.Data, resource.Model, resource.ID, false, true, tracker)
		if usage, ok := chunk.Data["usageMetadata"].(map[string]any); ok {
			converted := transform.ConvertUsage(usage)
			lastUsage = map[string]any{"total_input_tokens": converted["prompt_tokens"], "total_output_tokens": converted["completion_tokens"], "total_tokens": converted["total_tokens"]}
		}
		for _, candidate := range responseCandidatesLocal(chunk.Data) {
			for _, rawPart := range candidatePartsLocal(candidate) {
				part, _ := rawPart.(map[string]any)
				if text := stringValueLocal(part["text"]); text != "" && !boolValue(part["thought"]) {
					if !textStarted {
						textStarted = true
						textStepIndex = nextStepIndex
						nextStepIndex++
						if !emitter.emit("step.start", map[string]any{"index": textStepIndex, "step": map[string]any{"id": textStepID, "type": "model_output", "content": []any{}}}) {
							return false
						}
					}
					currentText.WriteString(text)
					if !emitter.emit("step.delta", map[string]any{"index": textStepIndex, "step_id": textStepID, "delta": map[string]any{"type": "text", "text": text}}) {
						return false
					}
				}
				if call, ok := part["functionCall"].(map[string]any); ok {
					callID := stringValueLocal(call["id"])
					if callID == "" {
						continue
					}
					arguments, _ := json.Marshal(call["args"])
					step, exists := toolSteps[callID]
					if !exists {
						step = map[string]any{"id": "step_" + strutil.ReqID(), "type": "function_call", "call_id": callID, "name": call["name"], "arguments": ""}
						toolSteps[callID] = step
						toolIndexes[callID] = nextStepIndex
						toolOrder = append(toolOrder, callID)
						nextStepIndex++
						if !emitter.emit("step.start", map[string]any{"index": toolIndexes[callID], "step": step}) {
							return false
						}
					}
					previousArguments, _ := step["arguments"].(string)
					currentArguments := string(arguments)
					step["arguments"] = currentArguments
					delta := incrementalDelta(previousArguments, currentArguments)
					if delta != "" && !emitter.emit("step.delta", map[string]any{"index": toolIndexes[callID], "step_id": step["id"], "delta": map[string]any{"arguments": delta}}) {
						return false
					}
				}
			}
		}
		return true
	})
	if streamErr == nil {
		streamErr = tracker.CaptureError()
	}
	if streamErr != nil {
		h.failInteraction(ctx, resource, streamErr)
		_ = emitter.emit("error", map[string]any{"error": map[string]any{"message": streamErr.Error(), "code": "upstream_error"}})
		return
	}
	if textStarted {
		step := map[string]any{"id": textStepID, "type": "model_output", "content": []any{map[string]any{"type": "text", "text": currentText.String()}}}
		_ = emitter.emit("step.stop", map[string]any{"index": textStepIndex, "step": step})
		stepRecords[textStepIndex] = step
	}
	for _, callID := range toolOrder {
		step := toolSteps[callID]
		index := toolIndexes[callID]
		_ = emitter.emit("step.stop", map[string]any{"index": index, "step": step})
		stepRecords[index] = step
	}
	steps := make([]any, 0, len(stepRecords))
	for index := 0; index < nextStepIndex; index++ {
		if step, ok := stepRecords[index]; ok {
			steps = append(steps, step)
		}
	}
	var existing []any
	_ = json.Unmarshal(resource.StepsJSON, &existing)
	resource.StepsJSON, _ = json.Marshal(append(existing, steps...))
	resource.UsageJSON, _ = json.Marshal(lastUsage)
	resource.Status = "completed"
	resource.UpdatedAt = time.Now().UTC().Unix()
	if h.toolStates != nil {
		if err := h.toolStates.CaptureResponse(ctx, tracker.CapturedResponse(), "", resource.ID, "interactions"); err != nil {
			h.failInteraction(ctx, resource, err)
			_ = emitter.emit("error", map[string]any{"error": map[string]any{"message": err.Error(), "code": "tool_state_store_error"}})
			return
		}
	}
	completed, err := h.repository.CompleteInteractionIfInProgress(ctx, *resource)
	if err != nil {
		h.failInteraction(ctx, resource, err)
		_ = emitter.emit("error", map[string]any{"error": map[string]any{"message": "resource persistence failed", "code": "internal_error"}})
		return
	}
	if !completed {
		_ = h.repository.DeleteToolStatesForResource(context.Background(), "interaction", resource.ID)
		if current, getErr := h.repository.GetInteraction(context.Background(), resource.ID, time.Now()); getErr == nil {
			*resource = current
		}
		_ = emitter.emit("interaction.status_update", map[string]any{"interaction_id": resource.ID, "status": resource.Status})
		return
	}
	_ = emitter.emit("interaction.completed", map[string]any{"interaction": interactionObject(*resource)})
}

func (h *ResponsesHandler) startBackgroundInteraction(resource repository.InteractionResource, payload map[string]any) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.runtimeCtx == nil || h.runtimeCtx.Err() != nil {
		return errors.New("resource runtime is not started")
	}
	ctx, cancel := context.WithCancel(h.runtimeCtx)
	h.jobs["interaction:"+resource.ID] = cancel
	now := time.Now().UTC()
	job := repository.BackgroundJob{ID: "job_" + strutil.ReqID(), ResourceKind: "interaction", ResourceID: resource.ID, Status: "in_progress", CreatedAt: now.Unix(), UpdatedAt: now.Unix(), ExpiresAt: now.Add(backgroundJobTTL).Unix()}
	if err := h.repository.PutBackgroundJob(ctx, job); err != nil {
		delete(h.jobs, "interaction:"+resource.ID)
		cancel()
		return err
	}
	emitter := interactionEventEmitter{repo: h.repository, resource: &resource}
	_ = emitter.emit("interaction.created", map[string]any{"interaction": interactionObject(resource)})
	_ = emitter.emit("interaction.status_update", map[string]any{"interaction_id": resource.ID, "status": "in_progress"})
	var initialSteps []any
	_ = json.Unmarshal(resource.StepsJSON, &initialSteps)
	initialStepCount := len(initialSteps)
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		err := h.executeInteraction(ctx, &resource, payload)
		h.mu.Lock()
		delete(h.jobs, "interaction:"+resource.ID)
		h.mu.Unlock()
		job.UpdatedAt = time.Now().UTC().Unix()
		current, getErr := h.repository.GetInteraction(context.Background(), resource.ID, time.Now())
		if errors.Is(getErr, sql.ErrNoRows) {
			return
		}
		if getErr == nil {
			resource = current
		}
		job.Status = resource.Status
		if err != nil {
			if resource.Status == "cancelled" {
				job.Status = "cancelled"
				_ = emitter.emit("interaction.status_update", map[string]any{"interaction_id": resource.ID, "status": "cancelled"})
			} else {
				job.Status = "failed"
				job.ErrorJSON, _ = json.Marshal(map[string]any{"message": err.Error()})
				_ = emitter.emit("error", map[string]any{"error": map[string]any{"message": err.Error(), "code": "upstream_error"}})
			}
		} else {
			emitStoredInteractionSteps(&emitter, resource.StepsJSON, initialStepCount)
			_ = emitter.emit("interaction.completed", map[string]any{"interaction": interactionObject(resource)})
		}
		_ = h.repository.PutBackgroundJob(context.Background(), job)
	}()
	return nil
}

func emitStoredInteractionSteps(emitter *interactionEventEmitter, stepsJSON []byte, start int) {
	var steps []any
	if json.Unmarshal(stepsJSON, &steps) != nil || start > len(steps) {
		return
	}
	for index, rawStep := range steps[start:] {
		step, _ := rawStep.(map[string]any)
		outputIndex := index
		_ = emitter.emit("step.start", map[string]any{"index": outputIndex, "step": step})
		switch step["type"] {
		case "model_output":
			for _, rawContent := range anySliceLocal(step["content"]) {
				content, _ := rawContent.(map[string]any)
				if text := stringValueLocal(content["text"]); text != "" {
					_ = emitter.emit("step.delta", map[string]any{"index": outputIndex, "step_id": step["id"], "delta": map[string]any{"type": "text", "text": text}})
				}
			}
		case "function_call":
			_ = emitter.emit("step.delta", map[string]any{"index": outputIndex, "step_id": step["id"], "delta": map[string]any{"arguments": step["arguments"]}})
		}
		_ = emitter.emit("step.stop", map[string]any{"index": outputIndex, "step": step})
	}
}

func (h *ResponsesHandler) handleInteractionsSubtree(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1beta/interactions/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		geminiResourceError(w, http.StatusNotFound, "NOT_FOUND", "interaction not found")
		return
	}
	id := parts[0]
	if len(parts) == 2 && parts[1] == "cancel" {
		if r.Method != http.MethodPost {
			geminiResourceError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		h.cancelInteraction(w, r, id)
		return
	}
	if len(parts) != 1 {
		geminiResourceError(w, http.StatusNotFound, "NOT_FOUND", "interaction endpoint not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		if boolQuery(r, "stream") {
			h.replayInteractionEvents(w, r, id)
			return
		}
		resource, err := h.repository.GetInteraction(r.Context(), id, time.Now())
		if err != nil {
			h.writeInteractionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, interactionObject(resource))
	case http.MethodDelete:
		h.cancelJobOnly("interaction:" + id)
		if err := h.repository.DeleteInteraction(r.Context(), id); err != nil {
			h.writeInteractionError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		geminiResourceError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

func (h *ResponsesHandler) cancelInteraction(w http.ResponseWriter, r *http.Request, id string) {
	h.cancelJobOnly("interaction:" + id)
	resource, err := h.repository.CancelInteraction(r.Context(), id, time.Now().UTC().Unix())
	if err != nil {
		h.writeInteractionError(w, err)
		return
	}
	if resource.Status == "cancelled" {
		emitter := interactionEventEmitter{repo: h.repository, resource: &resource}
		_ = emitter.emit("interaction.status_update", map[string]any{"interaction_id": id, "status": "cancelled"})
	}
	writeJSON(w, http.StatusOK, interactionObject(resource))
}

func (h *ResponsesHandler) replayInteractionEvents(w http.ResponseWriter, r *http.Request, id string) {
	if _, err := h.repository.GetInteraction(r.Context(), id, time.Now()); err != nil {
		h.writeInteractionError(w, err)
		return
	}
	sw := newSSEWriter(w, "text/event-stream")
	after := r.URL.Query().Get("last_event_id")
	for {
		events, err := h.repository.ListResourceEvents(r.Context(), "interaction", id, after)
		if err != nil {
			h.writeInteractionError(w, err)
			return
		}
		for _, event := range events {
			if !sw.write("id: " + event.EventID + "\nevent: " + event.EventType + "\ndata: " + string(event.EventJSON) + "\n\n") {
				return
			}
			after = event.EventID
		}
		resource, err := h.repository.GetInteraction(r.Context(), id, time.Now())
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

func (h *ResponsesHandler) interactionHistory(ctx context.Context, previousID string) ([]any, error) {
	if previousID == "" {
		return nil, nil
	}
	previous, err := h.repository.GetInteraction(ctx, previousID, time.Now())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &interactionadapter.AdapterError{Code: "state_not_available", Param: "previous_interaction_id", Message: "previous interaction state is not available"}
		}
		return nil, err
	}
	if previous.Status != "completed" && previous.Status != "incomplete" {
		return nil, &interactionadapter.AdapterError{Code: "state_not_available", Param: "previous_interaction_id", Message: "previous interaction has no reusable terminal state"}
	}
	return interactionadapter.HistoryItems(previous.StepsJSON)
}

func (h *ResponsesHandler) failInteraction(ctx context.Context, resource *repository.InteractionResource, err error) {
	if errors.Is(err, context.Canceled) {
		if current, getErr := h.repository.GetInteraction(context.Background(), resource.ID, time.Now()); getErr == nil && current.Status == "cancelled" {
			*resource = current
			return
		}
	}
	resource.Status = "failed"
	resource.UpdatedAt = time.Now().UTC().Unix()
	resource.ErrorJSON, _ = json.Marshal(map[string]any{"message": err.Error(), "code": "upstream_error"})
	updated, _ := h.repository.CompleteInteractionIfInProgress(ctx, *resource)
	if !updated {
		if current, getErr := h.repository.GetInteraction(context.Background(), resource.ID, time.Now()); getErr == nil {
			*resource = current
		}
	}
}

func (h *ResponsesHandler) writeInteractionError(w http.ResponseWriter, err error) {
	var adapterErr *interactionadapter.AdapterError
	if errors.As(err, &adapterErr) {
		status := http.StatusBadRequest
		if adapterErr.Code == "state_not_available" || adapterErr.Code == "tool_state_missing" {
			status = http.StatusConflict
		}
		geminiResourceError(w, status, strings.ToUpper(adapterErr.Code), adapterErr.Message)
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		geminiResourceError(w, http.StatusNotFound, "NOT_FOUND", "interaction not found")
		return
	}
	var vertexErr *vertex.VertexError
	if errors.As(err, &vertexErr) {
		writeJSON(w, vertexErr.Code, vertexErrorToGemini(vertexErr))
		return
	}
	geminiResourceError(w, http.StatusInternalServerError, "INTERNAL", "internal interaction error")
}

func interactionObject(resource repository.InteractionResource) map[string]any {
	var steps []any
	var usage, labels map[string]any
	var errorValue any
	_ = json.Unmarshal(resource.StepsJSON, &steps)
	_ = json.Unmarshal(resource.UsageJSON, &usage)
	_ = json.Unmarshal(resource.LabelsJSON, &labels)
	_ = json.Unmarshal(resource.ErrorJSON, &errorValue)
	result := map[string]any{
		"id": resource.ID, "object": "interaction", "model": resource.Model, "status": resource.Status,
		"created": time.Unix(resource.CreatedAt, 0).UTC().Format(time.RFC3339),
		"updated": time.Unix(resource.UpdatedAt, 0).UTC().Format(time.RFC3339),
		"steps":   steps, "usage": usage, "labels": labels,
	}
	if resource.Agent != "" {
		result["agent"] = resource.Agent
	}
	if errorValue != nil {
		result["error"] = errorValue
	}
	return result
}

type interactionEventEmitter struct {
	repo     *repository.SQLite
	resource *repository.InteractionResource
	write    func(string) bool
}

func (e *interactionEventEmitter) emit(eventType string, fields map[string]any) bool {
	eventID := "evt_" + strutil.ReqID()
	fields["event_type"] = eventType
	fields["event_id"] = eventID
	data, err := json.Marshal(fields)
	if err != nil {
		return false
	}
	sequence, err := e.repo.AllocateResourceEventSequence(context.Background(), "interaction", e.resource.ID)
	if err != nil {
		return false
	}
	event := repository.ResourceEvent{
		ResourceKind: "interaction", ResourceID: e.resource.ID, Sequence: sequence, EventID: eventID,
		EventType: eventType, EventJSON: data, CreatedAt: time.Now().UTC().Unix(),
	}
	if err := e.repo.AppendResourceEvent(context.Background(), event); err != nil {
		return false
	}
	if e.write == nil {
		return true
	}
	return e.write("id: " + eventID + "\nevent: " + eventType + "\ndata: " + string(data) + "\n\n")
}

func interactionInputSteps(items []any) []any {
	steps := make([]any, 0, len(items))
	for _, rawItem := range items {
		item, _ := rawItem.(map[string]any)
		if item["type"] == "message" {
			steps = append(steps, map[string]any{"id": "step_" + strutil.ReqID(), "type": "user_input", "content": item["content"]})
		} else {
			copy := map[string]any{"id": "step_" + strutil.ReqID()}
			for key, value := range item {
				copy[key] = value
			}
			steps = append(steps, copy)
		}
	}
	return steps
}

func geminiResourceError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": status, "message": message, "status": code}})
}

func boolQuery(r *http.Request, key string) bool {
	value := strings.ToLower(strings.TrimSpace(r.URL.Query().Get(key)))
	return value == "1" || value == "true"
}

func anySliceLocal(value any) []any {
	items, _ := value.([]any)
	return items
}
