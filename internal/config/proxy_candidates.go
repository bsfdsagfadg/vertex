package config

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/db"
)

type ProxyCandidate struct {
	RawURI        string  `json:"raw_uri"`
	Name          string  `json:"name"`
	Type          string  `json:"type"`
	Disabled      bool    `json:"disabled"`
	CooldownUntil int64   `json:"cooldown_until"`
	LastTestOK    bool    `json:"last_test_ok"`
	LastTestMs    float64 `json:"last_test_ms"`
	LastTestAt    int64   `json:"last_test_at"`
	LastTestError string  `json:"last_test_error"`
}

//nolint:gochecknoglobals // Serializes candidate read-modify-write operations.
var proxyCandidatesMu sync.Mutex

func candidateStore() (*sql.DB, error) {
	if db.GlobalDB == nil {
		return nil, fmt.Errorf("数据库尚未初始化")
	}
	return db.GlobalDB, nil
}

func ListProxyCandidates() []ProxyCandidate {
	store, err := candidateStore()
	if err != nil {
		return nil
	}
	rows, err := store.Query(`SELECT raw_uri, name, type, disabled, cooldown_until, last_test_ok, last_test_ms, last_test_at, last_test_error
		FROM entry_proxy_candidates ORDER BY rowid`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var result []ProxyCandidate
	for rows.Next() {
		var candidate ProxyCandidate
		if err := rows.Scan(&candidate.RawURI, &candidate.Name, &candidate.Type, &candidate.Disabled,
			&candidate.CooldownUntil, &candidate.LastTestOK, &candidate.LastTestMs,
			&candidate.LastTestAt, &candidate.LastTestError); err == nil {
			result = append(result, candidate)
		}
	}
	return result
}

func AddProxyCandidate(rawURI string) (ProxyCandidate, error) {
	proxyCandidatesMu.Lock()
	defer proxyCandidatesMu.Unlock()

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
	normalized, err := NormalizeProxyURI(rawURI)
	if err != nil {
		return ProxyCandidate{}, err
	}
	store, err := candidateStore()
	if err != nil {
		return ProxyCandidate{}, err
	}
	var exists int
	if err := store.QueryRow("SELECT COUNT(*) FROM entry_proxy_candidates WHERE normalized_uri = ?", normalized).Scan(&exists); err != nil {
		return ProxyCandidate{}, fmt.Errorf("检查候选代理: %w", err)
	}
	if exists > 0 {
		return ProxyCandidate{}, fmt.Errorf("该 URI 已在候选列表中")
	}
	name := extractProxyCandidateName(rawURI)
	if name == "" {
		name = scheme + "://" + parsed.Host
	}
	candidate := ProxyCandidate{RawURI: rawURI, Name: name, Type: scheme}
	_, err = store.Exec(`INSERT INTO entry_proxy_candidates
		(raw_uri, normalized_uri, name, type, disabled, cooldown_until, last_test_ok, last_test_ms, last_test_at, last_test_error)
		VALUES (?, ?, ?, ?, 0, 0, 0, 0, 0, '')`, rawURI, normalized, name, scheme)
	if err != nil {
		return ProxyCandidate{}, fmt.Errorf("保存候选代理: %w", err)
	}
	return candidate, nil
}

func RemoveProxyCandidate(rawURI string) (wasActive bool, err error) {
	proxyCandidatesMu.Lock()
	defer proxyCandidatesMu.Unlock()

	normalized, err := NormalizeProxyURI(rawURI)
	if err != nil {
		return false, err
	}
	store, err := candidateStore()
	if err != nil {
		return false, err
	}
	result, err := store.Exec("DELETE FROM entry_proxy_candidates WHERE normalized_uri = ?", normalized)
	if err != nil {
		return false, fmt.Errorf("删除候选代理: %w", err)
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return false, fmt.Errorf("未找到该候选 URI")
	}
	legacy := strings.TrimSpace(Load().ProxyURL)
	legacyNormalized, _ := NormalizeProxyURI(legacy)
	wasActive = legacyNormalized == normalized
	if wasActive {
		if err := WriteSettings(map[string]any{"proxy_url": ""}); err != nil {
			return false, fmt.Errorf("清理旧入口代理配置: %w", err)
		}
	}
	return wasActive, nil
}

func RemoveDisabledProxyCandidates() ([]string, error) {
	proxyCandidatesMu.Lock()
	defer proxyCandidatesMu.Unlock()

	store, err := candidateStore()
	if err != nil {
		return nil, err
	}
	tx, err := store.Begin()
	if err != nil {
		return nil, fmt.Errorf("开始清理禁用入口代理: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.Query("SELECT raw_uri FROM entry_proxy_candidates WHERE disabled = 1 ORDER BY rowid")
	if err != nil {
		return nil, fmt.Errorf("读取禁用入口代理: %w", err)
	}
	var removed []string
	for rows.Next() {
		var rawURI string
		if err := rows.Scan(&rawURI); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("扫描禁用入口代理: %w", err)
		}
		removed = append(removed, rawURI)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("关闭禁用入口代理结果集: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM entry_proxy_candidates WHERE disabled = 1"); err != nil {
		return nil, fmt.Errorf("删除禁用入口代理: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交禁用入口代理清理: %w", err)
	}
	return removed, nil
}

func UpdateProxyCandidateTest(rawURI string, ok bool, elapsedMs float64, errText string) error {
	proxyCandidatesMu.Lock()
	defer proxyCandidatesMu.Unlock()
	normalized, err := NormalizeProxyURI(rawURI)
	if err != nil {
		return err
	}
	store, err := candidateStore()
	if err != nil {
		return err
	}
	cooldown := int64(0)
	if !ok {
		cooldown = time.Now().Add(60 * time.Second).Unix()
	}
	result, err := store.Exec(`UPDATE entry_proxy_candidates
		SET last_test_ok = ?, last_test_ms = ?, last_test_at = ?, last_test_error = ?, cooldown_until = ?
		WHERE normalized_uri = ?`, ok, elapsedMs, time.Now().Unix(), errText, cooldown, normalized)
	if err != nil {
		return fmt.Errorf("保存候选代理测试结果: %w", err)
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return fmt.Errorf("未找到该候选 URI")
	}
	return nil
}

func HasProxyCandidate(rawURI string) bool {
	normalized, err := NormalizeProxyURI(rawURI)
	if err != nil {
		return false
	}
	store, err := candidateStore()
	if err != nil {
		return false
	}
	var count int
	return store.QueryRow("SELECT COUNT(*) FROM entry_proxy_candidates WHERE normalized_uri = ?", normalized).Scan(&count) == nil && count > 0
}

func MigrateLegacyProxy(rawURI string) error {
	rawURI = strings.TrimSpace(rawURI)
	if rawURI == "" {
		return nil
	}
	if err := ValidateProxyCandidateURI(rawURI); err != nil {
		return err
	}
	normalized, err := NormalizeProxyURI(rawURI)
	if err != nil {
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
	_, err = store.Exec(`INSERT INTO entry_proxy_candidates
		(raw_uri, normalized_uri, name, type, disabled)
		VALUES (?, ?, ?, ?, 0)
		ON CONFLICT(normalized_uri) DO UPDATE SET disabled = 0, cooldown_until = 0`, rawURI, normalized, name, strings.ToLower(parsed.Scheme))
	if err != nil {
		return fmt.Errorf("迁移旧 proxy_url: %w", err)
	}
	if err := WriteSettings(map[string]any{"proxy_url": ""}); err != nil {
		return fmt.Errorf("迁移成功但清理 proxy_url 失败: %w", err)
	}
	return nil
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
