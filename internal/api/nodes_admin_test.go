package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/transport"
	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/constant/features"
)

func TestBatchTestTimeoutAndDuplicateRejection(t *testing.T) {
	if got := batchTestTimeout(1); got != 5*time.Minute {
		t.Fatalf("小批量总超时=%v, want 5m", got)
	}
	if got := batchTestTimeout(1000); got <= 5*time.Minute {
		t.Fatalf("大批量应按轮次增加总超时: %v", got)
	}

	nodes.FinishTestProgress()
	if !nodes.StartTestProgress(1) {
		t.Fatal("测试状态初始化失败")
	}
	t.Cleanup(func() {
		nodes.TerminateTestProgress()
		nodes.FinishTestProgress()
	})
	adm := &AdminHandler{}
	recorder := httptest.NewRecorder()
	adm.adminTestAll(recorder, httptest.NewRequest("POST", "/api/admin/nodes/test-all", nil))
	if recorder.Code != 409 {
		t.Fatalf("重复批量测试 status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestParseInlineYamlAttrsKeepsNestedObjects(t *testing.T) {
	attrs := parseInlineYamlAttrs("name: demo, type: vless, ws-opts: { path: /ws, headers: { Host: edge.example.com } }, reality-opts: { public-key: pubkey, short-id: abcd }")

	if got := attrs["ws-opts"]; got != "{ path: /ws, headers: { Host: edge.example.com } }" {
		t.Fatalf("ws-opts was split unexpectedly: %q", got)
	}
	if got := attrs["reality-opts"]; got != "{ public-key: pubkey, short-id: abcd }" {
		t.Fatalf("reality-opts was split unexpectedly: %q", got)
	}
}

func TestClashProxyToURIPreservesVlessWSAndReality(t *testing.T) {
	raw := clashProxyToURI(map[string]string{
		"type":               "vless",
		"name":               "demo",
		"server":             "cf.example.com",
		"port":               "443",
		"uuid":               "12345678-1234-1234-1234-123456789012",
		"tls":                "true",
		"servername":         "edge.example.com",
		"client-fingerprint": "chrome",
		"flow":               "xtls-rprx-vision",
		"network":            "ws",
		"ws-opts":            "{ path: /ws, headers: { Host: edge.example.com } }",
		"reality-opts":       "{ public-key: pubkey, short-id: abcd }",
	})

	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	q := u.Query()

	if u.Scheme != "vless" {
		t.Fatalf("unexpected scheme: %s", u.Scheme)
	}
	if q.Get("security") != "reality" {
		t.Fatalf("security not preserved: %q", q.Get("security"))
	}
	if q.Get("pbk") != "pubkey" || q.Get("sid") != "abcd" {
		t.Fatalf("reality opts not preserved: pbk=%q sid=%q", q.Get("pbk"), q.Get("sid"))
	}
	if q.Get("type") != "ws" || q.Get("path") != "/ws" || q.Get("host") != "edge.example.com" {
		t.Fatalf("ws params not preserved: type=%q path=%q host=%q", q.Get("type"), q.Get("path"), q.Get("host"))
	}
	if q.Get("sni") != "edge.example.com" || q.Get("fp") != "chrome" || q.Get("flow") != "xtls-rprx-vision" {
		t.Fatalf("tls params not preserved: sni=%q fp=%q flow=%q", q.Get("sni"), q.Get("fp"), q.Get("flow"))
	}
}

func TestClashProxyToURIBuildsHy2WithPortRange(t *testing.T) {
	raw := clashProxyToURI(map[string]string{
		"type":             "hysteria2",
		"name":             "demo",
		"server":           "203.10.99.51",
		"port":             "20000",
		"ports":            "20000-55000",
		"password":         "secret",
		"sni":              "www.bing.com",
		"skip-cert-verify": "true",
	})

	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	q := u.Query()

	if u.Scheme != "hy2" {
		t.Fatalf("unexpected scheme: %s", u.Scheme)
	}
	if q.Get("ports") != "20000-55000" {
		t.Fatalf("ports not preserved: %q", q.Get("ports"))
	}
	if q.Get("sni") != "www.bing.com" || q.Get("insecure") != "1" {
		t.Fatalf("hy2 tls params not preserved: sni=%q insecure=%q", q.Get("sni"), q.Get("insecure"))
	}
}

func TestParseClashYAMLToNodesPreservesSSPluginOpts(t *testing.T) {
	yamlText := `
proxies:
  - { name: 'HK Demo', type: ss, server: example.com, port: 12022, cipher: aes-128-gcm, password: secret, plugin: obfs, plugin-opts: { mode: http, host: edge.example.com }, udp: true }
`

	imported := parseClashYAMLToNodes(yamlText)
	if len(imported) != 1 {
		t.Fatalf("expected 1 node, got %d", len(imported))
	}
	if imported[0].Type != "ss" || imported[0].Name != "HK Demo" {
		t.Fatalf("unexpected imported node metadata: %#v", imported[0])
	}

	out, err := transport.ParseURI(imported[0].RawURI)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if got := out["plugin"]; got != "obfs" {
		t.Fatalf("plugin not preserved: %#v", got)
	}
	opts, ok := out["plugin-opts"].(map[string]any)
	if !ok {
		t.Fatalf("plugin-opts missing or wrong type: %#v", out["plugin-opts"])
	}
	if opts["mode"] != "http" || opts["host"] != "edge.example.com" {
		t.Fatalf("plugin-opts not preserved: %#v", opts)
	}
	if got := out["udp"]; got != true {
		t.Fatalf("udp not preserved: %#v", got)
	}
}

func TestParseClashYAMLToNodesSkipsInvalidProxyObjects(t *testing.T) {
	yamlText := `
proxies:
  - { name: bad missing endpoint, type: ss }
  - { name: group-ish, type: select }
`

	imported := parseClashYAMLToNodes(yamlText)
	if len(imported) != 0 {
		t.Fatalf("expected invalid proxy objects to be skipped, got %#v", imported)
	}
}

func TestParseClashYAMLToNodesBuildsTUICWithMihomo(t *testing.T) {
	yamlText := `
proxies:
  - name: tuic-demo
    type: tuic
    server: tuic.example.com
    port: 443
    token: secret
    sni: tuic.example.com
    alpn: [h3]
    skip-cert-verify: true
    congestion-controller: bbr
    udp-relay-mode: native
`

	assertClashNodeBuildsWithMihomo(t, yamlText, "tuic")
}

func TestParseClashYAMLToNodesBuildsWireGuardWithMihomo(t *testing.T) {
	privateKey := base64.StdEncoding.EncodeToString(make([]byte, 32))
	publicKeyBytes := make([]byte, 32)
	for i := range publicKeyBytes {
		publicKeyBytes[i] = 1
	}
	publicKey := base64.StdEncoding.EncodeToString(publicKeyBytes)
	yamlText := fmt.Sprintf(`
proxies:
  - name: wg-demo
    type: wireguard
    server: 198.51.100.10
    port: 51820
    ip: 10.0.0.2/32
    private-key: %s
    public-key: %s
    udp: true
`, privateKey, publicKey)

	assertClashNodeBuildsWithMihomo(t, yamlText, "wireguard")
}

func TestParseClashYAMLToNodesRejectsIncompleteTUICAndWireGuard(t *testing.T) {
	yamlText := `
proxies:
  - { name: tuic-missing-auth, type: tuic, server: example.com, port: 443 }
  - { name: wg-missing-local-address, type: wireguard, server: 198.51.100.10, port: 51820, private-key: private, public-key: public }
  - { name: wg-missing-public-key, type: wireguard, server: 198.51.100.10, port: 51820, ip: 10.0.0.2/32, private-key: private }
`

	if imported := parseClashYAMLToNodes(yamlText); len(imported) != 0 {
		t.Fatalf("expected incomplete TUIC/WireGuard maps to be skipped, got %#v", imported)
	}
}

func TestSubscriptionFallbackProxyIgnoresActiveNode(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ActiveNodeURI = "clash://active-node"
	cfg.ProxyURL = "  http://127.0.0.1:7897  "

	got := subscriptionFallbackProxy(config.StaticProvider(cfg))
	if got != "http://127.0.0.1:7897" {
		t.Fatalf("expected global proxy_url, got %q", got)
	}
}

func assertClashNodeBuildsWithMihomo(t *testing.T, yamlText string, wantType string) {
	t.Helper()
	imported := parseClashYAMLToNodes(yamlText)
	if len(imported) != 1 {
		t.Fatalf("expected 1 node, got %d", len(imported))
	}
	out, err := transport.ParseURI(imported[0].RawURI)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if out["type"] != wantType {
		t.Fatalf("expected type %q, got %#v", wantType, out["type"])
	}
	proxy, err := adapter.ParseProxy(out)
	if wantType == "wireguard" && !features.WithGVisor {
		if err == nil {
			t.Fatal("expected a default build without with_gvisor to reject WireGuard construction")
		}
		if !strings.Contains(err.Error(), "gVisor is not included") {
			t.Fatalf("expected missing gVisor error, got %v", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("mihomo rejected imported %s map: %v", wantType, err)
	}
	if closer, ok := proxy.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			t.Fatalf("close imported %s proxy: %v", wantType, err)
		}
	}
}

func TestParseImportedNodesSupportsSingleTopLevelProxyObject(t *testing.T) {
	text := `{ name: 'HK Demo', type: ss, server: example.com, port: 12022, cipher: aes-128-gcm, password: secret, plugin: obfs, plugin-opts: { mode: http, host: edge.example.com } }`

	imported := parseImportedNodes(text)
	if len(imported) != 1 {
		t.Fatalf("expected 1 node, got %d", len(imported))
	}

	out, err := transport.ParseURI(imported[0].RawURI)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if out["type"] != "ss" || out["server"] != "example.com" {
		t.Fatalf("unexpected imported node: %#v", out)
	}
}

func TestParseImportedNodesSupportsV2RayNInnerURI(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"ConfigType":     5,
		"Remarks":        "demo",
		"Address":        "cf.example.com",
		"Port":           443,
		"Password":       "12345678-1234-1234-1234-123456789012",
		"StreamSecurity": "tls",
		"Sni":            "edge.example.com",
		"Fingerprint":    "chrome",
		"Network":        "ws",
		"ProtoExtraObj":  map[string]any{"VlessEncryption": "none"},
		"TransportExtraObj": map[string]any{
			"Path": "/ws",
			"Host": "edge.example.com",
		},
	})
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	text := "v2rayn://vless/" + base64.RawURLEncoding.EncodeToString(payload)
	imported := parseImportedNodes(text)
	if len(imported) != 1 {
		t.Fatalf("expected 1 node, got %d", len(imported))
	}

	out, err := transport.ParseURI(imported[0].RawURI)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if out["type"] != "vless" || out["servername"] != "edge.example.com" {
		t.Fatalf("unexpected imported node: %#v", out)
	}
	wsOpts, ok := out["ws-opts"].(map[string]any)
	if !ok || wsOpts["path"] != "/ws" {
		t.Fatalf("ws-opts not preserved: %#v", out["ws-opts"])
	}
}

