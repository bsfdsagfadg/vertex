package transform

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

func TestSnakeToCamel(t *testing.T) {
	cases := map[string]string{
		"max_output_tokens": "maxOutputTokens",
		"top_p":             "topP",
		"topK":              "topK", // 无下划线原样
		"temperature":       "temperature",
		"thinking_config":   "thinkingConfig",
	}
	for in, want := range cases {
		if got := SnakeToCamel(in); got != want {
			t.Errorf("SnakeToCamel(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestCamelToSnake(t *testing.T) {
	if got := CamelToSnake("topP"); got != "top_p" {
		t.Errorf("CamelToSnake(topP)=%q", got)
	}
	if got := CamelToSnake("maxOutputTokens"); got != "max_output_tokens" {
		t.Errorf("CamelToSnake(maxOutputTokens)=%q", got)
	}
}

func TestNormalizeBase64(t *testing.T) {
	if got := NormalizeBase64("data:image/png;base64,AAAA"); got != "AAAA" {
		t.Errorf("data URI 剥离失败: %q", got)
	}
	if got := NormalizeBase64("a-b_c"); got != "a+b/c===" {
		t.Errorf("URL-safe+padding: %q, want a+b/c===", got)
	}
}

func TestConvertChatRequest_PlainText(t *testing.T) {
	cfg := config.StaticProvider(config.DefaultConfig())
	body := map[string]any{
		"model": "gemini-3.1-flash",
		"messages": []any{
			map[string]any{"role": "system", "content": "你是助手"},
			map[string]any{"role": "user", "content": "你好"},
		},
		"temperature": 0.7,
		"max_tokens":  float64(100),
	}
	model, payload, err := ConvertChatRequest(body, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if model != "gemini-3.1-flash" {
		t.Errorf("model=%q", model)
	}
	contents, _ := payload["contents"].([]any)
	if len(contents) != 1 {
		t.Fatalf("contents len=%d, want 1", len(contents))
	}
	c0 := contents[0].(map[string]any)
	if c0["role"] != "user" {
		t.Errorf("role=%v, want user", c0["role"])
	}
	if c0["parts"].([]any)[0].(map[string]any)["text"] != "你好" {
		t.Errorf("user text mismatch")
	}
	si, ok := payload["systemInstruction"].(map[string]any)
	if !ok {
		t.Fatal("missing systemInstruction")
	}
	if si["parts"].([]any)[0].(map[string]any)["text"] != "你是助手" {
		t.Error("system text mismatch")
	}
	gc := payload["generationConfig"].(map[string]any)
	if gc["temperature"] != 0.7 {
		t.Errorf("temperature=%v", gc["temperature"])
	}
	if gc["maxOutputTokens"] != float64(100) {
		t.Errorf("maxOutputTokens=%v", gc["maxOutputTokens"])
	}
}

func TestConvertChatRequest_EmptyMessages(t *testing.T) {
	_, _, err := ConvertChatRequest(map[string]any{"model": "m", "messages": []any{}}, config.StaticProvider(config.DefaultConfig()))
	if err == nil {
		t.Error("expected error for empty messages")
	}
}

func TestConvertChatRequest_MaxTokensInvalid(t *testing.T) {
	body := map[string]any{
		"model":      "m",
		"messages":   []any{map[string]any{"role": "user", "content": "hi"}},
		"max_tokens": float64(0),
	}
	if _, _, err := ConvertChatRequest(body, config.StaticProvider(config.DefaultConfig())); err == nil {
		t.Error("expected error for max_tokens=0")
	}
}

func TestBuildVertexVariables_SafetyDefault(t *testing.T) {
	cfg := config.StaticProvider(config.DefaultConfig())
	payload := map[string]any{"contents": []any{
		map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}},
	}}
	vars := BuildVertexVariables("gemini-3.1-flash", payload, cfg)
	if vars["model"] != "gemini-3.1-flash" {
		t.Error("model")
	}
	ss, ok := vars["safetySettings"].([]any)
	if !ok || len(ss) != 5 {
		t.Errorf("safetySettings=%v, want 5 BLOCK_NONE", vars["safetySettings"])
	}
	first := ss[0].(map[string]any)
	if first["threshold"] != "BLOCK_NONE" {
		t.Errorf("threshold=%v", first["threshold"])
	}
}

func TestBuildVertexVariables_SystemDemote(t *testing.T) {
	cfg := config.StaticProvider(config.DefaultConfig())
	payload := map[string]any{
		"contents":          []any{},
		"systemInstruction": map[string]any{"parts": []any{map[string]any{"text": "sys"}}},
	}
	vars := BuildVertexVariables("m", payload, cfg)
	if _, ok := vars["systemInstruction"]; ok {
		t.Error("systemInstruction 应在无 user 时被降级删除")
	}
	contents := vars["contents"].([]any)
	if len(contents) != 1 {
		t.Fatalf("contents len=%d, want 1", len(contents))
	}
	c0 := contents[0].(map[string]any)
	if c0["role"] != "user" {
		t.Errorf("降级后 role=%v, want user", c0["role"])
	}
}

func TestGeminiJSONToOAIJSON(t *testing.T) {
	resp := map[string]any{
		"candidates": []any{map[string]any{
			"content":      map[string]any{"parts": []any{map[string]any{"text": "Hello"}}, "role": "model"},
			"finishReason": "STOP",
		}},
		"usageMetadata": map[string]any{
			"promptTokenCount":     float64(5),
			"candidatesTokenCount": float64(1),
			"totalTokenCount":      float64(6),
		},
	}
	oai := GeminiJSONToOAIJSON(resp, "gemini-3.1-flash")
	if oai["object"] != "chat.completion" {
		t.Errorf("object=%v", oai["object"])
	}
	c0 := oai["choices"].([]any)[0].(map[string]any)
	if c0["finish_reason"] != "stop" {
		t.Errorf("finish_reason=%v", c0["finish_reason"])
	}
	if c0["message"].(map[string]any)["content"] != "Hello" {
		t.Errorf("content=%v", c0["message"].(map[string]any)["content"])
	}
	usage := oai["usage"].(map[string]any)
	if usage["prompt_tokens"] != 5 || usage["completion_tokens"] != 1 || usage["total_tokens"] != 6 {
		t.Errorf("usage=%v", usage)
	}
}

func TestMapFinishReason(t *testing.T) {
	cases := []struct {
		in   string
		tool bool
		want string
	}{
		{"STOP", false, "stop"},
		{"FINISH_REASON_UNSPECIFIED", false, "stop"}, // 未知 → stop
		{"SAFETY", false, "content_filter"},
		{"MAX_TOKENS", false, "length"},
		{"STOP", true, "tool_calls"}, // 有工具调用覆盖
		{"", false, "stop"},
	}
	for _, c := range cases {
		if got := MapFinishReason(c.in, c.tool); got != c.want {
			t.Errorf("MapFinishReason(%q,%v)=%q, want %q", c.in, c.tool, got, c.want)
		}
	}
}

func TestMergeContentBlocks(t *testing.T) {
	merged := MergeContentBlocks([]map[string]any{
		{"text": "Hello "},
		{"text": "World"},
	})
	if len(merged) != 1 {
		t.Fatalf("merged len=%d, want 1", len(merged))
	}
	if merged[0]["text"] != "Hello World" {
		t.Errorf("merged text=%q", merged[0]["text"])
	}
}

func TestMergeContentBlocks_ThoughtRetained(t *testing.T) {
	// 纯思考块不被 cleanSimple/cleanPart 丢弃
	merged := MergeContentBlocks([]map[string]any{
		{"text": "thinking text", "thought": true},
		{"text": "answer"},
	})
	if len(merged) != 2 {
		t.Fatalf("merged len=%d, want 2（thought+text 应分开）", len(merged))
	}
	if merged[0]["text"] != "thinking text" {
		t.Errorf("thought text=%q, want 'thinking text'", merged[0]["text"])
	}
	if merged[0]["thought"] != true {
		t.Error("thought 标记应保留")
	}
	if merged[1]["text"] != "answer" {
		t.Errorf("answer text=%q, want 'answer'", merged[1]["text"])
	}
}

func TestMergeContentBlocks_ThoughtOnlyBlocks(t *testing.T) {
	// 两个连续纯 thought block（无 text，仅 thought:true）各自保留不合并（m6）
	merged := MergeContentBlocks([]map[string]any{
		{"thought": true},
		{"thought": true, "thoughtSignature": "sig1"},
	})
	if len(merged) != 2 {
		t.Fatalf("merged len=%d, want 2（纯 thought block 不合并）", len(merged))
	}
	if merged[0]["thought"] != true {
		t.Error("第一个 thought block 标记应保留")
	}
	if merged[1]["thought"] != true {
		t.Error("第二个 thought block 标记应保留")
	}
	if merged[1]["thoughtSignature"] != "sig1" {
		t.Error("第二个 thought block 的 thoughtSignature 应保留")
	}
}

// TestConvertChatRequest_Full 测试 ConvertChatRequest 的完整转换：OpenAI 请求 → Gemini payload。
func TestConvertChatRequest_Full(t *testing.T) {
	cfg := config.StaticProvider(config.DefaultConfig())
	body := map[string]any{
		"model":    "gemini-2.5-flash",
		"messages": []any{map[string]any{"role": "user", "content": "Hello"}},
		"stream":   false,
	}

	model, geminiPayload, err := ConvertChatRequest(body, cfg)
	if err != nil {
		t.Fatalf("ConvertChatRequest failed: %v", err)
	}
	if model != "gemini-2.5-flash" {
		t.Errorf("model=%q, want gemini-2.5-flash", model)
	}
	if geminiPayload == nil {
		t.Fatal("geminiPayload is nil")
	}

	contents, ok := geminiPayload["contents"].([]any)
	if !ok || len(contents) == 0 {
		t.Fatal("missing contents")
	}
	firstContent := contents[0].(map[string]any)
	if firstContent["role"] != "user" {
		t.Errorf("role=%q, want user", firstContent["role"])
	}
	parts, ok := firstContent["parts"].([]any)
	if !ok || len(parts) == 0 {
		t.Fatal("missing parts")
	}
	firstPart := parts[0].(map[string]any)
	if firstPart["text"] != "Hello" {
		t.Errorf("text=%q, want Hello", firstPart["text"])
	}

	if gc, ok := geminiPayload["generationConfig"].(map[string]any); ok {
		if _, exists := gc["temperature"]; !exists {
			t.Error("generationConfig should have temperature")
		}
	}
}

// TestConvertChatRequest_WithTools 测试包含工具的请求转换。
func TestConvertChatRequest_WithTools(t *testing.T) {
	cfg := config.StaticProvider(config.DefaultConfig())
	body := map[string]any{
		"model":    "gemini-2.5-flash",
		"messages": []any{map[string]any{"role": "user", "content": "What's the weather?"}},
		"tools": []any{map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "get_weather",
				"description": "Get weather",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"location": map[string]any{"type": "string"},
					},
				},
			},
		}},
	}

	model, geminiPayload, err := ConvertChatRequest(body, cfg)
	if err != nil {
		t.Fatalf("ConvertChatRequest failed: %v", err)
	}
	if model != "gemini-2.5-flash" {
		t.Errorf("model=%q", model)
	}

	if _, ok := geminiPayload["tools"]; !ok {
		if _, ok := geminiPayload["toolConfig"]; !ok {
			t.Log("tools may be transformed to a different structure, check implement detail")
		}
	}
}

