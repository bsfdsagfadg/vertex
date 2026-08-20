package responses

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/strutil"
	"github.com/bsfdsagfadg/vertex/internal/transform"
)

type CreateRequest struct {
	Model              string                     `json:"model"`
	Input              any                        `json:"input"`
	Instructions       any                        `json:"instructions,omitempty"`
	Tools              []any                      `json:"tools,omitempty"`
	ToolChoice         any                        `json:"tool_choice,omitempty"`
	ParallelToolCalls  *bool                      `json:"parallel_tool_calls,omitempty"`
	MaxOutputTokens    any                        `json:"max_output_tokens,omitempty"`
	Reasoning          map[string]any             `json:"reasoning,omitempty"`
	Text               map[string]any             `json:"text,omitempty"`
	Stream             bool                       `json:"stream,omitempty"`
	Store              *bool                      `json:"store,omitempty"`
	Background         bool                       `json:"background,omitempty"`
	PreviousResponseID string                     `json:"previous_response_id,omitempty"`
	Conversation       any                        `json:"conversation,omitempty"`
	Metadata           map[string]any             `json:"metadata,omitempty"`
	Include            []string                   `json:"include,omitempty"`
	Temperature        any                        `json:"temperature,omitempty"`
	TopP               any                        `json:"top_p,omitempty"`
	Truncation         any                        `json:"truncation,omitempty"`
	ServiceTier        any                        `json:"service_tier,omitempty"`
	SafetyIdentifier   string                     `json:"safety_identifier,omitempty"`
	PromptCacheKey     string                     `json:"prompt_cache_key,omitempty"`
	RawExtensions      map[string]json.RawMessage `json:"-"`
}

func (r *CreateRequest) UnmarshalJSON(data []byte) error {
	type plain CreateRequest
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range responseRequestFields {
		delete(fields, key)
	}
	*r = CreateRequest(decoded)
	r.RawExtensions = fields
	return nil
}

func (r CreateRequest) MarshalJSON() ([]byte, error) {
	type plain CreateRequest
	encoded, err := json.Marshal(plain(r))
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return nil, err
	}
	delete(fields, "RawExtensions")
	for key, value := range r.RawExtensions {
		if _, exists := fields[key]; !exists {
			fields[key] = value
		}
	}
	return json.Marshal(fields)
}

var responseRequestFields = []string{ //nolint:gochecknoglobals
	"model", "input", "instructions", "tools", "tool_choice", "parallel_tool_calls", "max_output_tokens",
	"reasoning", "text", "stream", "store", "background", "previous_response_id", "conversation", "metadata",
	"include", "temperature", "top_p", "truncation", "service_tier", "safety_identifier", "prompt_cache_key",
}

func (r CreateRequest) UnknownFields() []string {
	fields := make([]string, 0, len(r.RawExtensions))
	for key := range r.RawExtensions {
		fields = append(fields, key)
	}
	sort.Strings(fields)
	return fields
}

type AdapterError struct {
	Code    string
	Param   string
	Message string
}

func (e *AdapterError) Error() string { return e.Message }

