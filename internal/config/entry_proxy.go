package config

import (
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

var entryProxyCursor atomic.Uint64 //nolint:gochecknoglobals

// NormalizeProxyURI returns the stable identity used for entry-proxy deduplication.
// Fragment names are labels, not part of the dial target.
func NormalizeProxyURI(rawURI string) (string, error) {
	rawURI = strings.TrimSpace(rawURI)
	parsed, err := url.Parse(rawURI)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("URI 格式无效")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if !hasCaseSensitiveProxyPayload(parsed.Scheme) {
		parsed.Host = strings.ToLower(parsed.Host)
	}
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String(), nil
}

func hasCaseSensitiveProxyPayload(scheme string) bool {
	switch scheme {
	case "vmess", "clash", "ssr", "shadowsocksr":
		return true
	default:
		return false
	}
}

// SelectEntryProxySequence reserves a stable round-robin sequence for one request.
// An empty result means that the configured entry pool has no eligible entries.
// When no database candidates exist, the legacy proxy_url remains a single fallback.
func SelectEntryProxySequence(count int, cfg ConfigProvider) []string {
	items := ListProxyCandidates()
	now := time.Now().Unix()
	eligible := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.RawURI) == "" || item.Disabled || item.CooldownUntil > now {
			continue
		}
		eligible = append(eligible, strings.TrimSpace(item.RawURI))
	}
	if len(eligible) == 0 && len(items) == 0 && cfg != nil {
		if legacy := strings.TrimSpace(cfg.ProxyURL()); legacy != "" {
			eligible = append(eligible, legacy)
		}
	}
	if len(eligible) == 0 || count <= 0 {
		return nil
	}
	start := entryProxyCursor.Add(uint64(count)) - uint64(count)
	sequence := make([]string, count)
	for i := range sequence {
		sequence[i] = eligible[(start+uint64(i))%uint64(len(eligible))]
	}
	return sequence
}

// SelectEntryProxy selects one enabled, non-cooling entry in stable database order.
func SelectEntryProxy(cfg ConfigProvider) string {
	sequence := SelectEntryProxySequence(1, cfg)
	if len(sequence) == 0 {
		return ""
	}
	return sequence[0]
}

// MarkEntryProxyFailure excludes an entry for the transient 60-second cooldown.
func MarkEntryProxyFailure(rawURI, errText string) error {
	return updateEntryProxyTest(rawURI, false, 0, errText)
}

// MarkEntryProxySuccess clears a transient entry cooldown without changing manual state.
func MarkEntryProxySuccess(rawURI string) error {
	return updateEntryProxyTest(rawURI, true, 0, "")
}

func updateEntryProxyTest(rawURI string, success bool, elapsedMs float64, errText string) error {
	return UpdateProxyCandidateTest(rawURI, success, elapsedMs, errText)
}

func SetProxyCandidateEnabled(rawURI string, enabled bool) error {
	normalized, err := NormalizeProxyURI(rawURI)
	if err != nil {
		return err
	}
	store, err := candidateStore()
	if err != nil {
		return err
	}
	result, err := store.Exec("UPDATE entry_proxy_candidates SET disabled = ? WHERE normalized_uri = ?", !enabled, normalized)
	if err != nil {
		return fmt.Errorf("更新入口代理状态: %w", err)
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return fmt.Errorf("未找到该候选 URI")
	}
	return nil
}
