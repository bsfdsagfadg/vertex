package vertex

import (
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/transform"
)

// ---- 离线估算测试（强类型 contents）----

func TestEstimateTokens(t *testing.T) {
	cases := []struct {
		name     string
		contents []transform.Content
		want     int
	}{
		{
			name:     "empty contents",
			contents: nil,
			want:     0,
		},
		{
			name: "ascii only text",
			contents: []transform.Content{{Parts: []transform.Part{
				{Text: "hello world"},
			}}},
			// "hello world" = 11 ASCII chars → 11/4 = 2
			want: 2,
		},
		{
			name: "non-ascii text (Chinese)",
			contents: []transform.Content{{Parts: []transform.Part{
				{Text: "你好世界"},
			}}},
			// "你好世界" = 4 non-ASCII chars → 4 + 4/2 = 6
			want: 6,
		},
		{
			name: "mixed ascii and non-ascii",
			contents: []transform.Content{{Parts: []transform.Part{
				{Text: "Hello 你好"},
			}}},
			// "Hello " = 6 ASCII → 6/4 = 1
			// "你好" = 2 non-ASCII → 2 + 2/2 = 3
			// total = 4
			want: 4,
		},
		{
			name: "inlineData media adds 1024",
			contents: []transform.Content{{Parts: []transform.Part{
				{InlineData: &transform.InlineData{MimeType: "image/png", Data: "abc"}},
			}}},
			want: 1024,
		},
		{
			name: "fileData media adds 1024",
			contents: []transform.Content{{Parts: []transform.Part{
				{FileData: &transform.FileData{MimeType: "image/jpeg", FileURI: "gs://bucket/img.jpg"}},
			}}},
			want: 1024,
		},
		{
			name: "text + inlineData combined",
			contents: []transform.Content{{Parts: []transform.Part{
				{Text: "Describe this image"},
				{InlineData: &transform.InlineData{MimeType: "image/png", Data: "xyz"}},
			}}},
			// "Describe this image" = 19 ASCII → 19/4 = 4
			// inlineData = 1024
			// total = 1028
			want: 1028,
		},
		{
			name: "multiple contents",
			contents: []transform.Content{
				{Parts: []transform.Part{{Text: "hi"}}},
				{Parts: []transform.Part{{Text: "hello"}}},
			},
			// "hi" = 2/4 = 0, "hello" = 5/4 = 1 → total = 1
			want: 1,
		},
		{
			name: "inlineData wins over fileData",
			contents: []transform.Content{{Parts: []transform.Part{
				{InlineData: &transform.InlineData{MimeType: "image/png", Data: "a"}, FileData: &transform.FileData{FileURI: "gs://b"}},
			}}},
			// 旧语义：inlineData 存在即 1024（else-if 链）
			want: 1024,
		},
		{
			name: "empty string text counts 0",
			contents: []transform.Content{{Parts: []transform.Part{
				{Text: ""},
			}}},
			want: 0,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := estimateTokensTyped(c.contents); got != c.want {
				t.Errorf("estimateTokensTyped=%d，期望 %d", got, c.want)
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