package config

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/repository"
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
// Legacy proxy_url is migration input only and is never a runtime fallback.
func SelectEntryProxySequence(count int, cfg ConfigProvider) []string {
	if cfg == nil || !cfg.GlobalProxyEnabled() {
		return nil
	}
	items := ListProxyCandidates()
	now := time.Now().Unix()
	eligible := make([]ProxyCandidate, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.RawURI) == "" || item.Disabled || item.CooldownUntil > now {
			continue
		}
		item.RawURI = strings.TrimSpace(item.RawURI)
		eligible = append(eligible, item)
	}
	if strings.EqualFold(strings.TrimSpace(cfg.GlobalProxySelection()), "health") {
		sort.SliceStable(eligible, func(i, j int) bool {
			left, right := eligible[i], eligible[j]
			if left.Pinned != right.Pinned {
				return left.Pinned
			}
			if left.LastTestOK != right.LastTestOK {
				return left.LastTestOK
			}
			if left.ConsecutiveFailures != right.ConsecutiveFailures {
				return left.ConsecutiveFailures < right.ConsecutiveFailures
			}
			leftLatency, rightLatency := left.LastTestMs, right.LastTestMs
			if leftLatency <= 0 {
				leftLatency = 1e18
			}
			if rightLatency <= 0 {
				rightLatency = 1e18
			}
			return leftLatency < rightLatency
		})
	}
	if len(eligible) == 0 || count <= 0 {
		return nil
	}
	start := entryProxyCursor.Add(1) - 1
	sequence := make([]string, count)
	for i := range sequence {
		sequence[i] = eligible[(start+uint64(i))%uint64(len(eligible))].RawURI
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
	identity, _, err := globalProxyIdentity(rawURI)
	if err != nil {
		return err
	}
	store, err := candidateStore()
	if err != nil {
		return err
	}
	records, err := store.ListGlobalProxies(context.Background())
	if err != nil {
		return fmt.Errorf("读取全局代理状态: %w", err)
	}
	var record *repository.GlobalProxyRecord
	for index := range records {
		if records[index].CanonicalIdentity == identity.SemanticFingerprint {
			record = &records[index]
			break
		}
	}
	if record == nil {
		return fmt.Errorf("未找到该候选 URI")
	}
	if enabled {
		err = store.UpdateGlobalProxyHealth(context.Background(), repository.GlobalProxyHealth{
			GlobalProxyID: record.ID, LastTestOK: record.LastTestOK, LastTestMS: record.LastTestMS,
			LastTestAt: record.LastTestAt, LastTestError: record.LastTestError,
		}, false)
	} else {
		err = store.SetGlobalProxyDisabled(context.Background(), identity.SemanticFingerprint, true)
	}
	if err != nil {
		return fmt.Errorf("更新全局代理状态: %w", err)
	}
	InvalidateCandidateCache()
	return nil
}
