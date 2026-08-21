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

func TestNormalizeBase64(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"data uri prefix stripped", "data:image/png;base64,iVBORw0KGgoAAAANSUhEUg==", "iVBORw0KGgoAAAANSUhEUg=="},
		{"data uri prefix case insensitive", "DATA:image/png;base64,YWJj", "YWJj"},
		{"url-safe chars restored", "-_-_", "+/+/"},
		{"missing padding restored", "aGVsbG8", "aGVsbG8="},
		{"interior newline stripped", "YWJj\nY2Rl\n", "YWJjY2Rl"},
		{"interior spaces stripped", "YW Jj", "YWJj"},
		{"whitespace only", "  \t ", ""},
		{"already normalized unchanged", "aGVsbG8=", "aGVsbG8="},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NormalizeBase64(c.in)
			if got != c.want {
				t.Errorf("NormalizeBase64(%q)=%q，期望 %q", c.in, got, c.want)
			}
			if again := NormalizeBase64(got); again != got {
				t.Errorf("NormalizeBase64 非幂等: NormalizeBase64(%q)=%q", got, again)
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
