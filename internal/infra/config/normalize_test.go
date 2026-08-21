package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

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

func TestDefaultConfig_NewFields(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.DefaultResponseModalities != "图文" {
		t.Errorf("DefaultResponseModalities=%q, want 图文", cfg.DefaultResponseModalities)
	}
	if cfg.StreamIdleTimeoutSeconds != 20 {
		t.Errorf("StreamIdleTimeoutSeconds=%d, want 20", cfg.StreamIdleTimeoutSeconds)
	}
}

func TestLoad_DefaultResponseModalitiesNormalize(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"空值回退图文", "", "图文"},
		{"仅图片保留", "仅图片", "仅图片"},
		{"图文保留", "图文", "图文"},
		{"非法值回退图文", "INVALID", "图文"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := t.TempDir()
			origCached := cached
			origCacheTime := cacheTime
			t.Cleanup(func() {
				cached = origCached
				cacheTime = origCacheTime
			})

			_ = os.Setenv("VPROXY_CONFIG", filepath.Join(d, "config.json"))
			t.Cleanup(func() { _ = os.Unsetenv("VPROXY_CONFIG") })

			data, _ := json.Marshal(AppConfig{DefaultResponseModalities: tt.input})
			_ = os.WriteFile(filepath.Join(d, "config.json"), data, 0o644)
			InvalidateCache()
			cfg := Load()
			if cfg.DefaultResponseModalities != tt.expected {
				t.Errorf("got %q, want %q", cfg.DefaultResponseModalities, tt.expected)
			}
		})
	}
}

func TestLoad_StreamIdleTimeoutSecondsNormalize(t *testing.T) {
	tests := []struct {
		name     string
		input    int
		expected int
	}{
		{"zero falls back to 20", 0, 20},
		{"negative falls back to 20", -5, 20},
		{"positive preserved", 30, 30},
		{"default 20 preserved", 20, 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := t.TempDir()
			origCached := cached
			origCacheTime := cacheTime
			t.Cleanup(func() {
				cached = origCached
				cacheTime = origCacheTime
			})

			_ = os.Setenv("VPROXY_CONFIG", filepath.Join(d, "config.json"))
			t.Cleanup(func() { _ = os.Unsetenv("VPROXY_CONFIG") })

			data, _ := json.Marshal(AppConfig{StreamIdleTimeoutSeconds: tt.input})
			_ = os.WriteFile(filepath.Join(d, "config.json"), data, 0o644)
			InvalidateCache()
			cfg := Load()
			if cfg.StreamIdleTimeoutSeconds != tt.expected {
				t.Errorf("got %d, want %d", cfg.StreamIdleTimeoutSeconds, tt.expected)
			}
		})
	}
}
