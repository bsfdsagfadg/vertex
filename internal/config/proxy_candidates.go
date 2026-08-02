package config

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

//nolint:gochecknoglobals // Serializes candidate read-modify-write operations.
var proxyCandidatesMu sync.Mutex

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

	cfg := Load()
	for _, candidate := range cfg.ProxyURLCandidates {
		if candidate.RawURI == rawURI {
			return ProxyCandidate{}, fmt.Errorf("该 URI 已在候选列表中")
		}
	}

	name := extractProxyCandidateName(rawURI)
	if name == "" {
		name = scheme + "://" + parsed.Host
	}
	candidate := ProxyCandidate{RawURI: rawURI, Name: name, Type: scheme}
	updated := append(append([]ProxyCandidate(nil), cfg.ProxyURLCandidates...), candidate)
	if err := WriteSettings(map[string]any{"proxy_url_candidates": updated}); err != nil {
		return ProxyCandidate{}, fmt.Errorf("保存候选代理: %w", err)
	}
	return candidate, nil
}

func RemoveProxyCandidate(rawURI string) (wasActive bool, err error) {
	proxyCandidatesMu.Lock()
	defer proxyCandidatesMu.Unlock()

	cfg := Load()
	updated := make([]ProxyCandidate, 0, len(cfg.ProxyURLCandidates))
	found := false
	for _, candidate := range cfg.ProxyURLCandidates {
		if candidate.RawURI == rawURI {
			found = true
			continue
		}
		updated = append(updated, candidate)
	}
	if !found {
		return false, fmt.Errorf("未找到该候选 URI")
	}
	updates := map[string]any{"proxy_url_candidates": updated}
	wasActive = cfg.ProxyURL == rawURI
	if wasActive {
		updates["proxy_url"] = ""
	}
	if err := WriteSettings(updates); err != nil {
		return false, fmt.Errorf("保存候选代理: %w", err)
	}
	return wasActive, nil
}

func UpdateProxyCandidateTest(rawURI string, ok bool, elapsedMs float64, errText string) error {
	proxyCandidatesMu.Lock()
	defer proxyCandidatesMu.Unlock()

	cfg := Load()
	updated := append([]ProxyCandidate(nil), cfg.ProxyURLCandidates...)
	found := false
	for i := range updated {
		if updated[i].RawURI != rawURI {
			continue
		}
		found = true
		updated[i].LastTestOK = ok
		updated[i].LastTestMs = elapsedMs
		updated[i].LastTestAt = time.Now().Unix()
		updated[i].LastTestError = errText
		break
	}
	if !found {
		return fmt.Errorf("未找到该候选 URI")
	}
	if err := WriteSettings(map[string]any{"proxy_url_candidates": updated}); err != nil {
		return fmt.Errorf("保存候选代理测试结果: %w", err)
	}
	return nil
}

func HasProxyCandidate(rawURI string) bool {
	for _, candidate := range Load().ProxyURLCandidates {
		if candidate.RawURI == rawURI {
			return true
		}
	}
	return false
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
