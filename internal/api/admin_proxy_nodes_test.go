package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

// TestHealEnabledProxyCandidateReenablesDisabled 覆盖 P9 自愈路径：
// 周期拨测成功后，曾因网络错误被自动禁用的候选应被解除（防短暂抖动永久踢出）。
func TestHealEnabledProxyCandidateReenablesDisabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("VPROXY_CONFIG", path)
	config.InvalidateCache()
	t.Cleanup(config.InvalidateCache)

	uri := "socks5://user:pass@127.0.0.1:1080#entry"
	body := fmt.Sprintf(`{"proxy_url_candidates":[
		{"raw_uri":%q,"name":"entry","disabled":true},
		{"raw_uri":%q,"name":"other","disabled":false}
	]}`, uri, "socks5://user:pass@127.0.0.2:1080#other")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	config.InvalidateCache()

	// 禁用候选被自愈解禁；未禁用的候选不受影响。
	healEnabledProxyCandidate(uri)
	for _, c := range config.GetProxyCandidates() {
		if c.RawURI == uri && c.Disabled {
			t.Fatalf("自愈应解除被禁用的候选 %q", uri)
		}
		if c.RawURI == "socks5://user:pass@127.0.0.2:1080#other" && c.Disabled {
			t.Fatalf("未禁用的候选不应被误改: %+v", c)
		}
	}

	// 已启用候选调用自愈为无操作（不报错、不改状态）。
	healEnabledProxyCandidate("socks5://user:pass@127.0.0.2:1080#other")
	for _, c := range config.GetProxyCandidates() {
		if c.RawURI == "socks5://user:pass@127.0.0.2:1080#other" && c.Disabled {
			t.Fatal("自愈不应禁用候选")
		}
	}
}

// TestEntryProbeTargetIsBusinessDomain 保证拨测目标锁定业务域（P9，防回归到 gstatic 204）。
func TestEntryProbeTargetIsBusinessDomain(t *testing.T) {
	if !strings.Contains(entryProxyProbeURL, "google.com") {
		t.Fatalf("拨测目标应为业务域，got %q", entryProxyProbeURL)
	}
	if !strings.HasSuffix(entryProxyProbeURL, "enterprise.js") {
		t.Fatalf("拨测目标应为 recaptcha enterprise.js，got %q", entryProxyProbeURL)
	}
}
