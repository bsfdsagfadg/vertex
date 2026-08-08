package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestScheduledProxyProbeAutoDisablesAfterConsecutiveFailures(t *testing.T) {
	setupEntryProxyTest(t, `{"entry_proxy_probe_cooldown_seconds":120}`)
	uri := "socks5://127.0.0.1:1180#auto-disable"
	if _, err := AddProxyCandidate(uri); err != nil {
		t.Fatal(err)
	}

	for failure := 1; failure <= 9; failure++ {
		disabled, err := UpdateProxyCandidateProbeResult(uri, false, 10, "timeout", 120, true, 10)
		if err != nil {
			t.Fatal(err)
		}
		if disabled {
			t.Fatalf("candidate disabled after %d failures, want 10", failure)
		}
		candidate := ListProxyCandidates()[0]
		if candidate.Disabled || candidate.ConsecutiveFailures != failure || candidate.CooldownUntil <= time.Now().Unix() {
			t.Fatalf("unexpected failure state after %d probes: %+v", failure, candidate)
		}
	}

	disabled, err := UpdateProxyCandidateProbeResult(uri, false, 10, "timeout", 120, true, 10)
	if err != nil || !disabled {
		t.Fatalf("tenth failure should auto-disable: disabled=%v err=%v", disabled, err)
	}
	candidate := ListProxyCandidates()[0]
	if !candidate.Disabled || candidate.ConsecutiveFailures != 10 || candidate.CooldownUntil != 0 {
		t.Fatalf("unexpected auto-disabled state: %+v", candidate)
	}
}

func TestSuccessfulProxyProbeClearsConsecutiveFailures(t *testing.T) {
	setupEntryProxyTest(t, `{"entry_proxy_probe_cooldown_seconds":120}`)
	uri := "socks5://127.0.0.1:1181#recovery"
	if _, err := AddProxyCandidate(uri); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		if _, err := UpdateProxyCandidateProbeResult(uri, false, 10, "timeout", 120, true, 10); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := UpdateProxyCandidateProbeResult(uri, true, 8, "", 120, true, 10); err != nil {
		t.Fatal(err)
	}
	candidate := ListProxyCandidates()[0]
	if candidate.ConsecutiveFailures != 0 || candidate.CooldownUntil != 0 || !candidate.LastTestOK {
		t.Fatalf("successful probe did not reset failure state: %+v", candidate)
	}
}

func TestScheduledProxyProbeDoesNotDisableWhenPolicyOff(t *testing.T) {
	setupEntryProxyTest(t, `{}`)
	uri := "socks5://127.0.0.1:1183#no-auto-disable"
	if _, err := AddProxyCandidate(uri); err != nil {
		t.Fatal(err)
	}
	for range 12 {
		if disabled, err := UpdateProxyCandidateProbeResult(uri, false, 10, "timeout", 60, false, 10); err != nil || disabled {
			t.Fatalf("disabled with policy off: disabled=%v err=%v", disabled, err)
		}
	}
	candidate := ListProxyCandidates()[0]
	if candidate.Disabled || candidate.ConsecutiveFailures != 12 {
		t.Fatalf("unexpected policy-off state: %+v", candidate)
	}
}

func TestManualProxyFailureDoesNotCountTowardAutoDisable(t *testing.T) {
	setupEntryProxyTest(t, `{"entry_proxy_probe_cooldown_seconds":90}`)
	uri := "socks5://127.0.0.1:1182#manual"
	if _, err := AddProxyCandidate(uri); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateProxyCandidateProbeResult(uri, false, 10, "scheduled", 90, true, 10); err != nil {
		t.Fatal(err)
	}
	if err := UpdateProxyCandidateTest(uri, false, 11, "manual"); err != nil {
		t.Fatal(err)
	}
	candidate := ListProxyCandidates()[0]
	remaining := candidate.CooldownUntil - time.Now().Unix()
	if candidate.ConsecutiveFailures != 1 || candidate.Disabled || remaining < 88 || remaining > 90 {
		t.Fatalf("manual failure changed auto-disable state: %+v remaining=%d", candidate, remaining)
	}
	if err := UpdateProxyCandidateTest(uri, true, 8, ""); err != nil {
		t.Fatal(err)
	}
	candidate = ListProxyCandidates()[0]
	if candidate.ConsecutiveFailures != 0 || candidate.CooldownUntil != 0 {
		t.Fatalf("manual success did not clear failure state: %+v", candidate)
	}
	if _, err := UpdateProxyCandidateProbeResult(uri, false, 10, "scheduled", 90, true, 10); err != nil {
		t.Fatal(err)
	}
	if err := SetProxyCandidateEnabled(uri, true); err != nil {
		t.Fatal(err)
	}
	candidate = ListProxyCandidates()[0]
	if candidate.ConsecutiveFailures != 0 || candidate.CooldownUntil != 0 || candidate.Disabled {
		t.Fatalf("manual enable did not clear failure state: %+v", candidate)
	}
}
