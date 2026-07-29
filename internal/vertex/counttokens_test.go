package vertex

import "testing"

// ---- 新：estimateTokens 离线估算测试 ----

func TestEstimateTokens(t *testing.T) {
	cases := []struct {
		name     string
		contents []any
		want     int
	}{
		{
			name:     "empty contents",
			contents: nil,
			want:     0,
		},
		{
			name: "ascii only text",
			contents: []any{map[string]any{
				"parts": []any{map[string]any{"text": "hello world"}},
			}},
			// "hello world" = 11 ASCII chars → 11/4 = 2
			want: 2,
		},
		{
			name: "non-ascii text (Chinese)",
			contents: []any{map[string]any{
				"parts": []any{map[string]any{"text": "你好世界"}},
			}},
			// "你好世界" = 4 non-ASCII chars → 4 + 4/2 = 6
			want: 6,
		},
		{
			name: "mixed ascii and non-ascii",
			contents: []any{map[string]any{
				"parts": []any{map[string]any{"text": "Hello 你好"}},
			}},
			// "Hello " = 6 ASCII → 6/4 = 1
			// "你好" = 2 non-ASCII → 2 + 2/2 = 3
			// total = 4
			want: 4,
		},
		{
			name: "inlineData media adds 1024",
			contents: []any{map[string]any{
				"parts": []any{map[string]any{
					"inlineData": map[string]any{"mimeType": "image/png", "data": "abc"},
				}},
			}},
			want: 1024,
		},
		{
			name: "fileData media adds 1024",
			contents: []any{map[string]any{
				"parts": []any{map[string]any{
					"fileData": map[string]any{"mimeType": "image/jpeg", "fileUri": "gs://bucket/img.jpg"},
				}},
			}},
			want: 1024,
		},
		{
			name: "text + inlineData combined",
			contents: []any{map[string]any{
				"parts": []any{
					map[string]any{"text": "Describe this image"},
					map[string]any{"inlineData": map[string]any{"mimeType": "image/png", "data": "xyz"}},
				},
			}},
			// "Describe this image" = 19 ASCII → 19/4 = 4
			// inlineData = 1024
			// total = 1028
			want: 1028,
		},
		{
			name: "multiple contents",
			contents: []any{
				map[string]any{
					"parts": []any{map[string]any{"text": "hi"}},
				},
				map[string]any{
					"parts": []any{map[string]any{"text": "hello"}},
				},
			},
			// "hi" = 2/4 = 0, "hello" = 5/4 = 1 → total = 1
			want: 1,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := estimateTokens(c.contents); got != c.want {
				t.Errorf("estimateTokens=%d，期望 %d", got, c.want)
			}
		})
	}
}

// ---- coerceTokenCount ----

func TestCoerceTokenCount(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want int
	}{
		{"float64", float64(42), 42},
		{"float64 truncates", float64(42.9), 42},
		{"int", 7, 7},
		{"numeric string", "123", 123},
		{"trimmed not supported (atoi strict)", " 5 ", 0}, // Atoi 不 trim
		{"non-numeric string", "abc", 0},
		{"nil", nil, 0},
		{"bool", true, 0},
		{"zero float", float64(0), 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := coerceTokenCount(c.in); got != c.want {
				t.Errorf("coerceTokenCount(%v)=%d，期望 %d", c.in, got, c.want)
			}
		})
	}
}
