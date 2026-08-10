package transform

import (
	"encoding/json"
	"testing"
)

func TestConvertToolsTyped_WebSearch(t *testing.T) {
	// 测试场景 1：传入 {"type": "web_search"}
	bodyJSON := `{
		"model": "gemini-3.6-flash",
		"messages": [{"role": "user", "content": "hello"}],
		"tools": [
			{"type": "web_search"}
		]
	}`

	var req ChatCompletionRequest
	if err := json.Unmarshal([]byte(bodyJSON), &req); err != nil {
		t.Fatalf("failed to unmarshal request: %v", err)
	}

	geminiReq, _, err := ConvertChatRequestToGemini(&req, nil)
	if err != nil {
		t.Fatalf("ConvertChatRequestToGemini error: %v", err)
	}

	if len(geminiReq.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(geminiReq.Tools))
	}

	vars := BuildGeminiVariables("gemini-3.6-flash", geminiReq, nil)
	tools, ok := vars["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected 1 tool in vars, got %v", vars["tools"])
	}

	toolMap, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("tool is not a map: %v", tools[0])
	}

	if _, ok := toolMap["googleSearch"]; !ok {
		t.Errorf("expected googleSearch in toolMap, got %v", toolMap)
	}
}

func TestConvertToolsTyped_WebSearchWithFunction(t *testing.T) {
	// 测试场景 2：同时传入 function 声明与 web_search
	bodyJSON := `{
		"model": "gemini-3.6-flash",
		"messages": [{"role": "user", "content": "hello"}],
		"tools": [
			{"type": "web_search"},
			{
				"type": "function",
				"function": {
					"name": "get_weather",
					"description": "get weather",
					"parameters": {"type": "object", "properties": {}}
				}
			}
		]
	}`

	var req ChatCompletionRequest
	if err := json.Unmarshal([]byte(bodyJSON), &req); err != nil {
		t.Fatalf("failed to unmarshal request: %v", err)
	}

	geminiReq, _, err := ConvertChatRequestToGemini(&req, nil)
	if err != nil {
		t.Fatalf("ConvertChatRequestToGemini error: %v", err)
	}

	if len(geminiReq.Tools) != 1 {
		t.Fatalf("expected 1 combined tool, got %d", len(geminiReq.Tools))
	}

	vars := BuildGeminiVariables("gemini-3.6-flash", geminiReq, nil)
	tools, ok := vars["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected 1 tool in vars, got %v", vars["tools"])
	}

	toolMap, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("tool is not a map: %v", tools[0])
	}

	if _, ok := toolMap["googleSearch"]; !ok {
		t.Errorf("expected googleSearch in toolMap, got %v", toolMap)
	}
	if _, ok := toolMap["functionDeclarations"]; !ok {
		t.Errorf("expected functionDeclarations in toolMap, got %v", toolMap)
	}
}
