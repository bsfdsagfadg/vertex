package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/strutil"
	"github.com/bsfdsagfadg/vertex/internal/transform"
)

var legacyCompletionFields = map[string]bool{ //nolint:gochecknoglobals
	"model": true, "prompt": true, "best_of": true, "echo": true, "frequency_penalty": true,
	"logprobs": true, "max_tokens": true, "n": true, "presence_penalty": true, "seed": true,
	"stop": true, "stream": true, "stream_options": true, "suffix": true, "temperature": true,
	"top_p": true, "user": true,
}

func (c *ChatHandler) handleLegacyCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		oaiResourceError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON", nil)
		return
	}
	cfg := c.requestConfig(r)
	unknown := legacyUnknownFields(body)
	if len(unknown) > 0 {
		if strings.EqualFold(cfg.OpenAIParameterPolicy(), "strict") {
			oaiResourceError(w, http.StatusBadRequest, "unknown_parameter", "unsupported request field: "+unknown[0], unknown[0])
			return
		}
		w.Header().Set("X-VProxy-Transform-Warnings", "ignored_legacy_fields:"+strings.Join(unknown, ","))
	}
	rawModel, _ := body["model"].(string)
	if strings.TrimSpace(rawModel) == "" {
		oaiResourceError(w, http.StatusBadRequest, "missing_required_parameter", "missing required field 'model'", "model")
		return
	}
	actualModel, _, ok := resolveConfiguredModel(rawModel, cfg)
	if !ok {
		oaiModelNotFound(w, rawModel)
		return
	}
	prompts, err := legacyPrompts(body["prompt"])
	if err != nil {
		oaiResourceError(w, http.StatusBadRequest, "invalid_parameter", err.Error(), "prompt")
		return
	}
	n, nErr := resolveN(body["n"], cfg.MaxN())
	if nErr != "" {
		oaiResourceError(w, http.StatusBadRequest, "invalid_parameter", nErr, "n")
		return
	}
	if len(prompts)*n > 20 {
		oaiResourceError(w, http.StatusBadRequest, "invalid_parameter", "prompt count multiplied by n must not exceed 20", "n")
		return
	}
	if numberGreaterThanOne(body["best_of"]) {
		oaiResourceError(w, http.StatusBadRequest, "unsupported_parameter", "best_of greater than 1 is unavailable through the anonymous upstream", "best_of")
		return
	}
	if body["logprobs"] != nil {
		oaiResourceError(w, http.StatusBadRequest, "unsupported_parameter", "logprobs are unavailable through the anonymous upstream", "logprobs")
		return
	}
	echo, _ := body["echo"].(bool)
	allResponses := make([]map[string]any, 0, len(prompts)*n)
	for _, prompt := range prompts {
		chatBody := legacyChatBody(body, actualModel, prompt)
		model, payload, convErr := c.reqConv.Convert(chatBody, cfg)
		if convErr != nil {
			var policyErr *transform.PolicyError
			if errors.As(convErr, &policyErr) {
				oaiResourceError(w, http.StatusBadRequest, policyErr.Code, policyErr.Message, policyErr.Param)
				return
			}
			oaiResourceError(w, http.StatusBadRequest, "invalid_parameter", convErr.Error(), nil)
			return
		}
		if c.platform != nil {
			if expandErr := c.platform.expandLocalResources(r.Context(), payload); expandErr != nil {
				oaiResourceError(w, http.StatusConflict, "state_not_available", expandErr.Error(), nil)
				return
			}
		}
		if !applyRequestModelPolicy(w, payload, actualModel, cfg.OpenAIParameterPolicy(), "openai") {
			return
		}
		responses, completeErr := c.vc.CompleteChatN(r.Context(), model, payload, n)
		if completeErr != nil {
			ve := toVertexError(completeErr)
			writeJSON(w, ve.Code, vertexErrorToOAI(ve))
			return
		}
		allResponses = append(allResponses, responses...)
	}
	chatResponse := c.respConv.AggregateN(allResponses, actualModel)
	result := legacyCompletionResponse(chatResponse, actualModel, prompts, n, echo)
	if stream, _ := body["stream"].(bool); stream {
		w.Header().Set("X-VProxy-Stream-Mode", "simulated")
		writeLegacyCompletionStream(w, result)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func legacyUnknownFields(body map[string]any) []string {
	fields := make([]string, 0)
	for field := range body {
		if !legacyCompletionFields[field] {
			fields = append(fields, field)
		}
	}
	sort.Strings(fields)
	return fields
}

func legacyPrompts(raw any) ([]string, error) {
	switch value := raw.(type) {
	case string:
		return []string{value}, nil
	case []any:
		if len(value) == 0 || len(value) > 20 {
			return nil, errors.New("prompt array must contain 1 to 20 strings")
		}
		prompts := make([]string, 0, len(value))
		for _, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil, errors.New("prompt array must contain only strings")
			}
			prompts = append(prompts, text)
		}
		return prompts, nil
	default:
		return nil, errors.New("prompt must be a string or an array of strings")
	}
}

func legacyChatBody(source map[string]any, model, prompt string) map[string]any {
	result := map[string]any{"model": model, "messages": []any{map[string]any{"role": "user", "content": prompt}}}
	for _, field := range []string{"frequency_penalty", "max_tokens", "presence_penalty", "seed", "stop", "temperature", "top_p", "user"} {
		if value, exists := source[field]; exists {
			result[field] = value
		}
	}
	return result
}

func legacyCompletionResponse(chat map[string]any, model string, prompts []string, n int, echo bool) map[string]any {
	created := time.Now().Unix()
	choices := make([]any, 0)
	rawChoices, _ := chat["choices"].([]any)
	for index, raw := range rawChoices {
		choice, _ := raw.(map[string]any)
		message, _ := choice["message"].(map[string]any)
		text, _ := message["content"].(string)
		if echo && len(prompts) > 0 {
			promptIndex := index / n
			if promptIndex >= len(prompts) {
				promptIndex = len(prompts) - 1
			}
			text = prompts[promptIndex] + text
		}
		choices = append(choices, map[string]any{"text": text, "index": index, "logprobs": nil, "finish_reason": choice["finish_reason"]})
	}
	result := map[string]any{"id": "cmpl-" + strutil.ReqID(), "object": "text_completion", "created": created, "model": model, "choices": choices}
	if usage, ok := chat["usage"]; ok {
		result["usage"] = usage
	}
	return result
}

func writeLegacyCompletionStream(w http.ResponseWriter, result map[string]any) {
	sw := newSSEWriter(w, "text/event-stream")
	choices, _ := result["choices"].([]any)
	for _, raw := range choices {
		choice, _ := raw.(map[string]any)
		chunk := map[string]any{"id": result["id"], "object": "text_completion", "created": result["created"], "model": result["model"], "choices": []any{choice}}
		if !sw.write(sseEvent(chunk)) {
			return
		}
	}
	_ = sw.write("data: [DONE]\n\n")
}

func numberGreaterThanOne(value any) bool {
	switch number := value.(type) {
	case float64:
		return number > 1
	case int:
		return number > 1
	default:
		return false
	}
}
