package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/db"
)

func TestProxyCandidateLifecycleAndActiveRemoval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("VPROXY_CONFIG", path)
	if err := os.WriteFile(path, []byte(`{"proxy_url":""}`), 0o600); err != nil {
		t.Fatal(err)
	}
	InvalidateCache()
	t.Cleanup(InvalidateCache)
	if err := db.InitDB(filepath.Join(t.TempDir(), "entry.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.CloseDB)

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
	loaded := ListProxyCandidates()
	if len(loaded) != 1 || !loaded[0].LastTestOK {
		t.Fatalf("test result was not persisted: %+v", loaded)
	}
	if err := WriteSettings(map[string]any{"proxy_url": uri}); err != nil {
		t.Fatalf("activate candidate: %v", err)
	}
	wasActive, err := RemoveProxyCandidate(uri)
	if err != nil || !wasActive {
		t.Fatalf("remove active candidate: active=%v err=%v", wasActive, err)
	}
	loaded = ListProxyCandidates()
	if Load().ProxyURL != "" || len(loaded) != 0 {
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
