package interactions

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	responseadapter "github.com/bsfdsagfadg/vertex/internal/responses"
	"github.com/bsfdsagfadg/vertex/internal/strutil"
)

type CreateRequest struct {
	Model                 string                     `json:"model,omitempty"`
	Agent                 string                     `json:"agent,omitempty"`
	Input                 any                        `json:"input"`
	SystemInstruction     any                        `json:"system_instruction,omitempty"`
	Tools                 []any                      `json:"tools,omitempty"`
	ToolChoice            any                        `json:"tool_choice,omitempty"`
	ResponseFormat        map[string]any             `json:"response_format,omitempty"`
	Stream                bool                       `json:"stream,omitempty"`
	Store                 *bool                      `json:"store,omitempty"`
	Background            bool                       `json:"background,omitempty"`
	GenerationConfig      map[string]any             `json:"generation_config,omitempty"`
	PreviousInteractionID string                     `json:"previous_interaction_id,omitempty"`
	ResponseModalities    []any                      `json:"response_modalities,omitempty"`
	SafetySettings        []any                      `json:"safety_settings,omitempty"`
	ServiceTier           any                        `json:"service_tier,omitempty"`
	Environment           any                        `json:"environment,omitempty"`
	Labels                map[string]any             `json:"labels,omitempty"`
	RawExtensions         map[string]json.RawMessage `json:"-"`
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
	for _, key := range interactionRequestFields {
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

var interactionRequestFields = []string{ //nolint:gochecknoglobals
	"model", "agent", "input", "system_instruction", "tools", "tool_choice", "response_format", "stream", "store",
	"background", "generation_config", "previous_interaction_id", "response_modalities", "safety_settings", "service_tier",
	"environment", "labels",
}

func (r CreateRequest) UnknownFields() []string {
	fields := make([]string, 0, len(r.RawExtensions))
	for key := range r.RawExtensions {
		fields = append(fields, key)
	}
	sort.Strings(fields)
	return fields
}

type AdapterError = responseadapter.AdapterError

func BuildGemini(request CreateRequest, history []any) (map[string]any, []any, error) {
	if strings.TrimSpace(request.Agent) != "" {
		return nil, nil, &AdapterError{Code: "unsupported_agent", Param: "agent", Message: "managed agents are not available through the anonymous upstream"}
	}
	if request.Environment != nil {
		return nil, nil, &AdapterError{Code: "unsupported_environment", Param: "environment", Message: "remote managed environments are not supported"}
	}
	responseRequest := responseadapter.CreateRequest{
		Model: request.Model, Input: request.Input, Instructions: request.SystemInstruction,
		Tools: request.Tools, ToolChoice: request.ToolChoice, Stream: request.Stream,
		Store: request.Store, Background: request.Background, ServiceTier: request.ServiceTier,
	}
	if maxTokens, ok := request.GenerationConfig["max_output_tokens"]; ok {
		responseRequest.MaxOutputTokens = maxTokens
	}
	if temperature, ok := request.GenerationConfig["temperature"]; ok {
		responseRequest.Temperature = temperature
	}
	if topP, ok := request.GenerationConfig["top_p"]; ok {
		responseRequest.TopP = topP
	}
	if thinkingLevel, ok := request.GenerationConfig["thinking_level"].(string); ok {
		responseRequest.Reasoning = map[string]any{"effort": thinkingLevel}
	}
	if len(request.ResponseFormat) > 0 {
		responseRequest.Text = map[string]any{"format": request.ResponseFormat}
	}
	payload, inputItems, err := responseadapter.BuildGemini(responseRequest, history)
	if err != nil {
		return nil, nil, err
	}
	if len(request.ResponseModalities) > 0 {
		generation, _ := payload["generationConfig"].(map[string]any)
		if generation == nil {
			generation = map[string]any{}
			payload["generationConfig"] = generation
		}
		generation["responseModalities"] = request.ResponseModalities
	}
	if len(request.SafetySettings) > 0 {
		payload["safetySettings"] = request.SafetySettings
	}
	return payload, inputItems, nil
}

func Steps(response map[string]any) ([]any, map[string]any, string) {
	items, usage, status := responseadapter.OutputItems(response)
	steps := make([]any, 0, len(items))
	for _, rawItem := range items {
		item, _ := rawItem.(map[string]any)
		switch item["type"] {
		case "message":
			content := make([]any, 0)
			for _, rawContent := range anySlice(item["content"]) {
				part, _ := rawContent.(map[string]any)
				if text, _ := part["text"].(string); text != "" {
					content = append(content, map[string]any{"type": "text", "text": text})
				}
			}
			steps = append(steps, map[string]any{"id": "step_" + strutil.ReqID(), "type": "model_output", "content": content})
		case "function_call":
			steps = append(steps, map[string]any{
				"id": "step_" + strutil.ReqID(), "type": "function_call", "call_id": item["call_id"],
				"name": item["name"], "arguments": item["arguments"],
			})
		default:
			steps = append(steps, map[string]any{"id": "step_" + strutil.ReqID(), "type": fmt.Sprint(item["type"]), "data": item})
		}
	}
	return steps, usage, status
}

func HistoryItems(stepsJSON []byte) ([]any, error) {
	var steps []any
	if err := json.Unmarshal(stepsJSON, &steps); err != nil {
		return nil, fmt.Errorf("decode stored interaction steps: %w", err)
	}
	items := make([]any, 0, len(steps))
	for _, rawStep := range steps {
		step, _ := rawStep.(map[string]any)
		switch step["type"] {
		case "user_input":
			items = append(items, map[string]any{"type": "message", "role": "user", "content": step["content"]})
		case "model_output":
			items = append(items, map[string]any{"type": "message", "role": "assistant", "content": step["content"]})
		case "function_call":
			items = append(items, map[string]any{"type": "function_call", "call_id": step["call_id"], "name": step["name"], "arguments": step["arguments"]})
		case "function_call_output":
			items = append(items, map[string]any{"type": "function_call_output", "call_id": step["call_id"], "output": step["output"]})
		}
	}
	return items, nil
}

func anySlice(value any) []any {
	items, _ := value.([]any)
	return items
}