func BuildGemini(request CreateRequest, history []any) (map[string]any, []any, error) {
	if strings.TrimSpace(request.Model) == "" {
		return nil, nil, &AdapterError{Code: "missing_required_parameter", Param: "model", Message: "model is required"}
	}
	inputItems, err := NormalizeInput(request.Input)
	if err != nil {
		return nil, nil, err
	}
	allItems := append(append([]any{}, history...), inputItems...)
	contents, err := itemsToContents(allItems)
	if err != nil {
		return nil, nil, err
	}
	payload := map[string]any{"contents": contents}
	if instructionParts := normalizeTextContent(request.Instructions, "input_text"); len(instructionParts) > 0 {
		payload["systemInstruction"] = map[string]any{"parts": instructionParts}
	}
	if len(request.Tools) > 0 {
		declarations := make([]any, 0, len(request.Tools))
		for index, rawTool := range request.Tools {
			tool, _ := rawTool.(map[string]any)
			if strings.ToLower(stringValue(tool["type"])) != "function" {
				return nil, nil, &AdapterError{Code: "unsupported_tool", Param: fmt.Sprintf("tools[%d].type", index), Message: "only client function tools are supported"}
			}
			name := strings.TrimSpace(stringValue(tool["name"]))
			if name == "" {
				return nil, nil, &AdapterError{Code: "missing_required_parameter", Param: fmt.Sprintf("tools[%d].name", index), Message: "function tool name is required"}
			}
			declaration := map[string]any{"name": name}
			for _, field := range []string{"description", "parameters"} {
				if value, ok := tool[field]; ok {
					declaration[field] = value
				}
			}
			declarations = append(declarations, declaration)
		}
		payload["tools"] = []any{map[string]any{"functionDeclarations": declarations}}
	}
	if request.ToolChoice != nil {
		mode := strings.ToUpper(stringValue(request.ToolChoice))
		if mode == "REQUIRED" {
			mode = "ANY"
		}
		if mode == "AUTO" || mode == "ANY" || mode == "NONE" {
			payload["toolConfig"] = map[string]any{"functionCallingConfig": map[string]any{"mode": mode}}
		} else if choice, ok := request.ToolChoice.(map[string]any); ok {
			name := stringValue(choice["name"])
			payload["toolConfig"] = map[string]any{"functionCallingConfig": map[string]any{"mode": "ANY", "allowedFunctionNames": []any{name}}}
		}
	}
	generation := map[string]any{}
	if request.MaxOutputTokens != nil {
		generation["maxOutputTokens"] = request.MaxOutputTokens
	}
	if request.Temperature != nil {
		generation["temperature"] = request.Temperature
	}
	if request.TopP != nil {
		generation["topP"] = request.TopP
	}
	if effort := strings.TrimSpace(stringValue(request.Reasoning["effort"])); effort != "" {
		generation["thinkingConfig"] = map[string]any{"thinkingLevel": strings.ToUpper(effort)}
	}
	if format, ok := request.Text["format"].(map[string]any); ok {
		switch stringValue(format["type"]) {
		case "json_object":
			generation["responseMimeType"] = "application/json"
		case "json_schema":
			generation["responseMimeType"] = "application/json"
			if schema, ok := format["schema"].(map[string]any); ok {
				generation["responseSchema"] = schema
			}
		}
	}
	if len(generation) > 0 {
		payload["generationConfig"] = generation
	}
	if request.ServiceTier != nil {
		payload["serviceTier"] = request.ServiceTier
	}
	transform.MarkProtocol(payload, "openai")
	return payload, inputItems, nil
}

func NormalizeInput(input any) ([]any, error) {
	switch value := input.(type) {
	case string:
		return []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": value}}}}, nil
	case []any:
		return value, nil
	case map[string]any:
		return []any{value}, nil
	case nil:
		return nil, &AdapterError{Code: "missing_required_parameter", Param: "input", Message: "input is required"}
	default:
		return nil, &AdapterError{Code: "invalid_parameter", Param: "input", Message: "input must be a string, item, or item array"}
	}
}

func itemsToContents(items []any) ([]any, error) {
	contents := make([]any, 0, len(items))
	callNames := map[string]string{}
	for index, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok {
			return nil, &AdapterError{Code: "invalid_item", Param: fmt.Sprintf("input[%d]", index), Message: "input item must be an object"}
		}
		itemType := strings.ToLower(stringValue(item["type"]))
		if itemType == "" {
			itemType = "message"
		}
		switch itemType {
		case "message":
			role := strings.ToLower(stringValue(item["role"]))
			parts := normalizeTextContent(item["content"], "input_text")
			if len(parts) == 0 {
				continue
			}
			if role == "assistant" {
				role = "model"
			}
			if role == "system" || role == "developer" {
				role = "user"
			}
			contents = append(contents, map[string]any{"role": role, "parts": parts})
		case "function_call":
			callID := firstString(item["call_id"], item["id"])
			name := strings.TrimSpace(stringValue(item["name"]))
			if callID == "" || name == "" {
				return nil, &AdapterError{Code: "tool_call_identity_missing", Param: fmt.Sprintf("input[%d]", index), Message: "function_call requires call_id and name"}
			}
			arguments := normalizeArguments(item["arguments"])
			callNames[callID] = name
			contents = append(contents, map[string]any{"role": "model", "parts": []any{map[string]any{"functionCall": map[string]any{"id": callID, "name": name, "args": arguments}}}})
		case "function_call_output":
			callID := firstString(item["call_id"], item["id"])
			name := callNames[callID]
			if callID == "" || name == "" {
				return nil, &AdapterError{Code: "tool_state_missing", Param: fmt.Sprintf("input[%d].call_id", index), Message: "function call output cannot be restored from local state"}
			}
			contents = append(contents, map[string]any{"role": "function", "parts": []any{map[string]any{"functionResponse": map[string]any{
				"id": callID, "name": name, "response": normalizeOutput(item["output"]),
			}}}})
		default:
			return nil, &AdapterError{Code: "unsupported_input_item", Param: fmt.Sprintf("input[%d].type", index), Message: "input item type is not supported by the anonymous model boundary"}
		}
	}
	return mergeFunctionTurns(contents), nil
}

