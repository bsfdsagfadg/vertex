package transform

import (
	"encoding/json"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/infra/config"
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

func TestBuildGeminiVariablesTyped_MergeContiguousRoles(t *testing.T) {
	req := &GeminiRequest{
		Contents: []Content{
			{Role: "user", Parts: []Part{{Text: "part1"}}},
			{Role: "user", Parts: []Part{{Text: "part2"}}},
			{Role: "model", Parts: []Part{{Text: "resp"}}},
		},
	}
	vars := BuildGeminiVariablesTyped("gemini-pro", req, nil)
	if len(vars.Contents) != 2 {
		t.Fatalf("len(contents)=%d, want 2", len(vars.Contents))
	}
}

func TestBuildGeminiVariablesTyped_FilterEmptyPartsAndContents(t *testing.T) {
	req := &GeminiRequest{
		Contents: []Content{
			{Role: "user", Parts: []Part{{Text: ""}, {Text: "valid"}}},
			{Role: "model", Parts: []Part{{Text: ""}}},
		},
	}
	vars := BuildGeminiVariablesTyped("gemini-pro", req, nil)
	if len(vars.Contents) != 1 {
		t.Fatalf("len(contents)=%d, want 1", len(vars.Contents))
	}
}

func TestBuildGeminiVariablesTyped_SystemInstructionFallback(t *testing.T) {
	req := &GeminiRequest{
		SystemInstruction: &Content{Role: "system", Parts: []Part{{Text: "you are helpful"}}},
		Contents: []Content{
			{Role: "model", Parts: []Part{{Text: "prior model output"}}},
		},
	}
	vars := BuildGeminiVariablesTyped("gemini-pro", req, nil)
	if len(vars.Contents) != 2 {
		t.Fatalf("len(contents)=%d, want 2", len(vars.Contents))
	}
	if vars.Contents[0].Role != "user" {
		t.Errorf("first content role=%v, want 'user'", vars.Contents[0].Role)
	}
}

func TestBuildGeminiVariablesTyped_TrailingModelFix(t *testing.T) {
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

	vars := BuildGeminiVariablesTyped("gemini-2.0-flash", req, cfg)
	if len(vars.Contents) != 3 {
		t.Fatalf("len(contents)=%d, want 3", len(vars.Contents))
	}

	last := vars.Contents[len(vars.Contents)-1]
	if last.Role != "user" {
		t.Errorf("last role=%v, want 'user'", last.Role)
	}
	if len(last.Parts) == 0 || last.Parts[0].Text != "继续" {
		t.Errorf("p0 text=%v, want '继续'", last.Parts)
	}
}

func TestBuildGeminiVariablesTyped_ToolsNativeSchema(t *testing.T) {
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

	vars := BuildGeminiVariablesTyped("gemini-3.6-flash", req, nil)
	if vars == nil || len(vars.Tools) != 1 {
		t.Fatalf("expected 1 tool in vars, got %v", vars.Tools)
	}

	decls := vars.Tools[0].FunctionDeclarations
	if len(decls) != 1 {
		t.Fatalf("expected 1 functionDeclaration, got %v", decls)
	}

	paramsMap, ok := decls[0].Parameters.(map[string]any)
	if !ok {
		t.Fatalf("parameters not a map: %v", decls[0].Parameters)
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

func TestBuildGeminiVariablesTyped_HistoryThoughtSignatureInjection(t *testing.T) {
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

	vars := BuildGeminiVariablesTyped("gemini-3.6-flash", req, nil)
	if vars == nil || len(vars.Contents) != 3 {
		t.Fatalf("expected 3 contents, got %v", len(vars.Contents))
	}

	modelTurn := vars.Contents[1]
	if modelTurn.Role != "model" {
		t.Fatalf("expected model turn at index 1, got %v", modelTurn.Role)
	}

	if len(modelTurn.Parts) != 1 {
		t.Fatalf("expected 1 part in model turn, got %v", len(modelTurn.Parts))
	}

	p0 := modelTurn.Parts[0]
	if p0.ThoughtSignature == "" {
		t.Fatalf("thoughtSignature missing or empty in history model functionCall part: %+v", p0)
	}
}

func TestBuildGeminiVariablesTyped_ToolResponseIsolation(t *testing.T) {
	geminiReq := &GeminiRequest{
		Contents: []Content{
			{Role: "user", Parts: []Part{{Text: "Find files"}}},
			{Role: "model", Parts: []Part{{FunctionCall: &FunctionCall{Name: "glob", Args: map[string]any{"pattern": "*.go"}}}}},
			{Role: "user", Parts: []Part{{FunctionResponse: &FunctionResponse{Name: "glob", Response: map[string]any{"result": `["main.go"]`}}}}},
			{Role: "user", Parts: []Part{{Text: "Analyze main.go"}}},
		},
	}

	vars := BuildGeminiVariablesTyped("gemini-3.6-flash", geminiReq, nil)
	if vars == nil {
		t.Fatal("vars should not be nil")
	}

	// 验证 4 个独立的 content 节点 (user -> model -> user(functionResponse) -> user(follow-up text))
	if len(vars.Contents) != 4 {
		t.Fatalf("len(contents)=%d, want 4 (functionResponse must NOT be merged with follow-up user text)", len(vars.Contents))
	}

	// 节点 2: tool response Content
	toolContent := vars.Contents[2]
	if toolContent.Role != "user" {
		t.Fatalf("content[2] role=%v, want 'user'", toolContent.Role)
	}
	if len(toolContent.Parts) != 1 {
		t.Fatalf("len(toolParts)=%d, want 1", len(toolContent.Parts))
	}
	pTool := toolContent.Parts[0]
	fr := pTool.FunctionResponse
	if fr == nil {
		t.Fatalf("missing functionResponse in pTool: %+v", pTool)
	}
	if fr.Name != "glob" {
		t.Errorf("functionResponse.name=%v, want 'glob'", fr.Name)
	}
	respMap, ok := fr.Response.(map[string]any)
	if !ok {
		t.Fatalf("functionResponse.response must be a map, got %T (%v)", fr.Response, fr.Response)
	}
	if _, hasResult := respMap["result"]; !hasResult && len(respMap) == 0 {
		t.Errorf("functionResponse.response map is empty")
	}

	// 节点 3: follow-up user Content
	userContent := vars.Contents[3]
	if userContent.Role != "user" {
		t.Fatalf("content[3] role=%v, want 'user'", userContent.Role)
	}
	if len(userContent.Parts) != 1 {
		t.Fatalf("len(userParts)=%d, want 1", len(userContent.Parts))
	}
	if userContent.Parts[0].Text != "Analyze main.go" {
		t.Errorf("content[3] part text=%v, want 'Analyze main.go'", userContent.Parts[0].Text)
	}
}

func TestBuildGeminiVariablesTyped_MultipleParallelToolResponsesMerged(t *testing.T) {
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

	vars := BuildGeminiVariablesTyped("gemini-3.6-flash", geminiReq, nil)
	if vars == nil {
		t.Fatal("vars should not be nil")
	}

	// 期望 4 个 Content 节点：
	// 0: user ("Find and read files")
	// 1: model (2 functionCalls)
	// 2: user (2 functionResponses 合并到同一个 Content 中！)
	// 3: user ("Analyze the content" 隔离保持独立)
	if len(vars.Contents) != 4 {
		t.Fatalf("len(contents)=%d, want 4 (multiple consecutive functionResponses MUST be merged into 1 Content)", len(vars.Contents))
	}

	toolContent := vars.Contents[2]
	if toolContent.Role != "user" {
		t.Fatalf("content[2] role=%v, want 'user'", toolContent.Role)
	}
	if len(toolContent.Parts) != 2 {
		t.Fatalf("len(toolParts)=%d, want 2 (both tool responses merged into 1 turn)", len(toolContent.Parts))
	}

	// 校验第 1 个 part 是 glob
	fr0 := toolContent.Parts[0].FunctionResponse
	if fr0 == nil || fr0.Name != "glob" {
		t.Errorf("fr0 name=%v, want 'glob'", fr0)
	}

	// 校验第 2 个 part 是 read
	fr1 := toolContent.Parts[1].FunctionResponse
	if fr1 == nil || fr1.Name != "read" {
		t.Errorf("fr1 name=%v, want 'read'", fr1)
	}

	// 校验 follow-up user text 节点 3 独立存在
	userContent := vars.Contents[3]
	if userContent.Role != "user" {
		t.Fatalf("content[3] role=%v, want 'user'", userContent.Role)
	}
	if len(userContent.Parts) != 1 {
		t.Fatalf("len(userParts)=%d, want 1", len(userContent.Parts))
	}
	if userContent.Parts[0].Text != "Analyze the content" {
		t.Errorf("content[3] part text=%v, want 'Analyze the content'", userContent.Parts[0].Text)
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

func TestBuildGeminiVariablesTyped_NormalizesInlineData(t *testing.T) {
	// 模拟客户端传入 snake_case 键 + Data URI 前缀 + 夹杂换行的多模态请求，
	// 经 DTO 反序列化归一与文本家族 BuildVariables 透传后，上行 payload 必须纯净。
	raw := `{
		"contents": [{
			"role": "user",
			"parts": [{
				"inline_data": {
					"mime_type": "image/png",
					"data": "data:image/png;base64,YWJj\n"
				}
			}]
		}]
	}`
	var req GeminiRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	cfg := &dummyConfig{trailingFixEnabled: false}

	vars := BuildGeminiVariablesTyped("gemini-1.5-flash", &req, cfg)
	if vars == nil || vars.GeminiRequest == nil {
		t.Fatal("vars should not be nil")
	}
	if len(vars.Contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(vars.Contents))
	}
	parts := vars.Contents[0].Parts
	if len(parts) != 1 || parts[0].InlineData == nil {
		t.Fatalf("expected 1 inlineData part, got %+v", parts)
	}
	got := parts[0].InlineData.Data
	if got != "YWJj" {
		t.Errorf("inlineData.data 未规范化: %q，期望 YWJj（无 URI 前缀、无空白、padding 完整）", got)
	}
	if parts[0].InlineData.MimeType != "image/png" {
		t.Errorf("inlineData.mimeType=%q，期望 image/png", parts[0].InlineData.MimeType)
	}
}
