package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadNormalizesAndPersistsTimeouts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("VPROXY_CONFIG", path)
	t.Cleanup(InvalidateCache)

	initial := `{"request_timeout":9999,"race_timeout":9999,"stream_idle_timeout_seconds":0,"parallel_pool_size":99,"custom_field":"preserved"}`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	InvalidateCache()

	cfg := Load()
	if cfg.RequestTimeout != maxTimeoutSeconds {
		t.Fatalf("request_timeout=%d, want %d", cfg.RequestTimeout, maxTimeoutSeconds)
	}
	if cfg.RaceTimeout != maxTimeoutSeconds {
		t.Fatalf("race_timeout=%d, want %d", cfg.RaceTimeout, maxTimeoutSeconds)
	}
	if cfg.StreamIdleTimeoutSeconds != 30 {
		t.Fatalf("stream_idle_timeout_seconds=%d, want 30", cfg.StreamIdleTimeoutSeconds)
	}
	if cfg.ParallelPoolSize != 20 {
		t.Fatalf("parallel_pool_size=%d, want 20", cfg.ParallelPoolSize)
	}

	raw := map[string]any{}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["request_timeout"] != float64(maxTimeoutSeconds) || raw["race_timeout"] != float64(maxTimeoutSeconds) {
		t.Fatalf("规范化超时未写回: %#v", raw)
	}
	if raw["stream_idle_timeout_seconds"] != float64(30) || raw["parallel_pool_size"] != float64(20) {
		t.Fatalf("规范化空闲超时/并发数未写回: %#v", raw)
	}
	if raw["custom_field"] != "preserved" {
		t.Fatalf("未知字段未保留: %#v", raw)
	}
}

func TestLoadNormalizesNegativeTimeouts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("VPROXY_CONFIG", path)
	t.Cleanup(InvalidateCache)
	if err := os.WriteFile(path, []byte(`{"request_timeout":-1,"race_timeout":-1,"stream_idle_timeout_seconds":-1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	InvalidateCache()

	cfg := Load()
	if cfg.RequestTimeout != 180 || cfg.RaceTimeout != 0 || cfg.StreamIdleTimeoutSeconds != 30 {
		t.Fatalf("负值规范化错误: request=%d race=%d idle=%d", cfg.RequestTimeout, cfg.RaceTimeout, cfg.StreamIdleTimeoutSeconds)
	}
}
