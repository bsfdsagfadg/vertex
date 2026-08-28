package buildinfo

import "testing"

func TestInjectedReleaseIsSanitizedAndNotDirty(t *testing.T) {
	SetInjected("v1.2.3", "0123456789abcdef", "2026-01-01T00:00:00Z", "release")
	got := Current()
	if got.Version != "1.2.3" || got.Commit != "0123456789ab" || got.Dirty {
		t.Fatalf("unexpected release info: %+v", got)
	}
}

func TestInvalidVersionFallsBack(t *testing.T) {
	SetInjected("not a version", "", "", "local")
	if got := Current().Version; got != "0.0.0-dev" {
		t.Fatalf("version=%q", got)
	}
}