// TestConvertChatRequest_SystemInstruction 测试系统指令。
func TestConvertChatRequest_SystemInstruction(t *testing.T) {
	cfg := config.StaticProvider(config.DefaultConfig())
	body := map[string]any{
		"model":    "gemini-2.5-flash",
		"messages": []any{map[string]any{"role": "system", "content": "Be helpful."}},
	}

	_, geminiPayload, err := ConvertChatRequest(body, cfg)
	if err != nil {
		t.Fatalf("ConvertChatRequest failed: %v", err)
	}

	si, ok := geminiPayload["systemInstruction"].(map[string]any)
	if !ok {
		t.Fatal("missing systemInstruction")
	}
	parts, ok := si["parts"].([]any)
	if !ok || len(parts) == 0 {
		t.Fatal("systemInstruction missing parts")
	}
	firstPart := parts[0].(map[string]any)
	if firstPart["text"] != "Be helpful." {
		t.Errorf("text=%q, want Be helpful.", firstPart["text"])
	}
}

// TestIntegrationGeminiJSONToOAIJSON 测试 Gemini 非流式响应 → OAI 格式。
func TestIntegrationGeminiJSONToOAIJSON(t *testing.T) {
	geminiResp := map[string]any{
		"candidates": []any{map[string]any{
			"content":      map[string]any{"parts": []any{map[string]any{"text": "Hi there!"}}, "role": "model"},
			"finishReason": "STOP",
		}},
		"usageMetadata": map[string]any{
			"promptTokenCount":     float64(10),
			"candidatesTokenCount": float64(20),
			"totalTokenCount":      float64(30),
		},
	}

	oai := GeminiJSONToOAIJSON(geminiResp, "gemini-2.5-flash")
	if oai == nil {
		t.Fatal("GeminiJSONToOAIJSON returned nil")
	}
	if oai["object"] != "chat.completion" {
		t.Errorf("object=%q", oai["object"])
	}
	choices, ok := oai["choices"].([]any)
	if !ok || len(choices) == 0 {
		t.Fatal("no choices")
	}
	choice := choices[0].(map[string]any)
	if choice["finish_reason"] != "stop" {
		t.Errorf("finish_reason=%v", choice["finish_reason"])
	}
	msg, ok := choice["message"].(map[string]any)
	if !ok {
		t.Fatal("no message")
	}
	if msg["content"] != "Hi there!" {
		t.Errorf("content=%q", msg["content"])
	}

	usage, ok := oai["usage"].(map[string]any)
	if !ok {
		t.Fatal("no usage")
	}
	if usage["prompt_tokens"] != int(10) {
		t.Errorf("prompt_tokens=%v (%T)", usage["prompt_tokens"], usage["prompt_tokens"])
	}
	if usage["completion_tokens"] != int(20) {
		t.Errorf("completion_tokens=%v (%T)", usage["completion_tokens"], usage["completion_tokens"])
	}
}

