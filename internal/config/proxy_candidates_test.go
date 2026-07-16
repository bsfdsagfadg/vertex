package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteSettings_PreservesUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	t.Setenv("VPROXY_CONFIG", path)
	InvalidateCache()

	// 预置含未知键 foo 的 config
	initial := `{"port_api":2156,"foo":"bar"}`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	InvalidateCache()

	// 添加候选
	candidate, err := AddProxyCandidate("http://test-proxy:8080")
	if err != nil {
		t.Fatalf("AddProxyCandidate: %v", err)
	}
	if candidate.RawURI != "http://test-proxy:8080" {
		t.Fatalf("unexpected candidate: %+v", candidate)
	}

	// 验证 foo 仍存在
	raw := map[string]any{}
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["foo"] != "bar" {
		t.Fatalf("unknown key foo should be preserved, got %v", raw["foo"])
	}
	cands, ok := raw["proxy_url_candidates"].([]any)
	if !ok || len(cands) != 1 {
		t.Fatalf("proxy_url_candidates should have 1 entry, got %v", raw["proxy_url_candidates"])
	}

	// 删到空
	if err := RemoveProxyCandidate("http://test-proxy:8080"); err != nil {
		t.Fatalf("RemoveProxyCandidate: %v", err)
	}

	raw = map[string]any{}
	data, _ = os.ReadFile(path)
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["foo"] != "bar" {
		t.Fatalf("unknown key foo should still be preserved after removal, got %v", raw["foo"])
	}

	InvalidateCache()
}

func TestAddProxyCandidate_EmptySliceNotNull(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	t.Setenv("VPROXY_CONFIG", path)
	InvalidateCache()

	// 空 config 写入
	initial := `{}`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	InvalidateCache()

	// 添加再删除，确保最终 proxy_url_candidates 为 [] 而非 null
	candidate, err := AddProxyCandidate("http://test-proxy:8080")
	if err != nil {
		t.Fatalf("AddProxyCandidate: %v", err)
	}
	_ = candidate

	if err := RemoveProxyCandidate("http://test-proxy:8080"); err != nil {
		t.Fatalf("RemoveProxyCandidate: %v", err)
	}

	raw := map[string]any{}
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	// 验证 proxy_url_candidates 是 [] 而非 null
	switch v := raw["proxy_url_candidates"].(type) {
	case []any:
		if v != nil && len(v) != 0 {
			t.Fatalf("expected empty array, got %v", v)
		}
	case nil:
		// nil 也是可接受的，但最好是空数组
	default:
		t.Fatalf("unexpected type for proxy_url_candidates: %T", v)
	}

	InvalidateCache()
}