func TestParseImportedNodesSupportsSIP008(t *testing.T) {
	text := `{"servers":[{"remarks":"ss demo","server":"1.2.3.4","server_port":8388,"method":"aes-128-gcm","password":"secret"}]}`

	imported := parseImportedNodes(text)
	if len(imported) != 1 {
		t.Fatalf("expected 1 node, got %d", len(imported))
	}

	out, err := transport.ParseURI(imported[0].RawURI)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if out["type"] != "ss" || intValue(out["port"]) != 8388 {
		t.Fatalf("unexpected imported node: %#v", out)
	}
}

func TestParseImportedNodesSupportsV2RayOutbounds(t *testing.T) {
	text := `{
  "outbounds": [
    {
      "tag": "demo",
      "protocol": "vmess",
      "settings": {
        "vnext": [
          {
            "address": "v2ray.cool",
            "port": 443,
            "users": [
              {
                "id": "a3482e88-686a-4a58-8126-99c9df64b7bf",
                "security": "auto",
                "alterId": 0
              }
            ]
          }
        ]
      },
      "streamSettings": {
        "network": "ws",
        "security": "tls",
        "tlsSettings": {
          "serverName": "edge.example.com",
          "fingerprint": "chrome",
          "allowInsecure": true,
          "alpn": "h2"
        },
        "wsSettings": {
          "path": "/ws",
          "headers": {
            "Host": "edge.example.com"
          }
        }
      }
    }
  ]
}`

	imported := parseImportedNodes(text)
	if len(imported) != 1 {
		t.Fatalf("expected 1 node, got %d", len(imported))
	}

	out, err := transport.ParseURI(imported[0].RawURI)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if out["type"] != "vmess" || out["servername"] != "edge.example.com" {
		t.Fatalf("unexpected imported node: %#v", out)
	}
	wsOpts, ok := out["ws-opts"].(map[string]any)
	if !ok {
		t.Fatalf("ws-opts missing: %#v", out["ws-opts"])
	}
	headers, ok := wsOpts["headers"].(map[string]any)
	if !ok || headers["Host"] != "edge.example.com" {
		t.Fatalf("unexpected ws headers: %#v", wsOpts)
	}
}