// TestIntegrationGeminiJSONToOAIJSON_SafetyBlock 测试 Gemini 安全拦截 → content_filter。
func TestIntegrationGeminiJSONToOAIJSON_SafetyBlock(t *testing.T) {
	geminiResp := map[string]any{
		"candidates": []any{map[string]any{
			"content":      map[string]any{"parts": []any{}, "role": "model"},
			"finishReason": "SAFETY",
		}},
		"promptFeedback": map[string]any{"blockReason": "SAFETY"},
	}

	oai := GeminiJSONToOAIJSON(geminiResp, "gemini-2.5-flash")
	choices, ok := oai["choices"].([]any)
	if !ok || len(choices) == 0 {
		t.Fatal("no choices")
	}
	choice := choices[0].(map[string]any)
	if choice["finish_reason"] != "content_filter" {
		t.Errorf("finish_reason=%v, want content_filter", choice["finish_reason"])
	}
}

// TestIntegrationConvertRealtimeChunk 测试流式增量转换。
func TestIntegrationConvertRealtimeChunk(t *testing.T) {
	t.Run("first_chunk_has_role_delta", func(t *testing.T) {
		chunk := map[string]any{"candidates": []any{
			map[string]any{
				"content":      map[string]any{"parts": []any{map[string]any{"text": "Hi"}}, "role": "model"},
				"finishReason": "FINISH_REASON_UNSPECIFIED",
			},
		}}
		events := ConvertRealtimeChunk(chunk, "gemini-2.5-flash", "req-1", true, nil)
		if len(events) < 1 {
			t.Fatal("no events")
		}
		if !strings.Contains(events[0], `"role":"assistant"`) {
			t.Errorf("first event should contain role delta: %s", events[0])
		}
	})

	t.Run("finish_stop", func(t *testing.T) {
		chunk := map[string]any{
			"candidates": []any{map[string]any{
				"content":      map[string]any{"parts": []any{map[string]any{"text": "done"}}, "role": "model"},
				"finishReason": "STOP",
			}},
			"usageMetadata": map[string]any{
				"promptTokenCount": float64(5), "candidatesTokenCount": float64(10), "totalTokenCount": float64(15),
			},
		}
		events := ConvertRealtimeChunk(chunk, "m", "r", false, nil)
		var hasFinish bool
		for _, e := range events {
			if strings.Contains(e, `"finish_reason":"stop"`) {
				hasFinish = true
				break
			}
		}
		if !hasFinish {
			t.Errorf("should have finish_reason=stop event, got %v", events)
		}
	})

	t.Run("unspecified_no_finish", func(t *testing.T) {
		chunk := map[string]any{"candidates": []any{
			map[string]any{
				"content":      map[string]any{"parts": []any{map[string]any{"text": "x"}}, "role": "model"},
				"finishReason": "FINISH_REASON_UNSPECIFIED",
			},
		}}
		events := ConvertRealtimeChunk(chunk, "m", "r", false, nil)
		for _, e := range events {
			if strings.Contains(e, `"finish_reason":"`) && !strings.Contains(e, `"finish_reason":null`) {
				t.Errorf("UNSPECIFIED should not produce finish_reason: %s", e)
			}
		}
	})

	t.Run("function_call", func(t *testing.T) {
		chunk := map[string]any{"candidates": []any{map[string]any{
			"content": map[string]any{"parts": []any{
				map[string]any{"functionCall": map[string]any{"name": "get_weather", "args": map[string]any{"city": "SF"}}},
			}, "role": "model"},
			"finishReason": "STOP",
		}}}
		events := ConvertRealtimeChunk(chunk, "m", "r", false, nil)
		var hasToolCall bool
		for _, e := range events {
			if strings.Contains(e, `"tool_calls"`) {
				hasToolCall = true
				break
			}
		}
		if !hasToolCall {
			t.Errorf("should have tool_calls event, got %v", events)
		}
	})
}

