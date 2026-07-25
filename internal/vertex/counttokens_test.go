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

func TestEstimateStringTokens(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"", 0},
		{"a", 0},           // 1/4 = 0
		{"abcd", 1},        // 4/4 = 1
		{"abcdefgh", 2},    // 8/4 = 2
		{"你", 1},           // 1 + 0 = 1
		{"你好", 3},         // 2 + 1 = 3
		{"a你好", 3},         // "a"→1/4=0, "你好"→2+2/2=3 → total=3
	}
	for _, c := range cases {
		t.Run(c.text, func(t *testing.T) {
			if got := EstimateStringTokens(c.text); got != c.want {
				t.Errorf("EstimateStringTokens(%q)=%d，期望 %d", c.text, got, c.want)
			}
		})
	}
}

// ---- 兼容保留：parseCountTokensResponse 旧测试 ----

func TestParseCountTokensResponse(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int
	}{
		{
			name: "data.ui.countTokensV2 (number)",
			raw:  `[{"results":[{"data":{"ui":{"countTokensV2":{"totalTokens":42}}}}]}]`,
			want: 42,
		},
		{
			name: "data.countTokensV2 (number)",
			raw:  `[{"results":[{"data":{"countTokensV2":{"totalTokens":100}}}]}]`,
			want: 100,
		},
		{
			name: "data.countTokens (number)",
			raw:  `[{"results":[{"data":{"countTokens":{"totalTokens":7}}}]}]`,
			want: 7,
		},
		{
			name: "totalTokens as string",
			raw:  `[{"results":[{"data":{"ui":{"countTokensV2":{"totalTokens":"256"}}}}]}]`,
			want: 256,
		},
		{
			name: "single object (not array)",
			raw:  `{"results":[{"data":{"countTokensV2":{"totalTokens":15}}}]}`,
			want: 15,
		},
		{
			name: "entry-level errors skipped",
			raw:  `[{"errors":[{"message":"boom"}]},{"results":[{"data":{"countTokensV2":{"totalTokens":9}}}]}]`,
			want: 9,
		},
		{
			name: "result-level errors skipped",
			raw:  `[{"results":[{"errors":[{"x":1}]},{"data":{"countTokensV2":{"totalTokens":11}}}]}]`,
			want: 11,
		},
		{
			name: "ui preferred over flat",
			raw:  `[{"results":[{"data":{"ui":{"countTokensV2":{"totalTokens":1}},"countTokensV2":{"totalTokens":999}}}]}]`,
			want: 1,
		},
		{
			name: "no countData returns 0",
			raw:  `[{"results":[{"data":{"somethingElse":{}}}]}]`,
			want: 0,
		},
		{
			name: "missing totalTokens returns 0",
			raw:  `[{"results":[{"data":{"countTokensV2":{}}}]}]`,
			want: 0,
		},
		{
			name: "empty results returns 0",
			raw:  `[{"results":[]}]`,
			want: 0,
		},
		{
			name: "invalid json returns 0",
			raw:  `not json{`,
			want: 0,
		},
		{
			name: "json primitive returns 0",
			raw:  `12345`,
			want: 0,
		},
		{
			name: "empty array returns 0",
			raw:  `[]`,
			want: 0,
		},
		{
			name: "totalTokens non-numeric string returns 0",
			raw:  `[{"results":[{"data":{"countTokensV2":{"totalTokens":"abc"}}}]}]`,
			want: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseCountTokensResponse(c.raw); got != c.want {
				t.Errorf("parseCountTokensResponse(%s)=%d，期望 %d", c.raw, got, c.want)
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
