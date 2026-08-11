package transform

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConvertChatRequestToGemini_ToolMessageDisambiguation(t *testing.T) {
	strPtr := func(s string) *string { return &s }

	t.Run("Standard tool_call_id resolution", func(t *testing.T) {
		req := &ChatCompletionRequest{
			Model: "gemini-3.6-flash",
			Messages: []Message{
				{Role: "user", Content: MessageContent{String: strPtr("Hello")}},
				{
					Role: "assistant",
					ToolCalls: []OAIToolCall{
						{
							ID:       "call_123",
							Type:     "function",
							Function: OAIToolCallFn{Name: "get_weather", Arguments: `{}`},
						},
					},
				},
				{
					Role:       "tool",
					ToolCallID: "call_123",
					Content:    MessageContent{String: strPtr(`{"temperature": 25}`)},
				},
			},
		}

		geminiReq, _, err := ConvertChatRequestToGemini(req, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		vars := BuildGeminiVariablesTyped("gemini-3.6-flash", geminiReq, nil)
		b, _ := json.Marshal(vars)
		var m map[string]any
		_ = json.Unmarshal(b, &m)

		contents := m["contents"].([]any)
		toolTurn := contents[2].(map[string]any)
		parts := toolTurn["parts"].([]any)
		fr := parts[0].(map[string]any)["functionResponse"].(map[string]any)

		if fr["name"] != "get_weather" {
			t.Errorf("expected functionResponse.name='get_weather', got %v", fr["name"])
		}
	})

	t.Run("Sequential recovery for single unconsumed tool call", func(t *testing.T) {
		req := &ChatCompletionRequest{
			Model: "gemini-3.6-flash",
			Messages: []Message{
				{Role: "user", Content: MessageContent{String: strPtr("Run tool")}},
				{
					Role: "assistant",
					ToolCalls: []OAIToolCall{
						{
							ID:       "call_solo",
							Type:     "function",
							Function: OAIToolCallFn{Name: "read_file", Arguments: `{}`},
						},
					},
				},
				{
					Role:    "tool",
					Content: MessageContent{String: strPtr(`{"content": "ok"}`)},
				},
			},
		}

		geminiReq, _, err := ConvertChatRequestToGemini(req, nil)
		if err != nil {
			t.Fatalf("expected single unconsumed tool call to resolve automatically, got err: %v", err)
		}

		vars := BuildGeminiVariablesTyped("gemini-3.6-flash", geminiReq, nil)
		b, _ := json.Marshal(vars)
		var m map[string]any
		_ = json.Unmarshal(b, &m)

		contents := m["contents"].([]any)
		toolTurn := contents[2].(map[string]any)
		parts := toolTurn["parts"].([]any)
		fr := parts[0].(map[string]any)["functionResponse"].(map[string]any)

		if fr["name"] != "read_file" {
			t.Errorf("expected resolved functionResponse.name='read_file', got %v", fr["name"])
		}
	})

	t.Run("Ambiguous unconsumed tool calls cause 400 error", func(t *testing.T) {
		req := &ChatCompletionRequest{
			Model: "gemini-3.6-flash",
			Messages: []Message{
				{Role: "user", Content: MessageContent{String: strPtr("Run tools")}},
				{
					Role: "assistant",
					ToolCalls: []OAIToolCall{
						{
							ID:       "call_1",
							Type:     "function",
							Function: OAIToolCallFn{Name: "fn_a", Arguments: `{}`},
						},
						{
							ID:       "call_2",
							Type:     "function",
							Function: OAIToolCallFn{Name: "fn_b", Arguments: `{}`},
						},
					},
				},
				{
					Role:    "tool",
					Content: MessageContent{String: strPtr(`{"result": 1}`)},
				},
			},
		}

		_, _, err := ConvertChatRequestToGemini(req, nil)
		if err == nil {
			t.Fatal("expected error when tool call is ambiguous, got nil")
		}
		if !strings.Contains(err.Error(), "ambiguous unconsumed tool calls") {
			t.Errorf("expected ambiguous unconsumed tool calls error, got %v", err)
		}
	})

	t.Run("Unmatched isolated tool message causes 400 error", func(t *testing.T) {
		req := &ChatCompletionRequest{
			Model: "gemini-3.6-flash",
			Messages: []Message{
				{Role: "user", Content: MessageContent{String: strPtr("Hello")}},
				{
					Role:       "tool",
					ToolCallID: "call_nonexistent",
					Content:    MessageContent{String: strPtr(`{"result": 1}`)},
				},
			},
		}

		_, _, err := ConvertChatRequestToGemini(req, nil)
		if err == nil {
			t.Fatal("expected error for isolated tool message without match, got nil")
		}
		if !strings.Contains(err.Error(), "unmatched tool_call_id") {
			t.Errorf("expected unmatched tool_call_id error, got %v", err)
		}
	})

	t.Run("Assistant message with empty tool call function name is recovered via argument schema inference", func(t *testing.T) {
		req := &ChatCompletionRequest{
			Model: "gemini-3.6-flash",
			Tools: []OAITool{
				{
					Type: "function",
					Function: OAIFunction{
						Name: "read",
						Parameters: map[string]any{
							"properties": map[string]any{
								"filePath": map[string]any{"type": "string"},
							},
						},
					},
				},
				{
					Type: "function",
					Function: OAIFunction{
						Name: "bash",
						Parameters: map[string]any{
							"properties": map[string]any{
								"command": map[string]any{"type": "string"},
							},
						},
					},
				},
			},
			Messages: []Message{
				{Role: "user", Content: MessageContent{String: strPtr("Read file")}},
				{
					Role: "assistant",
					ToolCalls: []OAIToolCall{
						{
							ID:       "call_5785b8b760fcce2f02d7b894",
							Type:     "function",
							Function: OAIToolCallFn{Name: "", Arguments: `{"filePath":"E:\\User\\policy.go","limit":100}`},
						},
					},
				},
				{
					Role:       "tool",
					ToolCallID: "call_5785b8b760fcce2f02d7b894",
					Content:    MessageContent{String: strPtr("file content")},
				},
			},
		}

		geminiReq, _, err := ConvertChatRequestToGemini(req, nil)
		if err != nil {
			t.Fatalf("expected successful inference and recovery, got err: %v", err)
		}

		vars := BuildGeminiVariablesTyped("gemini-3.6-flash", geminiReq, nil)
		b, _ := json.Marshal(vars)
		var m map[string]any
		_ = json.Unmarshal(b, &m)

		contents := m["contents"].([]any)
		modelTurn := contents[1].(map[string]any)
		modelParts := modelTurn["parts"].([]any)
		fc := modelParts[0].(map[string]any)["functionCall"].(map[string]any)
		if fc["name"] != "read" {
			t.Errorf("expected recovered assistant functionCall.name='read', got %v", fc["name"])
		}

		toolTurn := contents[2].(map[string]any)
		toolParts := toolTurn["parts"].([]any)
		fr := toolParts[0].(map[string]any)["functionResponse"].(map[string]any)
		if fr["name"] != "read" {
			t.Errorf("expected functionResponse.name='read', got %v", fr["name"])
		}
	})

	t.Run("Custom tool schema takes precedence over hardcoded rules", func(t *testing.T) {
		req := &ChatCompletionRequest{
			Model: "gemini-3.6-flash",
			Tools: []OAITool{
				{
					Type: "function",
					Function: OAIFunction{
						Name: "custom_reader",
						Parameters: map[string]any{
							"properties": map[string]any{
								"filePath": map[string]any{"type": "string"},
							},
						},
					},
				},
				{
					Type: "function",
					Function: OAIFunction{
						Name: "custom_exec",
						Parameters: map[string]any{
							"properties": map[string]any{
								"command": map[string]any{"type": "string"},
							},
						},
					},
				},
			},
			Messages: []Message{
				{Role: "user", Content: MessageContent{String: strPtr("Read custom file")}},
				{
					Role: "assistant",
					ToolCalls: []OAIToolCall{
						{
							ID:       "call_custom123",
							Type:     "function",
							Function: OAIToolCallFn{Name: "", Arguments: `{"filePath":"test.txt"}`},
						},
					},
				},
				{
					Role:       "tool",
					ToolCallID: "call_custom123",
					Content:    MessageContent{String: strPtr("content")},
				},
			},
		}

		geminiReq, _, err := ConvertChatRequestToGemini(req, nil)
		if err != nil {
			t.Fatalf("expected successful recovery for custom tool, got err: %v", err)
		}

		vars := BuildGeminiVariablesTyped("gemini-3.6-flash", geminiReq, nil)
		b, _ := json.Marshal(vars)
		var m map[string]any
		_ = json.Unmarshal(b, &m)

		contents := m["contents"].([]any)
		modelTurn := contents[1].(map[string]any)
		modelParts := modelTurn["parts"].([]any)
		fc := modelParts[0].(map[string]any)["functionCall"].(map[string]any)
		if fc["name"] != "custom_reader" {
			t.Errorf("expected custom tool name 'custom_reader', got %v", fc["name"])
		}
	})
}
