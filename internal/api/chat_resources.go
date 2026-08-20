package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/repository"
	"github.com/bsfdsagfadg/vertex/internal/strutil"
)

func (c *ChatHandler) handleChatCompletionsRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		c.handleChatCompletions(w, r)
	case http.MethodGet:
		c.listStoredCompletions(w, r)
	default:
		oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
	}
}

func (c *ChatHandler) handleChatCompletionsSubtree(w http.ResponseWriter, r *http.Request) {
	if c.repository == nil {
		oaiResourceError(w, http.StatusServiceUnavailable, "resource_store_unavailable", "stored chat completion repository is unavailable", nil)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/chat/completions/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		oaiResourceError(w, http.StatusNotFound, "not_found", "chat completion not found", nil)
		return
	}
	id := parts[0]
	if len(parts) == 2 && parts[1] == "messages" {
		if r.Method != http.MethodGet {
			oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
			return
		}
		c.getStoredCompletionMessages(w, r, id)
		return
	}
	if len(parts) != 1 {
		oaiResourceError(w, http.StatusNotFound, "not_found", "chat completion endpoint not found", nil)
		return
	}
	switch r.Method {
	case http.MethodGet:
		c.getStoredCompletion(w, r, id)
	case http.MethodPost:
		c.updateStoredCompletion(w, r, id)
	case http.MethodDelete:
		if err := c.repository.DeleteChatCompletion(r.Context(), id); err != nil {
			c.writeChatResourceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "object": "chat.completion.deleted", "deleted": true})
	default:
		oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
	}
}

func (c *ChatHandler) persistStoredCompletion(ctx context.Context, request, response map[string]any, model string) error {
	if c.repository == nil {
		return errors.New("chat completion repository is unavailable")
	}
	now := time.Now().UTC()
	id := stringValueLocal(response["id"])
	if id == "" {
		id = "chatcmpl-" + strutil.ReqID()
		response["id"] = id
	}
	requestJSON, err := json.Marshal(request)
	if err != nil {
		return err
	}
	responseJSON, err := json.Marshal(response)
	if err != nil {
		return err
	}
	metadataJSON, err := json.Marshal(objectValue(request["metadata"]))
	if err != nil {
		return err
	}
	messagesJSON, err := json.Marshal(storedCompletionMessages(id, response, now.Unix()))
	if err != nil {
		return err
	}
	return c.repository.CreateChatCompletion(ctx, repository.ChatCompletionResource{
		ID: id, Model: model, Status: "completed", RequestJSON: requestJSON, ResponseJSON: responseJSON,
		MessagesJSON: messagesJSON, MetadataJSON: metadataJSON, CreatedAt: now.Unix(), UpdatedAt: now.Unix(),
		ExpiresAt: now.Add(storedResourceTTL).Unix(),
	})
}

func (c *ChatHandler) listStoredCompletions(w http.ResponseWriter, r *http.Request) {
	if c.repository == nil {
		oaiResourceError(w, http.StatusServiceUnavailable, "resource_store_unavailable", "stored chat completion repository is unavailable", nil)
		return
	}
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 || limit > 100 {
		limit = 20
	}
	afterID := strings.TrimSpace(r.URL.Query().Get("after"))
	afterCreated := int64(-1)
	if afterID != "" {
		cursor, cursorErr := c.repository.GetChatCompletion(r.Context(), afterID, time.Now())
		if cursorErr != nil {
			c.writeChatResourceError(w, cursorErr)
			return
		}
		afterCreated = cursor.CreatedAt
	}
	values, err := c.repository.ListChatCompletions(r.Context(), afterCreated, afterID, limit+1, time.Now())
	if err != nil {
		c.writeChatResourceError(w, err)
		return
	}
	hasMore := len(values) > limit
	if hasMore {
		values = values[:limit]
	}
	data := make([]any, 0, len(values))
	for _, value := range values {
		item, decodeErr := chatCompletionObject(value)
		if decodeErr != nil {
			c.writeChatResourceError(w, decodeErr)
			return
		}
		data = append(data, item)
	}
	firstID, lastID := "", ""
	if len(values) > 0 {
		firstID, lastID = values[0].ID, values[len(values)-1].ID
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data, "first_id": nilIfEmpty(firstID), "last_id": nilIfEmpty(lastID), "has_more": hasMore})
}

func (c *ChatHandler) getStoredCompletion(w http.ResponseWriter, r *http.Request, id string) {
	value, err := c.repository.GetChatCompletion(r.Context(), id, time.Now())
	if err != nil {
		c.writeChatResourceError(w, err)
		return
	}
	object, err := chatCompletionObject(value)
	if err != nil {
		c.writeChatResourceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, object)
}

func (c *ChatHandler) updateStoredCompletion(w http.ResponseWriter, r *http.Request, id string) {
	var body map[string]any
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&body); err != nil {
		oaiResourceError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON", nil)
		return
	}
	if len(body) != 1 {
		oaiResourceError(w, http.StatusBadRequest, "invalid_parameter", "only metadata can be updated", "metadata")
		return
	}
	metadata, ok := body["metadata"].(map[string]any)
	if !ok {
		oaiResourceError(w, http.StatusBadRequest, "invalid_parameter", "metadata must be an object", "metadata")
		return
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		oaiResourceError(w, http.StatusBadRequest, "invalid_parameter", "metadata is not valid JSON", "metadata")
		return
	}
	if err := c.repository.UpdateChatCompletionMetadata(r.Context(), id, encoded, time.Now().Unix()); err != nil {
		c.writeChatResourceError(w, err)
		return
	}
	c.getStoredCompletion(w, r, id)
}

func (c *ChatHandler) getStoredCompletionMessages(w http.ResponseWriter, r *http.Request, id string) {
	value, err := c.repository.GetChatCompletion(r.Context(), id, time.Now())
	if err != nil {
		c.writeChatResourceError(w, err)
		return
	}
	var messages []any
	if err := json.Unmarshal(value.MessagesJSON, &messages); err != nil {
		c.writeChatResourceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pageItems(messages, r))
}

func chatCompletionObject(value repository.ChatCompletionResource) (map[string]any, error) {
	var object map[string]any
	if err := json.Unmarshal(value.ResponseJSON, &object); err != nil {
		return nil, err
	}
	var metadata map[string]any
	if err := json.Unmarshal(value.MetadataJSON, &metadata); err != nil {
		return nil, err
	}
	object["metadata"] = metadata
	return object, nil
}

func storedCompletionMessages(completionID string, response map[string]any, created int64) []any {
	choices, _ := response["choices"].([]any)
	messages := make([]any, 0, len(choices))
	for index, rawChoice := range choices {
		choice, _ := rawChoice.(map[string]any)
		message, _ := choice["message"].(map[string]any)
		if message == nil {
			continue
		}
		copy := make(map[string]any, len(message)+4)
		for key, value := range message {
			copy[key] = value
		}
		copy["id"] = "chatcmpl-message-" + strutil.ReqID()
		copy["object"] = "chat.completion.message"
		copy["created"] = created
		copy["completion_id"] = completionID
		copy["index"] = index
		messages = append(messages, copy)
	}
	return messages
}

func objectValue(value any) map[string]any {
	object, _ := value.(map[string]any)
	if object == nil {
		return map[string]any{}
	}
	return object
}

func (c *ChatHandler) writeChatResourceError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		oaiResourceError(w, http.StatusNotFound, "not_found", "chat completion not found", nil)
		return
	}
	oaiResourceError(w, http.StatusInternalServerError, "internal_error", "stored chat completion operation failed", nil)
}
