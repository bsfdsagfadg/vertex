package api

import (
	"testing"
)

// ── hasValidStreamOutput ──

func TestHasValidStreamOutput_ValidContent(t *testing.T) {
	events := []string{`data: {"choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}`}
	if !hasValidStreamOutput(events) {
		t.Error("valid content should return true")
	}
}

func TestHasValidStreamOutput_EmptyContent(t *testing.T) {
	events := []string{`data: {"choices":[{"index":0,"delta":{"content":""},"finish_reason":null}]}`}
	if hasValidStreamOutput(events) {
		t.Error("empty content string should return false")
	}
}

func TestHasValidStreamOutput_NullContent(t *testing.T) {
	events := []string{`data: {"choices":[{"index":0,"delta":{"content":null},"finish_reason":null}]}`}
	if hasValidStreamOutput(events) {
		t.Error("null content should return false")
	}
}

func TestHasValidStreamOutput_ReasoningContent(t *testing.T) {
	events := []string{`data: {"choices":[{"index":0,"delta":{"reasoning_content":"thinking..."},"finish_reason":null}]}`}
	if !hasValidStreamOutput(events) {
		t.Error("reasoning_content should return true")
	}
}

func TestHasValidStreamOutput_ToolCalls(t *testing.T) {
	events := []string{`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"search","arguments":"{}"}}]},"finish_reason":null}]}`}
	if !hasValidStreamOutput(events) {
		t.Error("tool_calls should return true")
	}
}

func TestHasValidStreamOutput_EmptyEvents(t *testing.T) {
	if hasValidStreamOutput(nil) {
		t.Error("nil events should return false")
	}
	if hasValidStreamOutput([]string{}) {
		t.Error("empty events should return false")
	}
}

func TestHasValidStreamOutput_MetadataOnly(t *testing.T) {
	events := []string{`data: {"id":"chatcmpl-xxx","object":"chat.completion.chunk","created":123,"model":"gemini","choices":[{"index":0,"delta":{},"finish_reason":null}]}`}
	if hasValidStreamOutput(events) {
		t.Error("metadata-only event (no content/reasoning/tool_calls) should return false")
	}
}

// ── hasGeminiValidOutput ──

func TestHasGeminiValidOutput_NonMapPart(t *testing.T) {
	// 裸字符串 part 不应误触发首字计时
	chunk := map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"parts": []any{
						"bare string part",
					},
					"role": "model",
				},
			},
		},
	}
	if hasGeminiValidOutput(chunk) {
		t.Error("bare string part should NOT be considered valid output")
	}

	// 含 functionCall 的 part 应返回 true
	chunk = map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"parts": []any{
						map[string]any{"functionCall": map[string]any{"name": "get_weather"}},
					},
					"role": "model",
				},
			},
		},
	}
	if !hasGeminiValidOutput(chunk) {
		t.Error("functionCall part should be considered valid output")
	}
}