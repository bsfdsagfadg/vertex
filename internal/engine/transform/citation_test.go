package transform

import (
	"testing"
)

func TestConvertCitationsToMarkdown(t *testing.T) {
	gm := map[string]any{
		"groundingChunks": []any{
			map[string]any{
				"web": map[string]any{
					"uri":   "https://example.com/weather",
					"title": "北京天气预报",
				},
			},
			map[string]any{
				"web": map[string]any{
					"uri":   "https://example.com/time",
					"title": "北京时间",
				},
			},
		},
	}

	inputText := "当前北京时间为：上午 11:46 左右[cite: 0f151430-2]。天气状况：多云转阴[cite: 0f151430-1]。"

	got := ConvertCitationsToMarkdown(inputText, gm)

	want1 := "https://example.com/time"
	want2 := "https://example.com/weather"

	if !contains(got, want1) || !contains(got, want2) {
		t.Errorf("ConvertCitationsToMarkdown failed.\nGot: %s", got)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || findSubstr(s, substr))
}

func findSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
