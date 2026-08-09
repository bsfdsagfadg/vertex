package api

import "testing"

// ---- coerceOAIN：clamp [1,8]，非法 → 1 ----

func TestCoerceOAIN(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"valid mid", "3", 3},
		{"min", "1", 1},
		{"max", "8", 8},
		{"below clamps to 1", "0", 1},
		{"negative clamps to 1", "-5", 1},
		{"above clamps to 8", "9", 8},
		{"far above clamps to 8", "1000", 8},
		{"empty to 1", "", 1},
		{"non-numeric to 1", "abc", 1},
		{"whitespace trimmed", "  4  ", 4},
		{"float string to 1 (atoi fails)", "2.5", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := coerceOAIN(c.in); got != c.want {
				t.Errorf("coerceOAIN(%q)=%d，期望 %d", c.in, got, c.want)
			}
		})
	}
}

// ---- stringValOr：缺失/非字符串 → default，存在字符串（含空串）→ 原样 ----

func TestGetStr(t *testing.T) {
	body := map[string]any{
		"present":   "value",
		"empty":     "",
		"number":    42,
		"boolean":   true,
		"nilval":    nil,
		"nestedmap": map[string]any{"x": 1},
	}
	cases := []struct {
		name string
		key  string
		def  string
		want string
	}{
		{"present string returned", "present", "DEF", "value"},
		{"empty string returned as-is", "empty", "DEF", ""},
		{"missing returns default", "missing", "DEF", "DEF"},
		{"non-string number returns default", "number", "DEF", "DEF"},
		{"non-string bool returns default", "boolean", "DEF", "DEF"},
		{"nil value returns default", "nilval", "DEF", "DEF"},
		{"map value returns default", "nestedmap", "DEF", "DEF"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := stringValOr(body, c.key, c.def); got != c.want {
				t.Errorf("stringValOr(%q, %q)=%q，期望 %q", c.key, c.def, got, c.want)
			}
		})
	}
}

// ---- firstNonEmptyStr ----

func TestFirstNonEmptyStr(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want string
	}{
		{"a non-empty wins", "first", "second", "first"},
		{"a empty falls to b", "", "second", "second"},
		{"a whitespace falls to b", "   ", "second", "second"},
		{"both empty returns b", "", "", ""},
		{"a wins even if b empty", "first", "", "first"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := firstNonEmptyStr(c.a, c.b); got != c.want {
				t.Errorf("firstNonEmptyStr(%q,%q)=%q，期望 %q", c.a, c.b, got, c.want)
			}
		})
	}
}

// ---- hasImageSize ----

func TestHasImageSize(t *testing.T) {
	cases := []struct { //nolint:govet
		name    string
		payload map[string]any
		want    bool
	}{
		{
			name: "set non-empty",
			payload: map[string]any{
				"generationConfig": map[string]any{
					"imageConfig": map[string]any{"imageSize": "1K"},
				},
			},
			want: true,
		},
		{
			name: "imageSize empty string",
			payload: map[string]any{
				"generationConfig": map[string]any{
					"imageConfig": map[string]any{"imageSize": ""},
				},
			},
			want: false,
		},
		{
			name: "imageSize missing key",
			payload: map[string]any{
				"generationConfig": map[string]any{
					"imageConfig": map[string]any{},
				},
			},
			want: false,
		},
		{
			name: "no imageConfig",
			payload: map[string]any{
				"generationConfig": map[string]any{},
			},
			want: false,
		},
		{
			name:    "no generationConfig",
			payload: map[string]any{},
			want:    false,
		},
		{
			name: "imageSize non-string value",
			payload: map[string]any{
				"generationConfig": map[string]any{
					"imageConfig": map[string]any{"imageSize": 1024},
				},
			},
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasImageSize(c.payload); got != c.want {
				t.Errorf("hasImageSize=%v，期望 %v", got, c.want)
			}
		})
	}
}
