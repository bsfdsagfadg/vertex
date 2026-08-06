package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProxyCandidateLifecycleAndActiveRemoval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("VPROXY_CONFIG", path)
	if err := os.WriteFile(path, []byte(`{"proxy_url":""}`), 0o600); err != nil {
		t.Fatal(err)
	}
	InvalidateCache()
	t.Cleanup(InvalidateCache)

	uri := "socks5://user:pass@127.0.0.1:1080#%E5%85%A5%E5%8F%A3"
	candidate, err := AddProxyCandidate(uri)
	if err != nil {
		t.Fatalf("add candidate: %v", err)
	}
	if candidate.Name != "入口" || candidate.Type != "socks5" {
		t.Fatalf("unexpected candidate: %+v", candidate)
	}
	if _, err := AddProxyCandidate(uri); err == nil {
		t.Fatal("duplicate candidate should fail")
	}
	if err := UpdateProxyCandidateTest(uri, true, 12.5, ""); err != nil {
		t.Fatalf("update test result: %v", err)
	}
	loaded := Load()
	if len(loaded.ProxyURLCandidates) != 1 || !loaded.ProxyURLCandidates[0].LastTestOK {
		t.Fatalf("test result was not persisted: %+v", loaded.ProxyURLCandidates)
	}
	if err := WriteSettings(map[string]any{"proxy_url": uri}); err != nil {
		t.Fatalf("activate candidate: %v", err)
	}
	wasActive, err := RemoveProxyCandidate(uri)
	if err != nil || !wasActive {
		t.Fatalf("remove active candidate: active=%v err=%v", wasActive, err)
	}
	loaded = Load()
	if loaded.ProxyURL != "" || len(loaded.ProxyURLCandidates) != 0 {
		t.Fatalf("active candidate removal did not clear config: %+v", loaded)
	}
}

func TestProxyCandidateReadsLegacyVMessName(t *testing.T) {
	// {"ps":"legacy-name"}
	uri := "vmess://eyJwcyI6ImxlZ2FjeS1uYW1lIn0="
	if got := extractProxyCandidateName(uri); got != "legacy-name" {
		t.Fatalf("unexpected VMess name %q", got)
	}
}

func TestProxyCandidatePoolManagement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("VPROXY_CONFIG", path)
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	InvalidateCache()
	t.Cleanup(InvalidateCache)

	for _, uri := range []string{
		"socks5://u1@h1:1080", "socks5://u2@h2:1080", "socks5://u3@h3:1080",
	} {
		if _, err := AddProxyCandidate(uri); err != nil {
			t.Fatalf("add candidate %s: %v", uri, err)
		}
	}

	if err := SetProxyCandidatesDisabled([]string{"socks5://u1@h1:1080"}, true); err != nil {
		t.Fatalf("disable candidate: %v", err)
	}
	loaded := Load()
	foundDisabled := false
	for _, c := range loaded.ProxyURLCandidates {
		if c.RawURI == "socks5://u1@h1:1080" {
			foundDisabled = c.Disabled
		}
	}
	if !foundDisabled {
		t.Fatal("disabled flag was not persisted")
	}
	if err := SetProxyCandidatesDisabled([]string{"socks5://u1@h1:1080"}, false); err != nil {
		t.Fatalf("re-enable candidate: %v", err)
	}
	loaded = Load()
	for _, c := range loaded.ProxyURLCandidates {
		if c.RawURI == "socks5://u1@h1:1080" && c.Disabled {
			t.Fatal("candidate was not re-enabled")
		}
	}

	if err := BatchRemoveProxyCandidates([]string{"socks5://u2@h2:1080"}); err != nil {
		t.Fatalf("batch remove: %v", err)
	}
	if got := len(GetProxyCandidates()); got != 2 {
		t.Fatalf("expected 2 candidates after removal, got %d", got)
	}

	// 网络类失败自动禁用。
	if err := UpdateProxyCandidateTest("socks5://u3@h3:1080", false, 5000, "dial tcp: i/o timeout"); err != nil {
		t.Fatalf("update failing test: %v", err)
	}
	loaded = Load()
	for _, c := range loaded.ProxyURLCandidates {
		if c.RawURI == "socks5://u3@h3:1080" && !c.Disabled {
			t.Fatal("network failure should auto-disable candidate")
		}
	}

	// 清空禁用。
	n, err := RemoveDisabledProxyCandidates()
	if err != nil || n != 1 {
		t.Fatalf("remove disabled: n=%d err=%v", n, err)
	}
	if got := len(GetProxyCandidates()); got != 1 {
		t.Fatalf("expected 1 candidate after clearing disabled, got %d", got)
	}

	// 去重（手工写入重复 URI 后去重应移除冗余）。
	if err := WriteSettings(map[string]any{"proxy_url_candidates": append(GetProxyCandidates(), GetProxyCandidates()[0])}); err != nil {
		t.Fatalf("seed duplicate: %v", err)
	}
removed, err := DedupProxyCandidates()
	if err != nil || removed != 1 {
		t.Fatalf("dedup: removed=%d err=%v", removed, err)
	}
}