// TestBuildVertexVariables 测试 BuildVertexVariables 的 produced structure。
func TestBuildVertexVariables(t *testing.T) {
	cfg := config.StaticProvider(config.DefaultConfig())
	geminiPayload := map[string]any{
		"contents": []any{map[string]any{
			"role":  "user",
			"parts": []any{map[string]any{"text": "Hello"}},
		}},
	}

	vars := BuildVertexVariables("gemini-2.5-flash", geminiPayload, cfg)
	if vars == nil {
		t.Fatal("BuildVertexVariables returned nil")
	}
	if vars["model"] != "gemini-2.5-flash" {
		t.Errorf("model=%q", vars["model"])
	}
	if _, ok := vars["contents"]; !ok {
		t.Error("vars missing contents")
	}
}

// TestMatchTrailingFixModel 验证 matchTrailingFixModel 纯精确匹配语义：
// 空格 trim、空值/空清单短路、版本/层级后缀与前缀误伤均不自动命中。
func TestMatchTrailingFixModel(t *testing.T) {
	cases := []struct {
		name    string
		model   string
		entries []string
		want    bool
	}{
		{"精确命中", "gemini-3.6-flash", []string{"gemini-3.6-flash"}, true},
		{"版本后缀不命中", "gemini-3.6-flash-001", []string{"gemini-3.6-flash"}, false},
		{"层级变体不命中", "gemini-3.6-flash-lite", []string{"gemini-3.6-flash"}, false},
		{"前缀误伤屏蔽", "gemini-3.6-flashback", []string{"gemini-3.6-flash"}, false},
		{"空模型名", "", []string{"gemini-3.6-flash"}, false},
		{"空清单", "gemini-3.6-flash", nil, false},
		{"带首尾空格命中", " gemini-3.6-flash ", []string{"gemini-3.6-flash"}, true},
		{"清单项带空格命中", "gemini-3.6-flash", []string{" gemini-3.6-flash "}, true},
		{"多条目第二项命中", "gemini-3.6-flash-lite", []string{"gemini-3.5-flash-lite", "gemini-3.6-flash-lite"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := matchTrailingFixModel(c.model, c.entries); got != c.want {
				t.Errorf("matchTrailingFixModel(%q, %v)=%v, want %v", c.model, c.entries, got, c.want)
			}
		})
	}
}

