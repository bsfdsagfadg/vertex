// Package buildinfo provides the single authoritative build identity used by
// the HTTP API, TUI, startup logs, and release tooling.
package buildinfo

import (
	_ "embed"
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

// These values are populated by release and Docker builds via -ldflags -X.
// They intentionally live in this package so every presentation surface reads
// the same resolved BuildInfo value.
var (
	ReleaseVersion   string
	ReleaseCommit    string
	ReleaseBuildTime string
	ReleaseSource    string
)

//go:embed version.txt
var embeddedVersion string

// BuildInfo is the canonical, safe-to-publish build identity.
type BuildInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
	Dirty     bool   `json:"dirty"`
	Source    string `json:"source"`
	GoVersion string `json:"go_version"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
}

type vcsInfo struct {
	commit string
	dirty  bool
}

// Current resolves release injection first, then local VCS metadata, with the
// embedded numeric SemVer as an offline-safe version fallback.
func Current() BuildInfo {
	info, err := resolve(
		strings.TrimSpace(embeddedVersion),
		ReleaseVersion,
		ReleaseCommit,
		ReleaseBuildTime,
		ReleaseSource,
		readVCSInfo(),
	)
	if err != nil {
		panic(err)
	}
	return info
}

func resolve(baseVersion, releaseVersion, releaseCommit, releaseBuildTime, releaseSource string, vcs vcsInfo) (BuildInfo, error) {
	baseVersion = normalizeVersion(baseVersion)
	if !validSemVer(baseVersion) {
		return BuildInfo{}, fmt.Errorf("embedded build version %q is not numeric SemVer", baseVersion)
	}

	version := normalizeVersion(releaseVersion)
	if version == "" {
		version = baseVersion
	} else if !validSemVer(version) {
		return BuildInfo{}, fmt.Errorf("injected build version %q is not numeric SemVer", releaseVersion)
	}

	commit := normalizeOptional(releaseCommit)
	if commit == "" {
		commit = vcs.commit
	}
	commit = shortCommit(commit)

	source := strings.ToLower(strings.TrimSpace(releaseSource))
	if source == "" {
		if strings.TrimSpace(releaseVersion) != "" {
			source = "release"
		} else {
			source = "local"
		}
	}
	if source != "release" && source != "docker" && source != "local" {
		return BuildInfo{}, fmt.Errorf("invalid build source %q", releaseSource)
	}

	return BuildInfo{
		Version:   version,
		Commit:    commit,
		BuildTime: normalizeOptional(releaseBuildTime),
		Dirty:     vcs.dirty,
		Source:    source,
		GoVersion: runtime.Version(),
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
	}, nil
}

func readVCSInfo() vcsInfo {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return vcsInfo{}
	}
	var out vcsInfo
	for _, setting := range bi.Settings {
		switch setting.Key {
		case "vcs.revision":
			out.commit = strings.TrimSpace(setting.Value)
		case "vcs.modified":
			out.dirty = setting.Value == "true"
		}
	}
	return out
}

func normalizeVersion(version string) string {
	version = strings.TrimSpace(version)
	if len(version) > 1 && (version[0] == 'v' || version[0] == 'V') {
		version = version[1:]
	}
	return version
}

func normalizeOptional(value string) string {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, "unknown") || strings.EqualFold(value, "dev") {
		return ""
	}
	return value
}

func shortCommit(commit string) string {
	commit = strings.TrimSpace(commit)
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

func validSemVer(version string) bool {
	coreAndPre := version
	if plus := strings.IndexByte(coreAndPre, '+'); plus >= 0 {
		if plus == len(coreAndPre)-1 || !validIdentifiers(coreAndPre[plus+1:], true) {
			return false
		}
		coreAndPre = coreAndPre[:plus]
	}
	core := coreAndPre
	if dash := strings.IndexByte(coreAndPre, '-'); dash >= 0 {
		if dash == len(coreAndPre)-1 || !validIdentifiers(coreAndPre[dash+1:], false) {
			return false
		}
		core = coreAndPre[:dash]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if !validNumericIdentifier(part) {
			return false
		}
	}
	return true
}

func validIdentifiers(value string, allowNumericLeadingZero bool) bool {
	for _, part := range strings.Split(value, ".") {
		if part == "" {
			return false
		}
		allNumeric := true
		for _, r := range part {
			if (r < '0' || r > '9') && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && r != '-' {
				return false
			}
			if r < '0' || r > '9' {
				allNumeric = false
			}
		}
		if !allowNumericLeadingZero && allNumeric && len(part) > 1 && part[0] == '0' {
			return false
		}
	}
	return true
}

func validNumericIdentifier(value string) bool {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
