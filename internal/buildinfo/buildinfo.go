// Package buildinfo provides the single sanitized build metadata representation
// shared by HTTP and UI surfaces.
package buildinfo

import (
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
)

var (
	mu          sync.RWMutex
	injectedVer = ""
	injectedSHA = ""
	injectedAt  = ""
	injectedSrc = ""
)

// SetInjected records release metadata supplied by linker flags. Empty values
// are resolved from Go's build info or safe fallbacks.
func SetInjected(version, commit, buildTime, source string) {
	mu.Lock()
	injectedVer, injectedSHA, injectedAt, injectedSrc = version, commit, buildTime, source
	mu.Unlock()
}

type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
	Dirty     bool   `json:"dirty"`
	Source    string `json:"source"`
	GoVersion string `json:"go_version"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
}

func Current() Info {
	mu.RLock()
	ver, sha, at, src := injectedVer, injectedSHA, injectedAt, injectedSrc
	mu.RUnlock()
	if ver == "" || ver == "dev" {
		ver = "0.0.0-dev"
	}
	if sha == "" || sha == "unknown" {
		sha = "unknown"
	}
	dirty := false
	if bi, ok := debug.ReadBuildInfo(); ok {
		if sha == "unknown" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			sha = bi.Main.Version
		}
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if sha == "unknown" {
					sha = s.Value
				}
			case "vcs.time":
				if at == "" {
					at = s.Value
				}
			case "vcs.modified":
				dirty = s.Value == "true"
			}
		}
	}
	if len(sha) > 12 {
		sha = sha[:12]
	}
	if src == "" {
		src = "local"
	}
	if src == "release" || src == "docker" {
		dirty = false
	}
	return Info{Version: sanitizeVersion(ver), Commit: sha, BuildTime: at, Dirty: dirty, Source: src, GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
}

func sanitizeVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "0.0.0-dev"
	}
	if strings.HasPrefix(v, "v") {
		v = v[1:]
	}
	if len(v) > 64 {
		return "0.0.0-dev"
	}
	for _, r := range v {
		if !(r == '.' || r == '-' || r == '+' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z') {
			return "0.0.0-dev"
		}
	}
	base := v
	if i := strings.IndexAny(base, "-+"); i >= 0 {
		base = base[:i]
	}
	parts := strings.Split(base, ".")
	if len(parts) != 3 {
		return "0.0.0-dev"
	}
	for _, p := range parts {
		if p == "" {
			return "0.0.0-dev"
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return "0.0.0-dev"
			}
		}
	}
	return v
}
