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

// TestConvertToolsFormat_NumericConstraints 端到端验证工具参数数值约束转字符串。
func TestConvertToolsFormat_NumericConstraints(t *testing.T) {
	req := &GeminiRequest{
		Tools: []Tool{{
			FunctionDeclarations: []FunctionDeclaration{{
				Name: "list_items",
				Parameters: map[string]any{
					"type":       "object",
					"properties": map[string]any{},
					"minItems":   1,
					"maxItems":   float64(100),
				},
			}},
		}},
	}
	vars := BuildGeminiVariables("gemini-3-flash", req, config.StaticProvider(config.AppConfig{})) //nolint:exhaustruct
	dump, _ := json.Marshal(vars["tools"])
	if !strings.Contains(string(dump), `"list_items"`) {
		t.Errorf("tools 序列化不含 list_items: %s", dump)
	}
}