func OutputItems(response map[string]any) ([]any, map[string]any, string) {
	items := make([]any, 0)
	status := "completed"
	candidates, _ := response["candidates"].([]any)
	for _, rawCandidate := range candidates {
		candidate, _ := rawCandidate.(map[string]any)
		content, _ := candidate["content"].(map[string]any)
		parts, _ := content["parts"].([]any)
		var text strings.Builder
		for _, rawPart := range parts {
			part, _ := rawPart.(map[string]any)
			if value := stringValue(part["text"]); value != "" && !truthy(part["thought"]) {
				text.WriteString(value)
			}
			if call, ok := part["functionCall"].(map[string]any); ok {
				arguments, _ := json.Marshal(call["args"])
				items = append(items, map[string]any{
					"id": "fc_" + firstString(call["id"]), "type": "function_call", "status": "completed",
					"call_id": firstString(call["id"]), "name": stringValue(call["name"]), "arguments": string(arguments),
				})
			}
		}
		if text.Len() > 0 {
			items = append(items, map[string]any{
				"id": "msg_" + strutil.ReqID(), "type": "message", "status": "completed", "role": "assistant",
				"content": []any{map[string]any{"type": "output_text", "text": text.String(), "annotations": []any{}}},
			})
		}
		if finish := strings.ToUpper(stringValue(candidate["finishReason"])); finish != "" && finish != "STOP" && finish != "FINISH_REASON_UNSPECIFIED" {
			status = "incomplete"
		}
	}
	usage := map[string]any{}
	if metadata, ok := response["usageMetadata"].(map[string]any); ok {
		converted := transform.ConvertUsage(metadata)
		usage = map[string]any{
			"input_tokens": converted["prompt_tokens"], "output_tokens": converted["completion_tokens"], "total_tokens": converted["total_tokens"],
		}
	}
	return items, usage, status
}

func normalizeTextContent(raw any, expectedType string) []any {
	switch value := raw.(type) {
	case string:
		return []any{map[string]any{"text": value}}
	case []any:
		parts := make([]any, 0, len(value))
		for _, rawPart := range value {
			part, _ := rawPart.(map[string]any)
			partType := strings.ToLower(stringValue(part["type"]))
			switch {
			case partType == "text" || partType == "input_text" || partType == "output_text" || partType == expectedType:
				parts = append(parts, map[string]any{"text": stringValue(part["text"])})
			case partType == "input_image" || partType == "image_url":
				if uri := firstString(part["file_id"], part["image_url"], part["url"]); uri != "" {
					parts = append(parts, map[string]any{"fileData": map[string]any{"fileUri": uri, "mimeType": stringValue(part["mime_type"])}})
				}
			case partType == "input_file":
				if uri := firstString(part["file_id"], part["file_url"]); uri != "" {
					parts = append(parts, map[string]any{"fileData": map[string]any{"fileUri": uri, "mimeType": stringValue(part["mime_type"])}})
				}
			case partType == "input_audio":
				if data := stringValue(part["data"]); data != "" {
					parts = append(parts, map[string]any{"inlineData": map[string]any{"data": data, "mimeType": firstString(part["mime_type"], "audio/wav")}})
				}
			}
		}
		return parts
	default:
		return nil
	}
}

func normalizeArguments(raw any) any {
	if text, ok := raw.(string); ok {
		var value any
		if json.Unmarshal([]byte(text), &value) == nil {
			return value
		}
		return map[string]any{"raw": text}
	}
	if raw == nil {
		return map[string]any{}
	}
	return raw
}

func normalizeOutput(raw any) map[string]any {
	if text, ok := raw.(string); ok {
		var value map[string]any
		if json.Unmarshal([]byte(text), &value) == nil {
			return value
		}
		return map[string]any{"result": text}
	}
	if value, ok := raw.(map[string]any); ok {
		return value
	}
	return map[string]any{"result": raw}
}

func mergeFunctionTurns(contents []any) []any {
	result := make([]any, 0, len(contents))
	for _, rawContent := range contents {
		content, _ := rawContent.(map[string]any)
		if len(result) > 0 && content["role"] == "function" {
			last, _ := result[len(result)-1].(map[string]any)
			if last["role"] == "function" {
				last["parts"] = append(last["parts"].([]any), content["parts"].([]any)...)
				continue
			}
		}
		result = append(result, content)
	}
	return result
}

func firstString(values ...any) string {
	for _, value := range values {
		if text := strings.TrimSpace(stringValue(value)); text != "" {
			return text
		}
	}
	return ""
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func truthy(value any) bool {
	result, _ := value.(bool)
	return result
}

var ErrStateNotAvailable = errors.New("state not available")
