package strutil

import (
	"testing"
)

func TestPadB64(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"YWJj", "YWJj"},
		{"YWJjZA", "YWJjZA=="},
		{"YWJjZGU", "YWJjZGU="},
		{"a-b_c", "a+b/c==="},
	}
	for _, tt := range tests {
		got := PadB64(tt.input)
		if got != tt.expected {
			t.Errorf("PadB64(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestDecodeB64(t *testing.T) {
	data := "hello world"
	encoded := "aGVsbG8gd29ybGQ" // unpadded
	decoded, err := DecodeB64(encoded)
	if err != nil {
		t.Fatalf("DecodeB64 failed: %v", err)
	}
	if string(decoded) != data {
		t.Errorf("got %q, want %q", string(decoded), data)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := FirstNonEmpty("", "  ", "foo", "bar"); got != "foo" {
		t.Errorf("FirstNonEmpty got %q, want %q", got, "foo")
	}
	if got := FirstNonEmpty("", " "); got != "" {
		t.Errorf("FirstNonEmpty got %q, want empty", got)
	}
}

func TestFirstNonEmptyStr(t *testing.T) {
	if got := FirstNonEmptyStr("", "bar"); got != "bar" {
		t.Errorf("FirstNonEmptyStr got %q, want %q", got, "bar")
	}
	if got := FirstNonEmptyStr("foo", "bar"); got != "foo" {
		t.Errorf("FirstNonEmptyStr got %q, want %q", got, "foo")
	}
}

func TestIsTruthyStr(t *testing.T) {
	truthy := []string{"1", "true", "TRUE", "yes", "on", "  Yes  "}
	for _, s := range truthy {
		if !IsTruthyStr(s) {
			t.Errorf("IsTruthyStr(%q) should be true", s)
		}
	}
	falsy := []string{"0", "false", "no", "off", "", "random"}
	for _, s := range falsy {
		if IsTruthyStr(s) {
			t.Errorf("IsTruthyStr(%q) should be false", s)
		}
	}
}

func TestToStrAndToInt(t *testing.T) {
	if got := ToStr("hello"); got != "hello" {
		t.Errorf("ToStr(string) got %q", got)
	}
	if got := ToStr(123); got != "" {
		t.Errorf("ToStr(non-string) got %q", got)
	}
	if got := ToStrOr("", "def"); got != "def" {
		t.Errorf("ToStrOr got %q, want %q", got, "def")
	}
	if got := ToStrOr("val", "def"); got != "val" {
		t.Errorf("ToStrOr got %q, want %q", got, "val")
	}

	if got := ToInt(10, 0); got != 10 {
		t.Errorf("ToInt(int) got %d", got)
	}
	if got := ToInt(float64(20), 0); got != 20 {
		t.Errorf("ToInt(float64) got %d", got)
	}
	if got := ToInt(int64(30), 0); got != 30 {
		t.Errorf("ToInt(int64) got %d", got)
	}
	if got := ToInt("40", 0); got != 40 {
		t.Errorf("ToInt(string) got %d", got)
	}
	if got := ToInt("invalid", 99); got != 99 {
		t.Errorf("ToInt(invalid) got %d", got)
	}
}

func TestToMap(t *testing.T) {
	m := map[string]any{"k": "v"}
	if got := ToMap(m); got == nil || got["k"] != "v" {
		t.Errorf("ToMap failed: %v", got)
	}
	if got := ToMap("not a map"); got != nil {
		t.Errorf("ToMap(non-map) got %v, want nil", got)
	}
}

func TestToString(t *testing.T) {
	if got := ToString(nil); got != "" {
		t.Errorf("ToString(nil) got %q", got)
	}
	if got := ToString("abc"); got != "abc" {
		t.Errorf("ToString(string) got %q", got)
	}
	if got := ToString(123); got != "123" {
		t.Errorf("ToString(int) got %q", got)
	}
}

func TestReqID(t *testing.T) {
	id1 := ReqID()
	id2 := ReqID()
	if len(id1) != 24 || len(id2) != 24 {
		t.Errorf("ReqID length mismatch: len(id1)=%d, len(id2)=%d", len(id1), len(id2))
	}
	if id1 == id2 {
		t.Errorf("ReqID produced duplicate: %q == %q", id1, id2)
	}
}

func TestCasing(t *testing.T) {
	if got := SnakeToCamel("hello_world"); got != "helloWorld" {
		t.Errorf("SnakeToCamel got %q", got)
	}
	if got := SnakeToCamel("alreadyCamel"); got != "alreadyCamel" {
		t.Errorf("SnakeToCamel got %q", got)
	}
	if got := CamelToSnake("helloWorld"); got != "hello_world" {
		t.Errorf("CamelToSnake got %q", got)
	}
}
