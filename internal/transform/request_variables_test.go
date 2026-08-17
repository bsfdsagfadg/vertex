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

func (d *dummyConfig) SafetySettings() map[string]string {
	return map[string]string{}
}

func (d *dummyConfig) DropMaxTokens() bool { return false }

func (d *dummyConfig) TrailingModelFixEnabled() bool {
	return d.trailingFixEnabled
}

func (d *dummyConfig) TrailingFixModels() []string {
	return d.trailingFixModels
}

func (d *dummyConfig) ResolveModelName(m string) string {
	return m
}

func TestBuildGeminiVariablesTyped_Basic(t *testing.T) {
	req := &GeminiRequest{
		Contents: []Content{
			{Role: "user", Parts: []Part{{Text: "hello"}}},
		},
	}
	cfg := &dummyConfig{trailingFixEnabled: false}

	vars := BuildGeminiVariablesTyped("gemini-1.5-flash", req, cfg)
	if vars == nil {
		t.Fatal("vars should not be nil")
	}
	if vars.Model != "gemini-1.5-flash" {
		t.Errorf("model=%v, want 'gemini-1.5-flash'", vars.Model)
	}
	if vars.GeminiRequest == nil {
		t.Fatal("embedded GeminiRequest should not be nil")
	}
	if len(vars.Contents) != 1 || vars.Contents[0].Role != "user" {
		t.Fatalf("invalid contents in typed vars: %v", vars.Contents)
	}
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

func TestBuildGeminiVariables_ToolResponseIsolation(t *testing.T) {
	geminiReq := &GeminiRequest{
		Contents: []Content{
			{Role: "user", Parts: []Part{{Text: "Find files"}}},
			{Role: "model", Parts: []Part{{FunctionCall: &FunctionCall{Name: "glob", Args: map[string]any{"pattern": "*.go"}}}}},
			{Role: "user", Parts: []Part{{FunctionResponse: &FunctionResponse{Name: "glob", Response: map[string]any{"result": `["main.go"]`}}}}},
			{Role: "user", Parts: []Part{{Text: "Analyze main.go"}}},
		},
	}

	vars := BuildGeminiVariables("gemini-3.6-flash", geminiReq, nil)
	contents, ok := vars["contents"].([]any)
	if !ok {
		t.Fatalf("contents in vars is not []any")
	}

	// 验证 4 个独立的 content 节点 (user -> model -> user(functionResponse) -> user(follow-up text))
	if len(contents) != 4 {
		t.Fatalf("len(contents)=%d, want 4 (functionResponse must NOT be merged with follow-up user text)", len(contents))
	}

	// 节点 2: tool response Content
	toolContent, ok := contents[2].(map[string]any)
	if !ok || toolContent["role"] != "user" {
		t.Fatalf("content[2] role=%v, want 'user'", toolContent["role"])
	}
	toolParts, ok := toolContent["parts"].([]any)
	if !ok || len(toolParts) != 1 {
		t.Fatalf("len(toolParts)=%d, want 1", len(toolParts))
	}
	pTool, ok := toolParts[0].(map[string]any)
	if !ok {
		t.Fatalf("pTool not a map")
	}
	fr, ok := pTool["functionResponse"].(map[string]any)
	if !ok {
		t.Fatalf("missing functionResponse in pTool: %v", pTool)
	}
	if fr["name"] != "glob" {
		t.Errorf("functionResponse.name=%v, want 'glob'", fr["name"])
	}
	respMap, ok := fr["response"].(map[string]any)
	if !ok {
		t.Fatalf("functionResponse.response must be a map, got %T (%v)", fr["response"], fr["response"])
	}
	if _, hasResult := respMap["result"]; !hasResult && len(respMap) == 0 {
		t.Errorf("functionResponse.response map is empty")
	}

	// 节点 3: follow-up user Content
	userContent, ok := contents[3].(map[string]any)
	if !ok || userContent["role"] != "user" {
		t.Fatalf("content[3] role=%v, want 'user'", userContent["role"])
	}
	userParts, ok := userContent["parts"].([]any)
	if !ok || len(userParts) != 1 {
		t.Fatalf("len(userParts)=%d, want 1", len(userParts))
	}
	pUser, ok := userParts[0].(map[string]any)
	if !ok || pUser["text"] != "Analyze main.go" {
		t.Errorf("content[3] part text=%v, want 'Analyze main.go'", pUser["text"])
	}
}

func TestBuildGeminiVariables_MultipleParallelToolResponsesMerged(t *testing.T) {
	geminiReq := &GeminiRequest{
		Contents: []Content{
			{Role: "user", Parts: []Part{{Text: "Find and read files"}}},
			{Role: "model", Parts: []Part{
				{FunctionCall: &FunctionCall{Name: "glob", Args: map[string]any{"pattern": "*.go"}}},
				{FunctionCall: &FunctionCall{Name: "read", Args: map[string]any{"path": "main.go"}}},
			}},
			{Role: "user", Parts: []Part{
				{FunctionResponse: &FunctionResponse{Name: "glob", Response: map[string]any{"result": `["main.go"]`}}},
				{FunctionResponse: &FunctionResponse{Name: "read", Response: map[string]any{"result": "package main"}}},
			}},
			{Role: "user", Parts: []Part{{Text: "Analyze the content"}}},
		},
	}

	vars := BuildGeminiVariables("gemini-3.6-flash", geminiReq, nil)
	contents, ok := vars["contents"].([]any)
	if !ok {
		t.Fatalf("contents in vars is not []any")
	}

	// 期望 4 个 Content 节点：
	// 0: user ("Find and read files")
	// 1: model (2 functionCalls)
	// 2: user (2 functionResponses 合并到同一个 Content 中！)
	// 3: user ("Analyze the content" 隔离保持独立)
	if len(contents) != 4 {
		t.Fatalf("len(contents)=%d, want 4 (multiple consecutive functionResponses MUST be merged into 1 Content)", len(contents))
	}

	toolContent, ok := contents[2].(map[string]any)
	if !ok || toolContent["role"] != "user" {
		t.Fatalf("content[2] role=%v, want 'user'", toolContent["role"])
	}
	toolParts, ok := toolContent["parts"].([]any)
	if !ok || len(toolParts) != 2 {
		t.Fatalf("len(toolParts)=%d, want 2 (both tool responses merged into 1 turn)", len(toolParts))
	}

	// 校验第 1 个 part 是 glob
	p0, ok := toolParts[0].(map[string]any)
	if !ok {
		t.Fatalf("toolParts[0] not a map")
	}
	fr0, ok := p0["functionResponse"].(map[string]any)
	if !ok || fr0["name"] != "glob" {
		t.Errorf("fr0 name=%v, want 'glob'", fr0["name"])
	}

	// 校验第 2 个 part 是 read
	p1, ok := toolParts[1].(map[string]any)
	if !ok {
		t.Fatalf("toolParts[1] not a map")
	}
	fr1, ok := p1["functionResponse"].(map[string]any)
	if !ok || fr1["name"] != "read" {
		t.Errorf("fr1 name=%v, want 'read'", fr1["name"])
	}

	// 校验 follow-up user text 节点 3 独立存在
	userContent, ok := contents[3].(map[string]any)
	if !ok || userContent["role"] != "user" {
		t.Fatalf("content[3] role=%v, want 'user'", userContent["role"])
	}
	userParts, ok := userContent["parts"].([]any)
	if !ok || len(userParts) != 1 {
		t.Fatalf("len(userParts)=%d, want 1", len(userParts))
	}
	pUser, ok := userParts[0].(map[string]any)
	if !ok || pUser["text"] != "Analyze the content" {
		t.Errorf("content[3] part text=%v, want 'Analyze the content'", pUser["text"])
	}
}

func TestBuildGeminiVariablesTyped_DefaultSafetySettings(t *testing.T) {
	req := &GeminiRequest{
		Contents: []Content{
			{Role: "user", Parts: []Part{{Text: "Draw a picture"}}},
		},
	}
	cfg := &dummyConfig{}

	t.Run("image family outputs fixed 4xOFF safety settings", func(t *testing.T) {
		vars := BuildGeminiVariablesTyped("gemini-3.1-flash-image", req, cfg)
		if vars == nil || vars.GeminiRequest == nil {
			t.Fatal("vars or GeminiRequest is nil")
		}
		settings := vars.SafetySettings
		if len(settings) != 4 {
			t.Fatalf("expected 4 fixed safety settings, got %d: %v", len(settings), settings)
		}
		for _, s := range settings {
			if s.Threshold != "OFF" {
				t.Errorf("expected OFF threshold, got %s", s.Threshold)
			}
		}
	})

	t.Run("text family outputs fixed 4xOFF without JAILBREAK / CIVIC_INTEGRITY", func(t *testing.T) {
		vars := BuildGeminiVariablesTyped("gemini-2.5-flash", req, cfg)
		if vars == nil || vars.GeminiRequest == nil {
			t.Fatal("vars or GeminiRequest is nil")
		}
		settings := vars.SafetySettings
		if len(settings) != 4 {
			t.Fatalf("expected 4 fixed safety settings, got %d: %v", len(settings), settings)
		}
		for _, s := range settings {
			if s.Category == "HARM_CATEGORY_JAILBREAK" || s.Category == "HARM_CATEGORY_CIVIC_INTEGRITY" {
				t.Errorf("unexpected purged category %s in fixed list", s.Category)
			}
			if s.Threshold != "OFF" {
				t.Errorf("expected OFF threshold, got %s", s.Threshold)
			}
		}
	})

	t.Run("client-provided safety settings are ignored", func(t *testing.T) {
		clientReq := &GeminiRequest{
			Contents: []Content{
				{Role: "user", Parts: []Part{{Text: "Draw a picture"}}},
			},
			SafetySettings: []SafetySetting{
				{Category: "HARM_CATEGORY_JAILBREAK", Threshold: "BLOCK_NONE"},
				{Category: "CUSTOM", Threshold: "BLOCK_LOW_AND_ABOVE"},
			},
		}
		vars := BuildGeminiVariablesTyped("gemini-2.5-flash", clientReq, cfg)
		settings := vars.SafetySettings
		if len(settings) != 4 {
			t.Fatalf("expected 4 fixed safety settings ignoring client input, got %d: %v", len(settings), settings)
		}
	})
}

func TestSanitizeContentRolesTyped(t *testing.T) {
	cases := []struct {
		name     string
		input    []Content
		expected []string // 期望的各 content role
	}{
		{
			name:     "empty string defaults to user",
			input:    []Content{{Role: "", Parts: []Part{{Text: "hi"}}}},
			expected: []string{"user"},
		},
		{
			name:     "whitespace-only defaults to user",
			input:    []Content{{Role: "   ", Parts: []Part{{Text: "hi"}}}},
			expected: []string{"user"},
		},
		{
			name: "valid roles preserved",
			input: []Content{
				{Role: "user", Parts: []Part{{Text: "q"}}},
				{Role: "model", Parts: []Part{{Text: "a"}}},
			},
			expected: []string{"user", "model"},
		},
		{
			name: "mixed empty and valid",
			input: []Content{
				{Role: "", Parts: []Part{{Text: "turn1"}}},
				{Role: "model", Parts: []Part{{Text: "resp"}}},
				{Role: "  ", Parts: []Part{{Text: "turn2"}}},
			},
			expected: []string{"user", "model", "user"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeContentRolesTyped(tc.input)
			for i, want := range tc.expected {
				if got[i].Role != want {
					t.Errorf("content[%d] role = %q, want %q", i, got[i].Role, want)
				}
			}
		})
	}
}

func TestBuildGeminiVariablesTyped_EmptyRoleSanitized(t *testing.T) {
	// 场景：Gemini 原生 REST 客户端省略 role 字段
	req := &GeminiRequest{
		Contents: []Content{
			{Role: "", Parts: []Part{{Text: "Hello TTS"}}},
		},
		GenerationConfig: &GenerationConfig{
			ResponseModalities: []string{"AUDIO"},
		},
	}
	vars := BuildGeminiVariablesTyped("gemini-3.1-flash-tts-preview", req, nil)

	if len(vars.Contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(vars.Contents))
	}
	if vars.Contents[0].Role != "user" {
		t.Errorf("empty role should default to 'user', got %q", vars.Contents[0].Role)
	}
}

