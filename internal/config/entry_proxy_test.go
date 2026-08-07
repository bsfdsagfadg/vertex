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
