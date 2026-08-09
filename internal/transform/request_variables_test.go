package transform

import (
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

type dummyConfig struct {
	config.ConfigProvider
	trailingFixEnabled bool
	trailingFixModels  []string
}

func (d *dummyConfig) TrailingModelFixEnabled() bool {
	return d.trailingFixEnabled
}

func (d *dummyConfig) TrailingFixModels() []string {
	return d.trailingFixModels
}

func (d *dummyConfig) ResolveModelName(m string) string {
	return m
}

func TestBuildGeminiVariables_Basic(t *testing.T) {
	req := &GeminiRequest{
		Contents: []Content{
			{Role: "user", Parts: []Part{{Text: "hello"}}},
		},
	}
	cfg := &dummyConfig{trailingFixEnabled: false}

	vars := BuildGeminiVariables("gemini-1.5-flash", req, cfg)
	if vars["model"] != "gemini-1.5-flash" {
		t.Errorf("model=%v, want 'gemini-1.5-flash'", vars["model"])
	}

	contents, ok := vars["contents"].([]any)
	if !ok || len(contents) != 1 {
		t.Fatalf("invalid contents in vars: %v", vars["contents"])
	}
}

func TestBuildGeminiVariables_MergeContiguousRoles(t *testing.T) {
	req := &GeminiRequest{
		Contents: []Content{
			{Role: "user", Parts: []Part{{Text: "part1"}}},
			{Role: "user", Parts: []Part{{Text: "part2"}}},
			{Role: "model", Parts: []Part{{Text: "resp"}}},
		},
	}
	vars := BuildGeminiVariables("gemini-pro", req, nil)
	contents, ok := vars["contents"].([]any)
	if !ok || len(contents) != 2 {
		t.Fatalf("len(contents)=%d, want 2", len(contents))
	}
}

func TestBuildGeminiVariables_FilterEmptyPartsAndContents(t *testing.T) {
	req := &GeminiRequest{
		Contents: []Content{
			{Role: "user", Parts: []Part{{Text: ""}, {Text: "valid"}}},
			{Role: "model", Parts: []Part{{Text: ""}}},
		},
	}
	vars := BuildGeminiVariables("gemini-pro", req, nil)
	contents, ok := vars["contents"].([]any)
	if !ok || len(contents) != 1 {
		t.Fatalf("len(contents)=%d, want 1", len(contents))
	}
}

func TestBuildGeminiVariables_SystemInstructionFallback(t *testing.T) {
	req := &GeminiRequest{
		SystemInstruction: &Content{Role: "system", Parts: []Part{{Text: "you are helpful"}}},
		Contents: []Content{
			{Role: "model", Parts: []Part{{Text: "prior model output"}}},
		},
	}
	vars := BuildGeminiVariables("gemini-pro", req, nil)
	contents, ok := vars["contents"].([]any)
	if !ok || len(contents) != 2 {
		t.Fatalf("len(contents)=%d, want 2", len(contents))
	}
	first := contents[0].(map[string]any)
	if first["role"] != "user" {
		t.Errorf("first content role=%v, want 'user'", first["role"])
	}
}

func TestBuildGeminiVariables_TrailingModelFix(t *testing.T) {
	req := &GeminiRequest{
		Contents: []Content{
			{Role: "user", Parts: []Part{{Text: "hi"}}},
			{Role: "model", Parts: []Part{{Text: "hello"}}},
		},
	}
	cfg := &dummyConfig{
		trailingFixEnabled: true,
		trailingFixModels:  []string{"gemini-2.0-flash"},
	}

	vars := BuildGeminiVariables("gemini-2.0-flash", req, cfg)
	contents, ok := vars["contents"].([]any)
	if !ok || len(contents) != 3 {
		t.Fatalf("len(contents)=%d, want 3", len(contents))
	}

	last := contents[len(contents)-1].(map[string]any)
	if last["role"] != "user" {
		t.Errorf("last role=%v, want 'user'", last["role"])
	}
	parts := last["parts"].([]any)
	p0 := parts[0].(map[string]any)
	if p0["text"] != "继续" {
		t.Errorf("p0 text=%v, want '继续'", p0["text"])
	}
}

func TestBuildGeminiVariables_ToolsNativeSchema(t *testing.T) {
	req := &GeminiRequest{
		Contents: []Content{
			{Role: "user", Parts: []Part{{Text: "read file"}}},
		},
		Tools: []Tool{
			{
				FunctionDeclarations: []FunctionDeclaration{
					{
						Name:        "fs_read",
						Description: "Read file content",
						Parameters: map[string]any{
							"type": "object",
							"properties": map[string]any{
								"path": map[string]any{
									"type":        "string",
									"description": "File path",
								},
							},
							"required": []any{"path"},
						},
					},
				},
			},
		},
	}

	vars := BuildGeminiVariables("gemini-3.6-flash", req, nil)
	tools, ok := vars["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected 1 tool in vars, got %v", vars["tools"])
	}

	toolMap, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("tool not a map: %v", tools[0])
	}

	decls, ok := toolMap["functionDeclarations"].([]any)
	if !ok || len(decls) != 1 {
		t.Fatalf("expected 1 functionDeclaration, got %v", toolMap["functionDeclarations"])
	}

	declMap, ok := decls[0].(map[string]any)
	if !ok {
		t.Fatalf("decl not a map: %v", decls[0])
	}

	paramsMap, ok := declMap["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("parameters not a map: %v", declMap["parameters"])
	}

	// Verify uppercase enum for UI type
	if paramsMap["type"] != "OBJECT" {
		t.Errorf("parameters.type=%v, want 'OBJECT'", paramsMap["type"])
	}

	// Verify properties array format
	props, ok := paramsMap["properties"].([]any)
	if !ok || len(props) != 1 {
		t.Fatalf("expected properties to be []any slice of length 1, got %T: %v", paramsMap["properties"], paramsMap["properties"])
	}

	prop0, ok := props[0].(map[string]any)
	if !ok {
		t.Fatalf("prop0 not a map: %v", props[0])
	}

	if prop0["key"] != "path" {
		t.Errorf("prop0.key=%v, want 'path'", prop0["key"])
	}

	valMap, ok := prop0["value"].(map[string]any)
	if !ok || valMap["type"] != "STRING" {
		t.Errorf("prop0.value.type=%v, want 'STRING'", valMap["type"])
	}
}