func TestBuildGeminiVariablesTyped_WhitespaceRoleSanitized(t *testing.T) {
	// 场景：role 为纯空白
	req := &GeminiRequest{
		Contents: []Content{
			{Role: "   ", Parts: []Part{{Text: "hi"}}},
		},
	}
	vars := BuildGeminiVariablesTyped("gemini-pro", req, nil)

	if vars.Contents[0].Role != "user" {
		t.Errorf("whitespace-only role should default to 'user', got %q", vars.Contents[0].Role)
	}
}

func TestBuildGeminiVariablesTyped_ValidRolePreserved(t *testing.T) {
	// 场景：合法 role 不被改动
	req := &GeminiRequest{
		Contents: []Content{
			{Role: "user", Parts: []Part{{Text: "q"}}},
			{Role: "model", Parts: []Part{{Text: "a"}}},
		},
	}
	vars := BuildGeminiVariablesTyped("gemini-pro", req, nil)

	if vars.Contents[0].Role != "user" {
		t.Errorf("user role should be preserved, got %q", vars.Contents[0].Role)
	}
	if vars.Contents[1].Role != "model" {
		t.Errorf("model role should be preserved, got %q", vars.Contents[1].Role)
	}
}

func TestBuildGeminiVariablesTyped_EmptyRoleMergeOrder(t *testing.T) {
	// 场景：两条空 Role 的相邻 content。
	// 修复前：sanitize 未执行时 "" == "" → merge 误判为同角色合并，且 role 仍为空 → 上游 400。
	// 修复后：sanitize 先将两条均兜底为 "user"，merge 正确合并为 1 条，role 合法。
	req := &GeminiRequest{
		Contents: []Content{
			{Role: "", Parts: []Part{{Text: "turn1"}}},
			{Role: "", Parts: []Part{{Text: "turn2"}}},
		},
	}
	vars := BuildGeminiVariablesTyped("gemini-pro", req, nil)

	// 关键验证点：输出中所有 content 的 Role 不再为空字符串
	for i, c := range vars.Contents {
		if c.Role != "user" {
			t.Errorf("content[%d] role should be 'user', got %q", i, c.Role)
		}
	}
}

func TestBuildGeminiVariables_Defaults(t *testing.T) {
	req := &GeminiRequest{
		Contents: []Content{{Role: "user", Parts: []Part{{Text: "hello"}}}},
	}
	vars := BuildGeminiVariables("gemini-3.6-flash", req, nil)
	modelVal, _ := vars["model"].(string)
	if modelVal != "gemini-3.6-flash" {
		t.Errorf("Model=%q, want 'gemini-3.6-flash'", modelVal)
	}
}
