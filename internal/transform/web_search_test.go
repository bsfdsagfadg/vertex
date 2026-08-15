package transform

import (
	"testing"
)

func TestBuildGeminiVariables_WebSearch(t *testing.T) {
	// 测试场景 1：传入带有 GoogleSearch 的 Tool
	geminiReq := &GeminiRequest{
		Contents: []Content{{Role: "user", Parts: []Part{{Text: "hello"}}}},
		Tools: []Tool{
			{GoogleSearch: &GoogleSearch{}},
		},
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

func TestBuildGeminiVariables_WebSearchWithFunction(t *testing.T) {
	// 测试场景 2：同时传入 function 声明与 googleSearch
	geminiReq := &GeminiRequest{
		Contents: []Content{{Role: "user", Parts: []Part{{Text: "hello"}}}},
		Tools: []Tool{
			{
				GoogleSearch: &GoogleSearch{},
				FunctionDeclarations: []FunctionDeclaration{
					{
						Name:        "get_weather",
						Description: "get weather",
						Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
					},
				},
			},
		},
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
