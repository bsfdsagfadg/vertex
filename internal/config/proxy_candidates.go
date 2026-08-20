package config

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/repository"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

type ProxyCandidate struct {
	ID                  string                         `json:"id" db:"id"`
	RawURI              string                         `json:"raw_uri" db:"raw_uri"`
	CanonicalIdentity   string                         `json:"canonical_identity" db:"canonical_identity"`
	EndpointFingerprint string                         `json:"endpoint_fingerprint" db:"endpoint_fingerprint"`
	Name                string                         `json:"name" db:"name"`
	Type                string                         `json:"type" db:"type"`
	Disabled            bool                           `json:"disabled" db:"disabled"`
	Pinned              bool                           `json:"pinned" db:"pinned"`
	Sources             []repository.GlobalProxySource `json:"sources,omitempty" db:"-"`
	CooldownUntil       int64                          `json:"cooldown_until" db:"cooldown_until"`
	LastTestOK          bool                           `json:"last_test_ok" db:"last_test_ok"`
	LastTestMs          float64                        `json:"last_test_ms" db:"last_test_ms"`
	LastTestAt          int64                          `json:"last_test_at" db:"last_test_at"`
	LastTestError       string                         `json:"last_test_error" db:"last_test_error"`
	ConsecutiveFailures int                            `json:"consecutive_failures" db:"consecutive_failures"`
}

//nolint:gochecknoglobals // Serializes candidate read-modify-write operations.
var (
	proxyCandidatesMu  sync.Mutex
	candidateCacheMu   sync.RWMutex
	cachedCandidates   []ProxyCandidate
	candidateCacheTime time.Time
	proxyRepository    *repository.SQLite
)

const candidateCacheTTL = 2 * time.Second

// InvalidateCandidateCache 清除候选代理列表缓存。
func InvalidateCandidateCache() {
	candidateCacheMu.Lock()
	cachedCandidates = nil
	candidateCacheMu.Unlock()
}

func SetRepository(repo *repository.SQLite) {
	proxyCandidatesMu.Lock()
	proxyRepository = repo
	InvalidateCandidateCache()
	proxyCandidatesMu.Unlock()
	subMu.Lock()
	globalSubConfig = SubscriptionConfig{}
	subscriptionsLoaded = false
	subMu.Unlock()
}

func candidateStore() (*repository.SQLite, error) {
	if proxyRepository == nil {
		return nil, fmt.Errorf("数据库尚未初始化")
	}
	return proxyRepository, nil
}

func ListProxyCandidates() []ProxyCandidate {
	candidateCacheMu.RLock()
	if cachedCandidates != nil && time.Since(candidateCacheTime) < candidateCacheTTL {
		res := make([]ProxyCandidate, len(cachedCandidates))
		copy(res, cachedCandidates)
		candidateCacheMu.RUnlock()
		return res
	}
	candidateCacheMu.RUnlock()

	store, err := candidateStore()
	if err != nil {
		return nil
	}
	records, err := store.ListGlobalProxies(context.Background())
	if err != nil {
		return nil
	}
	result := make([]ProxyCandidate, 0, len(records))
	for _, record := range records {
		result = append(result, ProxyCandidate{
			ID: record.ID, RawURI: record.RawURI, CanonicalIdentity: record.CanonicalIdentity,
			EndpointFingerprint: record.EndpointFingerprint, Name: record.Name, Type: record.Type,
			Disabled: record.Disabled, Pinned: record.Pinned, Sources: record.Sources,
			CooldownUntil: record.CooldownUntil, LastTestOK: record.LastTestOK,
			LastTestMs: record.LastTestMS, LastTestAt: record.LastTestAt,
			LastTestError: record.LastTestError, ConsecutiveFailures: record.ConsecutiveFailures,
		})
	}
	candidateCacheMu.Lock()
	cachedCandidates = make([]ProxyCandidate, len(result))
	copy(cachedCandidates, result)
	candidateCacheTime = time.Now()
	candidateCacheMu.Unlock()

	return result
}

func AddProxyCandidate(rawURI string) (ProxyCandidate, error) {
	proxyCandidatesMu.Lock()
	defer proxyCandidatesMu.Unlock()
	return upsertProxyCandidate(rawURI, "manual", "", false, true)
}

