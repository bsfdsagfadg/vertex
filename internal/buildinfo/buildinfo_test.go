package buildinfo

import "testing"

func TestResolveUsesEmbeddedFallbackAndVCS(t *testing.T) {
	got, err := resolve("2.0.0-dev+local", "", "", "", "", vcsInfo{commit: "1234567890abcdef", dirty: true})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Version != "2.0.0-dev+local" || got.Commit != "1234567890ab" || !got.Dirty || got.Source != "local" {
		t.Fatalf("unexpected build info: %+v", got)
	}
}

func TestResolveReleaseOverridesVCS(t *testing.T) {
	got, err := resolve("2.0.0-dev+local", "v2.1.0", "abcdef0", "2026-08-20T00:00:00Z", "release", vcsInfo{commit: "ignored", dirty: true})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Version != "2.1.0" || got.Commit != "abcdef0" || got.BuildTime == "" || got.Source != "release" {
		t.Fatalf("unexpected build info: %+v", got)
	}
}

func TestResolveRejectsNonSemVer(t *testing.T) {
	for _, version := range []string{"", "dev", "unknown", "2.0", "02.0.0", "2.0.0-"} {
		t.Run(version, func(t *testing.T) {
			if _, err := resolve("2.0.0-dev+local", version, "", "", "release", vcsInfo{}); err == nil && version != "" {
				t.Fatalf("expected %q to be rejected", version)
			}
		})
	}
}

func TestCurrentAlwaysHasNumericVersion(t *testing.T) {
	got := Current()
	if !validSemVer(got.Version) {
		t.Fatalf("Current().Version=%q is not SemVer", got.Version)
	}
}
