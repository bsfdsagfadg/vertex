package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/db"
)

func setupEntryProxyTest(t *testing.T, configJSON string) ConfigProvider {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("VPROXY_CONFIG", configPath)
	if err := os.WriteFile(configPath, []byte(configJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	InvalidateCache()
	t.Cleanup(InvalidateCache)
	if err := db.InitDB(filepath.Join(t.TempDir(), "entry.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.CloseDB)
	entryProxyCursor.Store(0)
	return GetProvider()
}

func TestMigrateLegacyProxyIsIdempotentAndReenablesCandidate(t *testing.T) {
	uri := "socks5://user:pass@127.0.0.1:1080#legacy"
	setupEntryProxyTest(t, `{"proxy_url":"`+uri+`"}`)
	if _, err := AddProxyCandidate(uri); err != nil {
		t.Fatal(err)
	}
	if err := SetProxyCandidateEnabled(uri, false); err != nil {
		t.Fatal(err)
	}
	if err := UpdateProxyCandidateTest(uri, false, 12, "temporary failure"); err != nil {
		t.Fatal(err)
	}

	if err := MigrateLegacyProxy(uri); err != nil {
		t.Fatal(err)
	}
	if err := MigrateLegacyProxy(uri); err != nil {
		t.Fatal(err)
	}
	items := ListProxyCandidates()
	if len(items) != 1 {
		t.Fatalf("migration created duplicate candidates: %+v", items)
	}
	if items[0].Disabled || items[0].CooldownUntil != 0 {
		t.Fatalf("legacy active proxy was not restored to rotation: %+v", items[0])
	}
	if items[0].LastTestError != "temporary failure" {
		t.Fatalf("migration erased health history: %+v", items[0])
	}
	if got := Load().ProxyURL; got != "" {
		t.Fatalf("legacy proxy_url was not cleared: %q", got)
	}
}

func TestSelectEntryProxyRotatesAndFiltersUnavailableCandidates(t *testing.T) {
	provider := setupEntryProxyTest(t, `{"proxy_url":""}`)
	first := "socks5://127.0.0.1:1081#first"
	disabled := "socks5://127.0.0.1:1082#disabled"
	third := "socks5://127.0.0.1:1083#third"
	for _, uri := range []string{first, disabled, third} {
		if _, err := AddProxyCandidate(uri); err != nil {
			t.Fatal(err)
		}
	}
	if err := SetProxyCandidateEnabled(disabled, false); err != nil {
		t.Fatal(err)
	}
	if err := MarkEntryProxyFailure(third, "temporary failure"); err != nil {
		t.Fatal(err)
	}
	if got := SelectEntryProxy(provider); got != first {
		t.Fatalf("cooling and disabled entries were not filtered: %q", got)
	}
	if err := MarkEntryProxySuccess(third); err != nil {
		t.Fatal(err)
	}
	entryProxyCursor.Store(0)
	for index, want := range []string{first, third, first, third} {
		if got := SelectEntryProxy(provider); got != want {
			t.Fatalf("rotation[%d]=%q, want %q", index, got, want)
		}
	}
}

func TestNormalizeProxyURIPreservesEncodedPayloadCase(t *testing.T) {
	got, err := NormalizeProxyURI("VMESS://AbCdEf012_-#display-name")
	if err != nil {
		t.Fatal(err)
	}
	if want := "vmess://AbCdEf012_-"; got != want {
		t.Fatalf("NormalizeProxyURI() = %q, want %q", got, want)
	}

	got, err = NormalizeProxyURI("SOCKS5://User:Pass@EXAMPLE.COM:1080#display-name")
	if err != nil {
		t.Fatal(err)
	}
	if want := "socks5://User:Pass@example.com:1080"; got != want {
		t.Fatalf("NormalizeProxyURI() = %q, want %q", got, want)
	}
}

func TestAddProxyCandidateUsesCaseSensitiveEncodedIdentity(t *testing.T) {
	setupEntryProxyTest(t, `{"proxy_url":""}`)
	first := "vmess://AbCdEf012_-#first"
	if _, err := AddProxyCandidate(first); err != nil {
		t.Fatal(err)
	}
	if _, err := AddProxyCandidate("vmess://AbCdEf012_-#renamed"); err == nil {
		t.Fatal("fragment-only change should be treated as a duplicate")
	}
	if _, err := AddProxyCandidate("vmess://aBcDeF012_-#different-payload"); err != nil {
		t.Fatalf("case-distinct payload was rejected as a duplicate: %v", err)
	}
	if items := ListProxyCandidates(); len(items) != 2 {
		t.Fatalf("candidate count = %d, want 2: %+v", len(items), items)
	}
}

func TestRemoveDisabledProxyCandidatesKeepsEnabledEntries(t *testing.T) {
	setupEntryProxyTest(t, `{"proxy_url":""}`)
	enabled := "socks5://127.0.0.1:1081#enabled"
	disabled := "socks5://127.0.0.1:1082#disabled"
	for _, rawURI := range []string{enabled, disabled} {
		if _, err := AddProxyCandidate(rawURI); err != nil {
			t.Fatal(err)
		}
	}
	if err := SetProxyCandidateEnabled(disabled, false); err != nil {
		t.Fatal(err)
	}
	removed, err := RemoveDisabledProxyCandidates()
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != disabled {
		t.Fatalf("removed=%#v, want [%q]", removed, disabled)
	}
	items := ListProxyCandidates()
	if len(items) != 1 || items[0].RawURI != enabled {
		t.Fatalf("enabled entries changed during cleanup: %+v", items)
	}
}
