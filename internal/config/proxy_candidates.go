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

//nolint:gochecknoglobals
var proxyCandidatesMu sync.Mutex

func AddProxyCandidate(rawURI string) (ProxyCandidate, error) {
	proxyCandidatesMu.Lock()
	defer proxyCandidatesMu.Unlock()

	rawURI = strings.TrimSpace(rawURI)
	if rawURI == "" {
		return ProxyCandidate{}, fmt.Errorf("URI 为空")
	}

	cfg := Load()
	for _, c := range cfg.ProxyURLCandidates {
		if c.RawURI == rawURI {
			return ProxyCandidate{}, fmt.Errorf("该 URI 已在候选列表中")
		}
	}

	parsed, err := url.Parse(rawURI)
	if err != nil || parsed.Scheme == "" {
		return ProxyCandidate{}, fmt.Errorf("URI 格式无效: %w", err)
	}

	if !supportedProxyScheme(parsed.Scheme) {
		return ProxyCandidate{}, fmt.Errorf("不支持的代理协议: %s", parsed.Scheme)
	}

	name := extractProxyCandidateName(rawURI)
	if name == "" {
		host := parsed.Hostname()
		port := parsed.Port()
		if port != "" {
			name = parsed.Scheme + "://" + host + ":" + port
		} else {
			name = parsed.Scheme + "://" + host
		}
	}

	scheme := parsed.Scheme

	candidate := ProxyCandidate{
		RawURI: rawURI,
		Name:   name,
		Type:   scheme,
	}

	updated := cfg.ProxyURLCandidates
	if updated == nil {
		updated = []ProxyCandidate{}
	}
	updated = append(updated, candidate)
	if err := WriteSettings(map[string]any{"proxy_url_candidates": updated}); err != nil {
		return ProxyCandidate{}, fmt.Errorf("保存配置失败: %w", err)
	}
	InvalidateCache()
	return candidate, nil
}

func RemoveProxyCandidate(rawURI string) error {
	proxyCandidatesMu.Lock()
	defer proxyCandidatesMu.Unlock()

	cfg := Load()
	found := false
	updated := make([]ProxyCandidate, 0, len(cfg.ProxyURLCandidates))
	for _, c := range cfg.ProxyURLCandidates {
		if c.RawURI == rawURI {
			found = true
			continue
		}
		updated = append(updated, c)
	}
	if !found {
		return fmt.Errorf("未找到该候选 URI")
	}
	updates := map[string]any{"proxy_url_candidates": updated}
	if cfg.ProxyURL == rawURI {
		updates["proxy_url"] = ""
	}
	if err := WriteSettings(updates); err != nil {
		return fmt.Errorf("保存配置失败: %w", err)
	}
	InvalidateCache()
	return nil
}

func UpdateProxyCandidateTest(rawURI string, ok bool, ms float64, errStr string) error {
	proxyCandidatesMu.Lock()
	defer proxyCandidatesMu.Unlock()

	cfg := Load()
	updated := make([]ProxyCandidate, 0, len(cfg.ProxyURLCandidates))
	for _, c := range cfg.ProxyURLCandidates {
		if c.RawURI == rawURI {
			c.LastTestOK = ok
			c.LastTestMs = ms
			c.LastTestAt = time.Now().Unix()
			c.LastTestError = errStr
		}
		updated = append(updated, c)
	}
	if err := WriteSettings(map[string]any{"proxy_url_candidates": updated}); err != nil {
		return fmt.Errorf("保存配置失败: %w", err)
	}
	InvalidateCache()
	return nil
}

func extractProxyCandidateName(rawURI string) string {
	if strings.HasPrefix(rawURI, "vmess://") {
		b64Str := rawURI[8:]
		if idx := strings.Index(b64Str, "?"); idx != -1 {
			b64Str = b64Str[:idx]
		}
		if idx := strings.Index(b64Str, "#"); idx != -1 {
			b64Str = b64Str[:idx]
		}
		b64Str = strings.ReplaceAll(strings.ReplaceAll(b64Str, "-", "+"), "_", "/")
		if pad := len(b64Str) % 4; pad != 0 {
			b64Str += strings.Repeat("=", 4-pad)
		}
		if b, err := base64.StdEncoding.DecodeString(b64Str); err == nil {
			var d map[string]any
			if err := json.Unmarshal(b, &d); err == nil {
				if ps, ok := d["ps"].(string); ok && strings.TrimSpace(ps) != "" {
					return strings.TrimSpace(ps)
				}
			}
		}
	}

	if idx := strings.LastIndex(rawURI, "#"); idx != -1 {
		escapedName := rawURI[idx+1:]
		if dec, err := url.PathUnescape(escapedName); err == nil {
			return strings.TrimSpace(dec)
		}
		return strings.TrimSpace(escapedName)
	}

	return ""
}

var supportedSchemes = map[string]bool{
	"vless": true, "vmess": true, "ss": true, "trojan": true,
	"hysteria2": true, "hy2": true, "tuic": true,
	"socks5": true, "socks5h": true, "socks": true,
	"http": true, "https": true,
	"ssr": true, "shadowsocksr": true, "hysteria": true,
}

func supportedProxyScheme(scheme string) bool {
	return supportedSchemes[scheme]
}