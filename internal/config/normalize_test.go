package config

import "testing"

func TestNormalizeImageSizeTier(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"2K uppercase", "2K", "2K"},
		{"2k lowercase", "2k", "2K"},
		{"1K", "1K", "1K"},
		{"4K", "4K", "4K"},
		{"512", "512", "512"},
		{"8K invalid", "8K", ""},
		{"empty string", "", ""},
		{"whitespace around", "  2K  ", "2K"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := normalizeImageSizeTier(c.in); got != c.want {
				t.Errorf("normalizeImageSizeTier(%q)=%q，期望 %q", c.in, got, c.want)
			}
		})
	}
}
