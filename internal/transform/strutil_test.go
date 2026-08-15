package transform

import (
	"encoding/base64"
	"testing"
)

func TestDecodeBase64Loose(t *testing.T) {
	std := base64.StdEncoding.EncodeToString([]byte("hello world"))

	cases := []struct {
		name    string
		in      string
		want    []byte
		wantErr bool
	}{
		{"standard base64", std, []byte("hello world"), false},
		// URL-safe: 含 -/_ 字符。bytes 0xfb 0xff 0xbf → std "+/+/" 中含 +、/
		{"url-safe with dash underscore", "-_-_", mustStdDecode("+/+/"), false},
		{"missing padding restored", "aGVsbG8", []byte("hello"), false}, // "hello" std = aGVsbG8= (缺1个=)
		{"empty string", "", []byte{}, false},
		{"invalid chars", "@@@@", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := decodeBase64Loose(c.in)
			if c.wantErr {
				if err == nil {
					t.Errorf("应返回错误，实际 got=%v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("意外错误: %v", err)
			}
			if string(got) != string(c.want) {
				t.Errorf("decodeBase64Loose(%q)=%v，期望 %v", c.in, got, c.want)
			}
		})
	}
}

func mustStdDecode(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}