func TestBuildGeminiVariables_HistoryThoughtSignatureInjection(t *testing.T) {
	req := &GeminiRequest{
		Contents: []Content{
			{Role: "user", Parts: []Part{{Text: "Find files"}}},
			{
				Role: "model",
				Parts: []Part{
					{
						FunctionCall: &FunctionCall{
							Name: "glob",
							Args: map[string]any{"pattern": "*.go"},
						},
					},
				},
			},
			{
				Role: "user",
				Parts: []Part{
					{
						FunctionResponse: &FunctionResponse{
							Name:     "glob",
							Response: map[string]any{"result": []string{"main.go"}},
						},
					},
				},
			},
		},
	}

	vars := BuildGeminiVariables("gemini-3.6-flash", req, nil)
	contents, ok := vars["contents"].([]any)
	if !ok || len(contents) != 3 {
		t.Fatalf("expected 3 contents, got %v", len(contents))
	}

	modelTurn, ok := contents[1].(map[string]any)
	if !ok || modelTurn["role"] != "model" {
		t.Fatalf("expected model turn at index 1, got %v", contents[1])
	}

	parts, ok := modelTurn["parts"].([]any)
	if !ok || len(parts) != 1 {
		t.Fatalf("expected 1 part in model turn, got %v", modelTurn["parts"])
	}

	p0, ok := parts[0].(map[string]any)
	if !ok {
		t.Fatalf("p0 not a map: %v", parts[0])
	}

	sig, ok := p0["thoughtSignature"].(string)
	if !ok || sig == "" {
		t.Fatalf("thoughtSignature missing or empty in history model functionCall part: %v", p0)
	}

	// Verify the sentinel is base64 encoded and non-empty
	if len(sig) == 0 {
		t.Errorf("expected non-empty thoughtSignature, got %q", sig)
	}
}