// UpsertProxyCandidateSource promotes or imports a GlobalProxy while retaining
// its role-specific provenance. Existing identities gain the new source rather
// than creating an alias row.
func UpsertProxyCandidateSource(rawURI, sourceType, sourceID string, pinned bool) (ProxyCandidate, error) {
	proxyCandidatesMu.Lock()
	defer proxyCandidatesMu.Unlock()
	return upsertProxyCandidate(rawURI, sourceType, sourceID, pinned, false)
}

func upsertProxyCandidate(rawURI, sourceType, sourceID string, pinned, rejectExisting bool) (ProxyCandidate, error) {
	rawURI = strings.TrimSpace(rawURI)
	if rawURI == "" {
		return ProxyCandidate{}, fmt.Errorf("URI 为空")
	}
	parsed, err := url.Parse(rawURI)
	if err != nil || parsed.Scheme == "" {
		return ProxyCandidate{}, fmt.Errorf("URI 格式无效")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if !supportedEntryProxyScheme(scheme) {
		return ProxyCandidate{}, fmt.Errorf("不支持的代理协议: %s", scheme)
	}
	store, err := candidateStore()
	if err != nil {
		return ProxyCandidate{}, err
	}
	identity, id, err := globalProxyIdentity(rawURI)
	if err != nil {
		return ProxyCandidate{}, err
	}
	if rejectExisting && hasGlobalProxyIdentity(identity.SemanticFingerprint) {
		return ProxyCandidate{}, fmt.Errorf("该 URI 已在候选列表中")
	}
	name := extractProxyCandidateName(rawURI)
	if name == "" {
		name = scheme + "://" + parsed.Host
	}
	candidate := ProxyCandidate{
		ID: id, RawURI: rawURI, CanonicalIdentity: identity.SemanticFingerprint,
		EndpointFingerprint: identity.EndpointFingerprint, Name: name, Type: scheme,
		Pinned: pinned,
	}
	err = store.UpsertGlobalProxy(context.Background(), repository.GlobalProxy{
		ID: id, RawURI: rawURI, CanonicalIdentity: identity.SemanticFingerprint,
		EndpointFingerprint: identity.EndpointFingerprint, Name: name, Type: scheme,
		Pinned: pinned,
	}, repository.GlobalProxySource{GlobalProxyID: id, SourceType: sourceType, SourceID: sourceID}, repository.GlobalProxyHealth{GlobalProxyID: id})
	if err != nil {
		return ProxyCandidate{}, fmt.Errorf("保存候选代理: %w", err)
	}
	InvalidateCandidateCache()
	return candidate, nil
}

func SetProxyCandidatePinned(rawURI string, pinned bool) error {
	identity, _, err := globalProxyIdentity(rawURI)
	if err != nil {
		return err
	}
	store, err := candidateStore()
	if err != nil {
		return err
	}
	if err := store.SetGlobalProxyPinned(context.Background(), identity.SemanticFingerprint, pinned); err != nil {
		return fmt.Errorf("更新全局代理固定状态: %w", err)
	}
	InvalidateCandidateCache()
	return nil
}

func RemoveProxyCandidate(rawURI string) (wasActive bool, err error) {
	proxyCandidatesMu.Lock()
	defer proxyCandidatesMu.Unlock()

	identity, _, err := globalProxyIdentity(rawURI)
	if err != nil {
		return false, err
	}
	store, err := candidateStore()
	if err != nil {
		return false, err
	}
	record, err := store.DeleteGlobalProxy(context.Background(), identity.SemanticFingerprint)
	if err != nil {
		return false, fmt.Errorf("删除候选代理: %w", err)
	}
	legacyProxy := strings.TrimSpace(Load().ProxyURL)
	wasActive = record.Pinned || strings.EqualFold(legacyProxy, rawURI)
	if wasActive && legacyProxy != "" {
		if writeErr := WriteSettings(map[string]any{"proxy_url": ""}); writeErr != nil {
			return false, fmt.Errorf("清理已删除代理的旧配置: %w", writeErr)
		}
	}
	InvalidateCandidateCache()
	return wasActive, nil
}

func RemoveDisabledProxyCandidates() ([]string, error) {
	proxyCandidatesMu.Lock()
	defer proxyCandidatesMu.Unlock()

	store, err := candidateStore()
	if err != nil {
		return nil, err
	}
	removed, err := store.DeleteDisabledGlobalProxies(context.Background())
	if err != nil {
		return nil, fmt.Errorf("清理禁用全局代理: %w", err)
	}
	InvalidateCandidateCache()
	return removed, nil
}

func UpdateProxyCandidateTest(rawURI string, ok bool, elapsedMs float64, errText string) error {
	cfg := Load()
	_, err := updateProxyCandidateResult(rawURI, ok, elapsedMs, errText, cfg.EntryProxyProbeCooldownSeconds, false, false, 0)
	return err
}

// UpdateProxyCandidateProbeResult records one scheduled health probe. Only
// scheduled failures contribute to automatic disablement.
func UpdateProxyCandidateProbeResult(
	rawURI string,
	ok bool,
	elapsedMs float64,
	errText string,
	cooldownSeconds int,
	autoDisable bool,
	failureLimit int,
) (bool, error) {
	return updateProxyCandidateResult(rawURI, ok, elapsedMs, errText, cooldownSeconds, true, autoDisable, failureLimit)
}

func updateProxyCandidateResult(
	rawURI string,
	ok bool,
	elapsedMs float64,
	errText string,
	cooldownSeconds int,
	countScheduledFailure bool,
	autoDisable bool,
	failureLimit int,
) (bool, error) {
	proxyCandidatesMu.Lock()
	defer proxyCandidatesMu.Unlock()
	identity, _, err := globalProxyIdentity(rawURI)
	if err != nil {
		return false, err
	}
	store, err := candidateStore()
	if err != nil {
		return false, err
	}
	var record *repository.GlobalProxyRecord
	records, err := store.ListGlobalProxies(context.Background())
	if err != nil {
		return false, err
	}
	for index := range records {
		if records[index].CanonicalIdentity == identity.SemanticFingerprint {
			record = &records[index]
			break
		}
	}
	if record == nil {
		return false, fmt.Errorf("未找到该候选 URI")
	}
	consecutiveFailures := record.ConsecutiveFailures
	disabled := record.Disabled

	if cooldownSeconds < 0 {
		cooldownSeconds = 0
	}
	cooldown := int64(0)
	autoDisabled := false
	if ok {
		consecutiveFailures = 0
	} else {
		cooldown = time.Now().Add(time.Duration(cooldownSeconds) * time.Second).Unix()
		if countScheduledFailure {
			consecutiveFailures++
			if autoDisable && failureLimit > 0 && consecutiveFailures >= failureLimit && !disabled {
				disabled = true
				autoDisabled = true
				cooldown = 0
			}
		}
	}
	err = store.UpdateGlobalProxyHealth(context.Background(), repository.GlobalProxyHealth{
		GlobalProxyID: record.ID, CooldownUntil: cooldown, LastTestOK: ok,
		LastTestMS: elapsedMs, LastTestAt: time.Now().Unix(), LastTestError: errText,
		ConsecutiveFailures: consecutiveFailures,
	}, disabled)
	if err != nil {
		return false, fmt.Errorf("保存候选代理测试结果: %w", err)
	}
	InvalidateCandidateCache()
	return autoDisabled, nil
}

func HasProxyCandidate(rawURI string) bool {
	identity, _, err := globalProxyIdentity(rawURI)
	if err != nil {
		return false
	}
	return hasGlobalProxyIdentity(identity.SemanticFingerprint)
}

func MigrateLegacyProxy(rawURI string) error {
	rawURI = strings.TrimSpace(rawURI)
	if rawURI == "" {
		return nil
	}
	if err := ValidateProxyCandidateURI(rawURI); err != nil {
		return err
	}
	store, err := candidateStore()
	if err != nil {
		return err
	}
	parsed, _ := url.Parse(rawURI)
	name := extractProxyCandidateName(rawURI)
	if name == "" {
		name = strings.ToLower(parsed.Scheme) + "://" + parsed.Host
	}
	identity, id, err := globalProxyIdentity(rawURI)
	if err != nil {
		return err
	}
	err = store.UpsertGlobalProxy(context.Background(), repository.GlobalProxy{
		ID: id, RawURI: rawURI, CanonicalIdentity: identity.SemanticFingerprint,
		EndpointFingerprint: identity.EndpointFingerprint, Name: name,
		Type: strings.ToLower(parsed.Scheme), Pinned: true,
	}, repository.GlobalProxySource{GlobalProxyID: id, SourceType: "legacy_config", SourceID: "proxy_url"}, repository.GlobalProxyHealth{GlobalProxyID: id})
	if err != nil {
		return fmt.Errorf("迁移旧 proxy_url: %w", err)
	}
	// A legacy proxy is explicitly adopted as the pinned route. Clear stale
	// admission gates from the old runtime while preserving diagnostics.
	if records, listErr := store.ListGlobalProxies(context.Background()); listErr == nil {
		for _, record := range records {
			if record.ID != id {
				continue
			}
			if healthErr := store.UpdateGlobalProxyHealth(context.Background(), repository.GlobalProxyHealth{
				GlobalProxyID: id, LastTestOK: record.LastTestOK, LastTestMS: record.LastTestMS,
				LastTestAt: record.LastTestAt, LastTestError: record.LastTestError,
			}, false); healthErr != nil {
				return fmt.Errorf("重置迁移代理准入状态: %w", healthErr)
			}
			break
		}
	} else {
		return fmt.Errorf("读取迁移代理状态: %w", listErr)
	}
	if err := WriteSettings(map[string]any{"proxy_url": ""}); err != nil {
		return fmt.Errorf("迁移成功但清理 proxy_url 失败: %w", err)
	}
	InvalidateCandidateCache()
	return nil
}

func globalProxyIdentity(rawURI string) (transport.CanonicalProxyIdentity, string, error) {
	identity, err := transport.ProxyIdentity(strings.TrimSpace(rawURI))
	if err != nil {
		return transport.CanonicalProxyIdentity{}, "", err
	}
	sum := sha256.Sum256([]byte("gp\x00" + identity.SemanticFingerprint))
	return identity, "gp_" + hex.EncodeToString(sum[:12]), nil
}

func hasGlobalProxyIdentity(identity string) bool {
	store, err := candidateStore()
	if err != nil {
		return false
	}
	records, err := store.ListGlobalProxies(context.Background())
	if err != nil {
		return false
	}
	for _, record := range records {
		if record.CanonicalIdentity == identity {
			return true
		}
	}
	return false
}

func ValidateProxyCandidateURI(rawURI string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURI))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("URI 格式无效")
	}
	if !supportedEntryProxyScheme(strings.ToLower(parsed.Scheme)) {
		return fmt.Errorf("不支持的代理协议: %s", parsed.Scheme)
	}
	return nil
}

