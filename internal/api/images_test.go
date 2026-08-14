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