func TestBuildVertexVariables_TrailingModelFix(t *testing.T) {
	// 场景1: toggle ON + 命中模型 + 纯 functionResponse → role 修正 + 追加
	t.Run("命中模型+纯functionResponse→role修正+追加", func(t *testing.T) {
		cfg := config.StaticProvider(config.AppConfig{TrailingModelFixEnabled: true, TrailingFixModels: []string{"gemini-3.5-flash-lite", "gemini-3.6-flash"}})
		payload := map[string]any{
			"contents": []any{
				map[string]any{"role": "user", "parts": []any{map[string]any{"text": "天气如何"}}},
				map[string]any{"role": "model", "parts": []any{map[string]any{"functionCall": map[string]any{"name": "get_weather", "args": map[string]any{"city": "北京"}}}}},
				map[string]any{"role": "model", "parts": []any{map[string]any{"functionResponse": map[string]any{"name": "get_weather", "response": map[string]any{"temp": 22}}}}},
			},
		}
		vars := BuildVertexVariables("gemini-3.6-flash", payload, cfg)
		contents := vars["contents"].([]any)
		// 第3项 role→function，末尾含 functionResponse part → 视为 model turn 未闭合，追加 → 4 项
		if len(contents) != 4 {
			t.Fatalf("len(contents)=%d, want 4 (3项+追加)", len(contents))
		}
		third := contents[2].(map[string]any)
		if third["role"] != "function" {
			t.Errorf("third content role=%q, want function", third["role"])
		}
		last := contents[3].(map[string]any)
		if last["role"] != "user" {
			t.Errorf("last content role=%q, want user", last["role"])
		}
		lastParts := last["parts"].([]any)
		if len(lastParts) != 1 {
			t.Fatalf("last parts len=%d, want 1", len(lastParts))
		}
		if text, _ := lastParts[0].(map[string]any)["text"].(string); text != "继续" {
			t.Errorf("last parts[0].text=%q, want 继续", text)
		}
	})

	// 场景2: toggle ON + 命中模型 + 末尾 model → 无条件追加
	t.Run("命中模型+末尾model→无条件追加", func(t *testing.T) {
		cfg := config.StaticProvider(config.AppConfig{TrailingModelFixEnabled: true, TrailingFixModels: []string{"gemini-3.5-flash-lite", "gemini-3.6-flash"}})
		payload := map[string]any{
			"contents": []any{
				map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}},
				map[string]any{"role": "model", "parts": []any{map[string]any{"text": "hello"}}},
			},
		}
		vars := BuildVertexVariables("gemini-3.6-flash", payload, cfg)
		contents := vars["contents"].([]any)
		if len(contents) != 3 {
			t.Fatalf("len(contents)=%d, want 3 (2项+追加)", len(contents))
		}
		last := contents[2].(map[string]any)
		if last["role"] != "user" {
			t.Errorf("last content role=%q, want user", last["role"])
		}
	})

	// 场景3: toggle ON + 命中模型 + 末尾 user → 不追加（行为翻转）
	t.Run("命中模型+末尾user→不追加", func(t *testing.T) {
		cfg := config.StaticProvider(config.AppConfig{TrailingModelFixEnabled: true, TrailingFixModels: []string{"gemini-3.5-flash-lite", "gemini-3.6-flash"}})
		payload := map[string]any{
			"contents": []any{
				map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}},
				map[string]any{"role": "model", "parts": []any{map[string]any{"text": "hello"}}},
				map[string]any{"role": "user", "parts": []any{map[string]any{"text": "继续"}}},
			},
		}
		vars := BuildVertexVariables("gemini-3.6-flash", payload, cfg)
		contents := vars["contents"].([]any)
		// 末尾为 user → 条件不匹配，不追加
		if len(contents) != 3 {
			t.Fatalf("len(contents)=%d, want 3 (不追加)", len(contents))
		}
		last := contents[2].(map[string]any)
		if last["role"] != "user" {
			t.Errorf("last content role=%q, want user", last["role"])
		}
	})

	// 场景4: toggle ON + 不命中模型 → 不做任何修改
	t.Run("不命中模型→不做任何修改", func(t *testing.T) {
		cfg := config.StaticProvider(config.AppConfig{TrailingModelFixEnabled: true, TrailingFixModels: []string{"gemini-3.5-flash-lite", "gemini-3.6-flash"}})
		payload := map[string]any{
			"contents": []any{
				map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}},
				map[string]any{"role": "model", "parts": []any{map[string]any{"text": "hello"}}},
			},
		}
		vars := BuildVertexVariables("gemini-2.5-flash", payload, cfg)
		contents := vars["contents"].([]any)
		if len(contents) != 2 {
			t.Errorf("len(contents)=%d, want 2 (不做修改)", len(contents))
		}
	})

	// 场景5: toggle OFF + 命中模型 → 不做任何修改
	t.Run("开关关闭+命中模型→不做修改", func(t *testing.T) {
		cfg := config.StaticProvider(config.DefaultConfig())
		payload := map[string]any{
			"contents": []any{
				map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}},
				map[string]any{"role": "model", "parts": []any{map[string]any{"text": "hello"}}},
			},
		}
		vars := BuildVertexVariables("gemini-3.6-flash", payload, cfg)
		contents := vars["contents"].([]any)
		if len(contents) != 2 {
			t.Errorf("len(contents)=%d, want 2 (不做修改)", len(contents))
		}
	})

	// 场景6: toggle ON + 版本后缀 → 纯精确匹配不命中 → 不追加
	t.Run("版本后缀不自动命中→不追加", func(t *testing.T) {
		cfg := config.StaticProvider(config.AppConfig{TrailingModelFixEnabled: true, TrailingFixModels: []string{"gemini-3.5-flash-lite", "gemini-3.6-flash"}})
		payload := map[string]any{
			"contents": []any{
				map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}},
				map[string]any{"role": "model", "parts": []any{map[string]any{"text": "hello"}}},
			},
		}
		vars := BuildVertexVariables("gemini-3.6-flash-001", payload, cfg)
		contents := vars["contents"].([]any)
		if len(contents) != 2 {
			t.Errorf("len(contents)=%d, want 2 (纯精确匹配下版本后缀不自动命中→不追加)", len(contents))
		}
	})

	// 场景6b: toggle ON + 不在清单中的模型 → 即使开关开启也不追加
	t.Run("不在清单中的模型→不追加", func(t *testing.T) {
		cfg := config.StaticProvider(config.AppConfig{TrailingModelFixEnabled: true, TrailingFixModels: []string{"gemini-3.5-flash-lite", "gemini-3.6-flash"}})
		payload := map[string]any{
			"contents": []any{
				map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}},
				map[string]any{"role": "model", "parts": []any{map[string]any{"text": "hello"}}},
			},
		}
		vars := BuildVertexVariables("gemini-1.5-pro", payload, cfg)
		contents := vars["contents"].([]any)
		if len(contents) != 2 {
			t.Errorf("len(contents)=%d, want 2 (清单外模型不追加)", len(contents))
		}
	})

	// 场景7: toggle ON + 空 contents → 不 panic
	t.Run("空contents→不panic", func(t *testing.T) {
		cfg := config.StaticProvider(config.AppConfig{TrailingModelFixEnabled: true, TrailingFixModels: []string{"gemini-3.5-flash-lite", "gemini-3.6-flash"}})
		payload := map[string]any{
			"contents": []any{},
		}
		vars := BuildVertexVariables("gemini-3.6-flash", payload, cfg)
		contents := vars["contents"].([]any)
		if len(contents) != 0 {
			t.Errorf("len(contents)=%d, want 0", len(contents))
		}
	})

	// 场景8: toggle ON + 命中模型 + 末尾 function → 追加
	t.Run("命中模型+末尾function→追加", func(t *testing.T) {
		cfg := config.StaticProvider(config.AppConfig{TrailingModelFixEnabled: true, TrailingFixModels: []string{"gemini-3.5-flash-lite", "gemini-3.6-flash"}})
		payload := map[string]any{
			"contents": []any{
				map[string]any{"role": "user", "parts": []any{map[string]any{"text": "天气如何"}}},
				map[string]any{"role": "model", "parts": []any{map[string]any{"functionCall": map[string]any{"name": "get_weather"}}}},
				map[string]any{"role": "function", "parts": []any{map[string]any{"functionResponse": map[string]any{"name": "get_weather", "response": map[string]any{"temp": 22}}}}},
			},
		}
		vars := BuildVertexVariables("gemini-3.6-flash", payload, cfg)
		contents := vars["contents"].([]any)
		// 末尾 role="function" 但含 functionResponse part → 视为 model turn 未闭合，追加 → 4 项
		if len(contents) != 4 {
			t.Fatalf("len(contents)=%d, want 4 (3项+追加)", len(contents))
		}
		last := contents[3].(map[string]any)
		if last["role"] != "user" {
			t.Errorf("last content role=%q, want user", last["role"])
		}
	})

	// 场景9: toggle ON + 混合 parts（text+functionResponse）末尾 role 仍为 model → 追加
	t.Run("命中模型+混合parts末尾model→追加", func(t *testing.T) {
		cfg := config.StaticProvider(config.AppConfig{TrailingModelFixEnabled: true, TrailingFixModels: []string{"gemini-3.5-flash-lite", "gemini-3.6-flash"}})
		payload := map[string]any{
			"contents": []any{
				map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}},
				map[string]any{"role": "model", "parts": []any{
					map[string]any{"text": "结果如下"},
					map[string]any{"functionResponse": map[string]any{"name": "a", "response": map[string]any{"v": 1}}},
				}},
			},
		}
		vars := BuildVertexVariables("gemini-3.6-flash", payload, cfg)
		contents := vars["contents"].([]any)
		// 混合 parts 不被 normalizeFunctionResponseRoles 修正，role 仍为 model → 条件追加
		if len(contents) != 3 {
			t.Fatalf("len(contents)=%d, want 3 (2项+追加)", len(contents))
		}
		last := contents[2].(map[string]any)
		if last["role"] != "user" {
			t.Errorf("last content role=%q, want user", last["role"])
		}
	})

	// 场景10: toggle ON + 真实 bug 复现（OpenCode"继续"场景，user/model 交替，与生产日志结构一致）→ 末尾 user 含 functionResponse → 追加
	t.Run("命中模型+user含functionResponse→追加", func(t *testing.T) {
		cfg := config.StaticProvider(config.AppConfig{TrailingModelFixEnabled: true, TrailingFixModels: []string{"gemini-3.5-flash-lite", "gemini-3.6-flash"}})
		payload := map[string]any{
			"contents": []any{
				map[string]any{"role": "user", "parts": []any{map[string]any{"text": "这里是 win环境"}}},
				map[string]any{"role": "model", "parts": []any{map[string]any{"functionCall": map[string]any{"name": "glob", "args": map[string]any{"pattern": "**/*"}}}}},
				map[string]any{"role": "user", "parts": []any{map[string]any{"functionResponse": map[string]any{"name": "glob", "response": map[string]any{"content": "a.txt"}}}}},
				map[string]any{"role": "model", "parts": []any{map[string]any{"functionCall": map[string]any{"name": "read"}}}},
				map[string]any{"role": "user", "parts": []any{
					map[string]any{"functionResponse": map[string]any{"name": "read", "response": map[string]any{"content": "b.txt"}}},
					map[string]any{"text": "继续"},
				}},
			},
		}
		vars := BuildVertexVariables("gemini-3.6-flash", payload, cfg)
		contents := vars["contents"].([]any)
		// 最后一条 role=user 但含 functionResponse part → 必须追加
		if len(contents) != 6 {
			t.Fatalf("len(contents)=%d, want 6 (5项+追加)", len(contents))
		}
		last := contents[5].(map[string]any)
		if last["role"] != "user" {
			t.Errorf("last content role=%q, want user", last["role"])
		}
		lastParts := last["parts"].([]any)
		if len(lastParts) != 1 {
			t.Fatalf("last parts len=%d, want 1 (纯文本)", len(lastParts))
		}
	})

	// 场景11: toggle ON + 纯 functionResponse 结尾（路径A：中断后无用户文本）→ 追加
	t.Run("命中模型+纯functionResponse末尾→追加", func(t *testing.T) {
		cfg := config.StaticProvider(config.AppConfig{TrailingModelFixEnabled: true, TrailingFixModels: []string{"gemini-3.5-flash-lite", "gemini-3.6-flash"}})
		payload := map[string]any{
			"contents": []any{
				map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}},
				map[string]any{"role": "model", "parts": []any{map[string]any{"functionCall": map[string]any{"name": "read"}}}},
				map[string]any{"role": "user", "parts": []any{map[string]any{"functionResponse": map[string]any{"name": "read", "response": map[string]any{"content": "x"}}}}},
			},
		}
		vars := BuildVertexVariables("gemini-3.6-flash", payload, cfg)
		contents := vars["contents"].([]any)
		if len(contents) != 4 {
			t.Fatalf("len(contents)=%d, want 4 (3项+追加)", len(contents))
		}
		last := contents[3].(map[string]any)
		if last["role"] != "user" {
			t.Errorf("last content role=%q, want user", last["role"])
		}
	})
}

