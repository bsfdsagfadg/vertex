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
		updated[i].LastTestMs = elapsedMs
		updated[i].LastTestAt = time.Now().Unix()
		updated[i].LastTestError = errText
		if ok {
			updated[i].LastTestOK = true
			updated[i].CooldownUntil = 0
		} else {
			updated[i].LastTestOK = false
			updated[i].CooldownUntil = time.Now().Unix() + 60
			// 网络类错误（拨号/连接失败）自动禁用该候选，避免失效节点留在轮询池中。
			// 注意：触发路径为"候选健康拨测"——面板手动测试（adminTestProxyNode）与
			// 后台周期拨测（StartEntryProxyProbeLoop，每 5min 探测业务域）。
			// 运行期（CreateSession 链）请求失败一律不归因入口（v4：入口健康与 RT 解耦），
			// 候选恢复后由周期拨测成功后自动解禁（SetProxyCandidatesDisabled(false)）。
			if isNetworkTestFailure(errText) {
				updated[i].Disabled = true
			}
		}
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

// isNetworkTestFailure 判断测试错误是否为网络类失败（拨号/连接/超时），此类失败应自动禁用候选。
func isNetworkTestFailure(errText string) bool {
	lower := strings.ToLower(errText)
	return strings.Contains(lower, "dial") ||
		strings.Contains(lower, "refused") ||
		strings.Contains(lower, "i/o timeout") ||
		strings.Contains(lower, "deadline exceeded") ||
		strings.Contains(lower, "connection")
}

// SetProxyCandidatesDisabled 批量启用/禁用候选。禁用仅影响轮询挑选；启用后恢复参与轮询。
func SetProxyCandidatesDisabled(uris []string, disabled bool) error {
	proxyCandidatesMu.Lock()
	defer proxyCandidatesMu.Unlock()

	cfg := Load()
	updated := append([]ProxyCandidate(nil), cfg.ProxyURLCandidates...)
	targets := make(map[string]bool, len(uris))
	for _, u := range uris {
		targets[strings.TrimSpace(u)] = true
	}
	changed := false
	for i := range updated {
		if targets[updated[i].RawURI] {
			updated[i].Disabled = disabled
			changed = true
		}
	}
	if !changed {
		return fmt.Errorf("未找到任何匹配的候选 URI")
	}
	if err := WriteSettings(map[string]any{"proxy_url_candidates": updated}); err != nil {
		return fmt.Errorf("保存候选代理状态: %w", err)
	}
	return nil
}

// BatchRemoveProxyCandidates 批量删除候选。
func BatchRemoveProxyCandidates(uris []string) error {
	proxyCandidatesMu.Lock()
	defer proxyCandidatesMu.Unlock()

	cfg := Load()
	targets := make(map[string]bool, len(uris))
	for _, u := range uris {
		targets[strings.TrimSpace(u)] = true
	}
	updated := make([]ProxyCandidate, 0, len(cfg.ProxyURLCandidates))
	found := false
	for _, c := range cfg.ProxyURLCandidates {
		if targets[c.RawURI] {
			found = true
			continue
		}
		updated = append(updated, c)
	}
	if !found {
		return fmt.Errorf("未找到任何匹配的候选 URI")
	}
	if err := WriteSettings(map[string]any{"proxy_url_candidates": updated}); err != nil {
		return fmt.Errorf("保存候选代理: %w", err)
	}
	return nil
}

// RemoveDisabledProxyCandidates 清空所有已禁用的候选，返回删除数量。
func RemoveDisabledProxyCandidates() (int, error) {
	proxyCandidatesMu.Lock()
	defer proxyCandidatesMu.Unlock()

	cfg := Load()
	updated := make([]ProxyCandidate, 0, len(cfg.ProxyURLCandidates))
	removed := 0
	for _, c := range cfg.ProxyURLCandidates {
		if c.Disabled {
			removed++
			continue
		}
		updated = append(updated, c)
	}
	if removed == 0 {
		return 0, nil
	}
	if err := WriteSettings(map[string]any{"proxy_url_candidates": updated}); err != nil {
		return 0, fmt.Errorf("保存候选代理: %w", err)
	}
	return removed, nil
}

// DedupProxyCandidates 按 RawURI 去重（保留首次出现项），返回移除数量。
func DedupProxyCandidates() (int, error) {
	proxyCandidatesMu.Lock()
	defer proxyCandidatesMu.Unlock()

	cfg := Load()
	seen := make(map[string]bool, len(cfg.ProxyURLCandidates))
	updated := make([]ProxyCandidate, 0, len(cfg.ProxyURLCandidates))
	removed := 0
	for _, c := range cfg.ProxyURLCandidates {
		if seen[c.RawURI] {
			removed++
			continue
		}
		seen[c.RawURI] = true
		updated = append(updated, c)
	}
	if removed == 0 {
		return 0, nil
	}
	if err := WriteSettings(map[string]any{"proxy_url_candidates": updated}); err != nil {
		return 0, fmt.Errorf("保存候选代理: %w", err)
	}
	return removed, nil
}

// GetProxyCandidates 返回候选列表的深拷贝，避免外部并发修改内部切片。
func GetProxyCandidates() []ProxyCandidate {
	return append([]ProxyCandidate(nil), Load().ProxyURLCandidates...)
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