func extractProxyCandidateName(rawURI string) string {
	if strings.HasPrefix(strings.ToLower(rawURI), "vmess://") {
		body := rawURI[len("vmess://"):]
		if index := strings.IndexAny(body, "?#"); index >= 0 {
			body = body[:index]
		}
		body = strings.NewReplacer("-", "+", "_", "/").Replace(body)
		if remainder := len(body) % 4; remainder != 0 {
			body += strings.Repeat("=", 4-remainder)
		}
		if decoded, err := base64.StdEncoding.DecodeString(body); err == nil {
			var payload map[string]any
			if json.Unmarshal(decoded, &payload) == nil {
				if name, ok := payload["ps"].(string); ok && strings.TrimSpace(name) != "" {
					return strings.TrimSpace(name)
				}
			}
		}
	}
	if index := strings.LastIndex(rawURI, "#"); index >= 0 {
		name := rawURI[index+1:]
		if decoded, err := url.QueryUnescape(name); err == nil {
			return strings.TrimSpace(decoded)
		}
		return strings.TrimSpace(name)
	}
	return ""
}

func supportedEntryProxyScheme(scheme string) bool {
	switch strings.ToLower(scheme) {
	case "vless", "vmess", "ss", "trojan", "hysteria2", "hy2", "tuic",
		"socks5", "socks5h", "socks", "http", "https", "ssr", "shadowsocksr",
		"hysteria", "anytls", "clash":
		return true
	default:
		return false
	}
}