// TestMarshalRoundTrip 验证 ConvertChatRequest + BuildVertexVariables 的 JSON 可序列化。
func TestMarshalRoundTrip(t *testing.T) {
	cfg := config.StaticProvider(config.DefaultConfig())
	body := map[string]any{
		"model":    "gemini-2.5-flash",
		"messages": []any{map[string]any{"role": "user", "content": "Hello"}},
	}
	model, geminiPayload, err := ConvertChatRequest(body, cfg)
	if err != nil {
		t.Fatalf("ConvertChatRequest: %v", err)
	}
	payload := BuildVertexVariables(model, geminiPayload, cfg)

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("empty marshal result")
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded["model"] != "gemini-2.5-flash" {
		t.Errorf("model mismatch after round-trip")
	}
}

// TestToNativeSchema_NumericConstraintsAsStrings 验证数值约束字段被转为字符串。
func TestToNativeSchema_NumericConstraintsAsStrings(t *testing.T) {
	schema := map[string]any{
		"type":     "object",
		"minItems": 1, "maxItems": float64(10),
		"properties": map[string]any{
			"name": map[string]any{
				"type":      "string",
				"minLength": 2, "maxLength": float64(50),
			},
		},
	}
	native := toNativeSchema(schema).(map[string]any)

	for _, field := range []string{"minItems", "maxItems"} {
		v, ok := native[field]
		if !ok {
			t.Fatalf("字段 %s 被删除", field)
		}
		if _, ok := v.(string); !ok {
			t.Errorf("%s 应为字符串，实际是 %T(%v)", field, v, v)
		}
	}
	props, _ := native["properties"].([]any)
	if len(props) > 0 {
		prop := props[0].(map[string]any)
		val := prop["value"].(map[string]any)
		for _, field := range []string{"minLength", "maxLength"} {
			v, ok := val[field]
			if !ok {
				t.Fatalf("嵌套字段 %s 被删除", field)
			}
			if _, ok := v.(string); !ok {
				t.Errorf("嵌套 %s 应为字符串，实际是 %T(%v)", field, v, v)
			}
		}
	}
}

