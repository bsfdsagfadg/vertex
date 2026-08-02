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
