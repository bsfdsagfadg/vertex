package api

import (
	"testing"
)

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