// TestToNativeSchema_DefaultNullablePreserved 验证 Gemini 支持的 default/nullable/examples 不被误删。
func TestToNativeSchema_DefaultNullablePreserved(t *testing.T) {
	schema := map[string]any{
		"type":     "object",
		"default":  "hello",
		"nullable": true,
		"examples": []any{"ex1", "ex2"},
		"properties": map[string]any{
			"x": map[string]any{"type": "string", "default": "world"},
		},
	}
	native := toNativeSchema(schema).(map[string]any)
	for _, field := range []string{"default", "nullable", "examples"} {
		if _, ok := native[field]; !ok {
			t.Errorf("字段 %s 被错误剔除（Gemini 原生支持）", field)
		}
	}
	props, _ := native["properties"].([]any)
	if len(props) > 0 {
		val := props[0].(map[string]any)["value"].(map[string]any)
		if _, ok := val["default"]; !ok {
			t.Errorf("嵌套 property 的 default 被错误剔除")
		}
	}
}

// TestToNativeSchema_UnknownTypeFallsBackToSTRING 验证非标准 type 兜底 STRING。
func TestToNativeSchema_UnknownTypeFallsBackToSTRING(t *testing.T) {
	native := toNativeSchema(map[string]any{"type": "any", "properties": map[string]any{}}).(map[string]any)
	if native["type"] != "STRING" {
		t.Errorf("未知类型 'any' 应兜底为 STRING，实际: %v", native["type"])
	}
}

// TestMergeContiguousRoles 验证相邻同 role content 被合并。
func TestMergeContiguousRoles(t *testing.T) {
	contents := []any{
		map[string]any{"role": "model", "parts": []any{map[string]any{"text": "hi"}}},
		map[string]any{"role": "user", "parts": []any{map[string]any{"text": "q1"}}},
		map[string]any{"role": "user", "parts": []any{map[string]any{"text": "q2"}}},
		map[string]any{"role": "model", "parts": []any{map[string]any{"text": "bye"}}},
	}
	merged := mergeContiguousRoles(contents).([]any)
	if len(merged) != 3 {
		t.Fatalf("应合并为 3 个 content，实际 %d", len(merged))
	}
	user := merged[1].(map[string]any)
	if parts := user["parts"].([]any); len(parts) != 2 {
		t.Errorf("user content 应有 2 个 parts，实际 %d", len(parts))
	}
}

// TestMergeContiguousRoles_FunctionResponse 合并多轮工具结果（连续 function 角色）。
func TestMergeContiguousRoles_FunctionResponse(t *testing.T) {
	contents := []any{
		map[string]any{"role": "model", "parts": []any{
			map[string]any{"functionCall": map[string]any{"name": "get_weather", "args": map[string]any{"city": "NY"}}},
			map[string]any{"functionCall": map[string]any{"name": "get_time", "args": map[string]any{"tz": "EST"}}},
		}},
		map[string]any{"role": "function", "parts": []any{
			map[string]any{"functionResponse": map[string]any{"name": "get_weather", "response": map[string]any{"temp": 20}}},
		}},
		map[string]any{"role": "function", "parts": []any{
			map[string]any{"functionResponse": map[string]any{"name": "get_time", "response": map[string]any{"time": "10:00"}}},
		}},
		map[string]any{"role": "model", "parts": []any{map[string]any{"text": "done"}}},
	}
	merged := mergeContiguousRoles(contents).([]any)
	if len(merged) != 3 {
		t.Fatalf("应合并为 3 个 content，实际 %d", len(merged))
	}
	funcContent := merged[1].(map[string]any)
	if parts := funcContent["parts"].([]any); len(parts) != 2 {
		t.Errorf("function content 应合并两个 functionResponse 为 2 个 parts，实际 %d", len(parts))
	}
}

// TestConvertToolsFormat_NumericConstraints 端到端验证工具参数数值约束转字符串。
func TestConvertToolsFormat_NumericConstraints(t *testing.T) {
	geminiPayload := map[string]any{
		"tools": []any{map[string]any{
			"functionDeclarations": []any{map[string]any{
				"name": "list_items",
				"parameters": map[string]any{
					"type":       "object",
					"properties": map[string]any{},
					"minItems":   1,
					"maxItems":   float64(100),
				},
			}},
		}},
	}
	vars := BuildVertexVariables("gemini-3-flash", geminiPayload, config.StaticProvider(config.AppConfig{})) //nolint:exhaustruct
	dump, _ := json.Marshal(vars["tools"])
	if !strings.Contains(string(dump), `"minItems":"1"`) {
		t.Errorf("minItems 应转为字符串 \"1\": %s", dump)
	}
}

// TestNormalizePartMultimodal 验证 normalizePart 对 OpenAI 风格多模态 part 的归一（Fix 7）：
// image_url(data:/http)/input_image、media/file/file_data、inline_data → 对应 Gemini part。
func TestNormalizePartMultimodal(t *testing.T) {
	got := normalizePart(map[string]any{
		"type":      "image_url",
		"image_url": map[string]any{"url": "data:image/png;base64,QQ=="},
	})
	id, _ := got["inlineData"].(map[string]any)
	if id == nil || id["mimeType"] != "image/png" || id["data"] != "QQ==" {
		t.Fatalf("image_url data: 应转 inlineData，got %v", got)
	}

	got = normalizePart(map[string]any{
		"type":      "image_url",
		"image_url": map[string]any{"url": "https://x.com/a.mp4"},
	})
	fd, _ := got["fileData"].(map[string]any)
	if fd == nil || fd["fileUri"] != "https://x.com/a.mp4" || fd["mimeType"] != "video/mp4" {
		t.Fatalf("image_url http 应转 fileData(video/mp4)，got %v", got)
	}

	got = normalizePart(map[string]any{
		"type": "file", "file_uri": "gs://b/x.pdf", "mime_type": "application/pdf",
	})
	fd, _ = got["fileData"].(map[string]any)
	if fd == nil || fd["fileUri"] != "gs://b/x.pdf" || fd["mimeType"] != "application/pdf" {
		t.Fatalf("file 应转 fileData，got %v", got)
	}

	got = normalizePart(map[string]any{
		"type": "inline_data", "inline_data": map[string]any{"mime_type": "audio/wav", "data": "ZGF0YQ=="},
	})
	id, _ = got["inlineData"].(map[string]any)
	if id == nil || id["mimeType"] != "audio/wav" || id["data"] != "ZGF0YQ==" {
		t.Fatalf("inline_data 应转 inlineData，got %v", got)
	}

	got = normalizePart(map[string]any{"type": "text", "text": "hi"})
	if got["text"] != "hi" {
		t.Fatalf("text part，got %v", got)
	}

	got = normalizePart(map[string]any{"type": "weird", "some_key": "v"})
	if got["someKey"] != "v" {
		t.Fatalf("未知 part 应 camelCase 透传，got %v", got)
	}
}

// TestGuessMIMEFromURI 验证多类型 mime 猜测覆盖图/视频/音频/pdf/txt。
func TestGuessMIMEFromURI(t *testing.T) {
	cases := map[string]string{
		"a.jpg": "image/jpeg", "a.png": "image/png", "a.webp": "image/webp", "a.gif": "image/gif",
		"a.mp4": "video/mp4", "a.mov": "video/quicktime", "a.webm": "video/webm",
		"a.mp3": "audio/mpeg", "a.wav": "audio/wav", "a.ogg": "audio/ogg",
		"a.pdf": "application/pdf", "a.txt": "text/plain", "a.xyz": "image/png",
		"http://x/a.MP4?t=1": "video/mp4",
	}
	for in, want := range cases {
		if got := guessMIMEFromURI(in); got != want {
			t.Errorf("guessMIMEFromURI(%q)=%q，want %q", in, got, want)
		}
	}
}

// splitAssistantContent：assistant 文本里的 markdown data-URI 图片必须重解析为 inlineData，
// 否则巨型 base64 markdown 作为文本进 model 角色，多轮改图被上游拒。
func TestSplitAssistantContent_ImageMarkdown(t *testing.T) {
	s := "这是图片：\n\n![image](data:image/png;base64,iVBORw0KGgoAAAANS) 完成"
	parts := splitAssistantContent(s)
	var hasInline, hasText bool
	for _, p := range parts {
		m := p.(map[string]any)
		if id, ok := m["inlineData"].(map[string]any); ok {
			if id["mimeType"] == "image/png" {
				hasInline = true
			}
		}
		if txt, ok := m["text"].(string); ok && txt != "" {
			hasText = true
		}
	}
	if !hasInline {
		t.Errorf("markdown 图片应重解析为 inlineData，got %v", parts)
	}
	if !hasText {
		t.Errorf("图片前后的文本应保留为 text part")
	}
}

func TestSplitAssistantContent_PlainText(t *testing.T) {
	parts := splitAssistantContent("纯文本回复")
	if len(parts) != 1 || parts[0].(map[string]any)["text"] != "纯文本回复" {
		t.Errorf("纯文本应原样为单个 text part，got %v", parts)
	}
}
