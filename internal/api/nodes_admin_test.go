package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

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

func TestProxyMapToURISSRRoundTrip(t *testing.T) {
	// 回归：带 obfsparam/protoparam 的 SSR 节点，参数必须拼进 base64 体内，
	// 否则转出 URI 无法被 ParseURI 解析、导入被静默跳过
	proxy := map[string]any{
		"type":        "ssr",
		"name":        "SSR-1",
		"server":      "ss.example.com",
		"port":        8388,
		"cipher":      "aes-256-cfb",
		"password":    "p@ss word",
		"protocol":    "auth_aes128_md5",
		"obfs":        "tls1.2_ticket_auth",
		"obfsparam":  "obfs.example.com",
		"protoparam": "param123",
	}
	raw := proxyMapToURI(proxy)
	if raw == "" {
		t.Fatal("proxyMapToURI returned empty URI")
	}
	pn, err := transport.ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI rejected converted SSR URI %q: %v", raw, err)
	}
	if pn.Type != "ssr" || pn.Server != "ss.example.com" || pn.Port != 8388 {
		t.Fatalf("unexpected node: type=%q server=%q port=%d", pn.Type, pn.Server, pn.Port)
	}
	if pn.Password != "p@ss word" {
		t.Fatalf("password not preserved: %q", pn.Password)
	}
	if pn.ObfsParam != "obfs.example.com" {
		t.Fatalf("obfsparam not preserved: %q", pn.ObfsParam)
	}
	if pn.ProtocolParam != "param123" {
		t.Fatalf("protoparam not preserved: %q", pn.ProtocolParam)
	}
	if !pn.Supported {
		t.Fatalf("expected Supported=true, got %q", pn.UnsupportedReason)
	}
}

func TestProxyMapToURISSHRoundTrip(t *testing.T) {
	sshURI := proxyMapToURI(map[string]any{
		"type":                        "ssh",
		"name":                        "SSH-1",
		"server":                      "203.0.113.10",
		"port":                        22,
		"username":                    "root",
		"password":                    "secret",
		"private-key":                 "QUJDRA==",
		"private-key-passphrase":      "pp",
	})
	pn, err := transport.ParseURI(sshURI)
	if err != nil {
		t.Fatalf("ParseURI rejected converted SSH URI %q: %v", sshURI, err)
	}
	if pn.Type != "ssh" || pn.Server != "203.0.113.10" || pn.Username != "root" || pn.Password != "secret" {
		t.Fatalf("ssh round trip mismatch: %#v", pn)
	}
	if pn.SSHPrivateKey != "QUJDRA==" || pn.SSHPrivateKeyPassphrase != "pp" {
		t.Fatalf("ssh key params not preserved: pk=%q psk=%q", pn.SSHPrivateKey, pn.SSHPrivateKeyPassphrase)
	}

	if got := proxyMapToURI(map[string]any{"type": "ssh", "server": "203.0.113.10", "port": 22}); got != "" {
		t.Fatalf("expected empty URI for ssh without credentials, got %q", got)
	}
	if got := proxyMapToURI(map[string]any{"type": "naive", "server": "naive.example.com", "port": 443, "username": "u", "password": "p"}); got != "" {
		t.Fatalf("expected empty URI for unsupported naive type, got %q", got)
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
	if got := out.Plugin; got != "obfs" {
		t.Fatalf("plugin not preserved: %#v", got)
	}
	if out.PluginOptions == "" {
		t.Fatalf("plugin-opts missing: %#v", out.PluginOptions)
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
	if out.Type != "ss" || out.Server != "example.com" {
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
	if out.Type != "vless" || out.TLS == nil || out.TLS.ServerName != "edge.example.com" {
		t.Fatalf("unexpected imported node: %#v", out)
	}
	if out.Transport == nil || out.Transport.Type != "ws" || out.Transport.Path != "/ws" {
		t.Fatalf("ws-opts not preserved: %#v", out.Transport)
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
	if out.Type != "ss" || out.Port != 8388 {
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
	if out.Type != "vmess" || out.TLS == nil || out.TLS.ServerName != "edge.example.com" {
		t.Fatalf("unexpected imported node: %#v", out)
	}
	if out.Transport == nil || out.Transport.Type != "ws" {
		t.Fatalf("ws transport missing: %#v", out.Transport)
	}
	headers := out.Transport.Headers["Host"]
	if len(headers) == 0 || headers[0] != "edge.example.com" {
		t.Fatalf("unexpected ws headers: %#v", out.Transport)
	}
}

func TestParseClashYAMLToNodesSkipsUnsupportedTypes(t *testing.T) {
	yamlText := `
proxies:
  - { name: wg-a, type: wireguard, server: 1.2.3.4, port: 51820, private-key: abc, public-key: def }
  - { name: snell-a, type: snell, server: 5.6.7.8, port: 443, psk: xyz }
  - { name: valid-ss, type: ss, server: example.com, port: 8388, cipher: aes-128-gcm, password: secret }
`
	imported := parseClashYAMLToNodes(yamlText)
	if len(imported) != 1 {
		t.Fatalf("expected only the supported ss node to be imported, got %d: %#v", len(imported), imported)
	}
	if imported[0].Type != "ss" || imported[0].Name != "valid-ss" {
		t.Fatalf("unexpected imported node: %#v", imported[0])
	}
	if !strings.HasPrefix(imported[0].RawURI, "ss://") {
		t.Fatalf("expected standard ss URI, got %q", imported[0].RawURI)
	}
}

func TestParseClashYAMLToNodesVlessWSTLSAndHy2Ports(t *testing.T) {
	yamlText := `
proxies:
  - { name: vless-ws, type: vless, server: cf.example.com, port: 443, uuid: 12345678-1234-1234-1234-123456789012, tls: true, servername: edge.example.com, client-fingerprint: chrome, network: ws, ws-opts: { path: /ws, headers: { Host: edge.example.com } } }
  - { name: hy2-p, type: hysteria2, server: 203.10.99.51, port: 20000, password: secret, sni: www.bing.com, skip-cert-verify: true, ports: 20000-55000 }
`
	imported := parseClashYAMLToNodes(yamlText)
	if len(imported) != 2 {
		t.Fatalf("expected 2 nodes, got %d: %#v", len(imported), imported)
	}

	vless, err := transport.ParseURI(imported[0].RawURI)
	if err != nil {
		t.Fatalf("ParseURI vless returned error: %v", err)
	}
	if vless.Type != "vless" || vless.TLS == nil || vless.TLS.ServerName != "edge.example.com" {
		t.Fatalf("unexpected vless IR: %#v", vless)
	}
	if vless.Transport == nil || vless.Transport.Type != "ws" || vless.Transport.Path != "/ws" {
		t.Fatalf("ws transport missing: %#v", vless.Transport)
	}
	if !vless.Supported {
		t.Fatalf("vless should be supported: %s", vless.UnsupportedReason)
	}

	hy2, err := transport.ParseURI(imported[1].RawURI)
	if err != nil {
		t.Fatalf("ParseURI hy2 returned error: %v", err)
	}
	if hy2.Type != "hysteria2" || len(hy2.ServerPorts) == 0 || hy2.ServerPorts[0] != "20000:55000" {
		t.Fatalf("hy2 ports not preserved: %#v", hy2)
	}
	if !hy2.Supported {
		t.Fatalf("hy2 should be supported: %s", hy2.UnsupportedReason)
	}
}

func TestNodeIdentityFromIR(t *testing.T) {
	uriA := "vless://12345678-1234-1234-1234-123456789012@cf.example.com:443?security=tls#name-a"
	uriB := "vless://12345678-1234-1234-1234-123456789012@cf.example.com:443?security=tls#name-b"
	keyA, okA := nodeIdentityFromIR(uriA)
	keyB, okB := nodeIdentityFromIR(uriB)
	if !okA || !okB || keyA != keyB {
		t.Fatalf("same server/cred with different fragments should share a key: %q vs %q (ok=%v,%v)", keyA, keyB, okA, okB)
	}
	if keyA != "vless://12345678-1234-1234-1234-123456789012@cf.example.com:443" {
		t.Fatalf("unexpected identity key: %q", keyA)
	}

	payload, err := json.Marshal(map[string]any{
		"add": "cf.example.com",
		"port": 443,
		"id":  "aa11bb22-cc33-dd44-ee55-ff6601122334",
		"ps":  "test",
	})
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	vmessKey, okV := nodeIdentityFromIR("vmess://" + base64.StdEncoding.EncodeToString(payload))
	if !okV {
		t.Fatalf("vmess identity should parse")
	}
	if vmessKey == keyA {
		t.Fatalf("vmess and vless with different credentials must not share a key")
	}
	if !strings.HasPrefix(vmessKey, "vmess://") {
		t.Fatalf("unexpected vmess identity key: %q", vmessKey)
	}
}

func TestDedupNodesUsesIRIdentity(t *testing.T) {
	// 依赖 api 包 init 已注册 nodes.NodeIdentityFunc
	uriA := "vless://12345678-1234-1234-1234-123456789012@cf.example.com:443?security=tls#name-a"
	uriB := "vless://12345678-1234-1234-1234-123456789012@cf.example.com:443?security=tls#name-b"
	nodes.MergeNodes([]nodes.Node{{RawURI: uriA, Name: "a"}, {RawURI: uriB, Name: "b"}})
	removed := nodes.DedupNodes()
	if removed != 1 {
		t.Fatalf("expected 1 removed via IR identity, got %d", removed)
	}
	for _, n := range nodes.LoadNodes() {
		nodes.DeleteNode(n.RawURI)
	}
}

func TestAdminTestNodeDisablesUnsupportedAndUnparseableURIs(t *testing.T) {
	adm := &AdminHandler{}
	cases := []struct {
		name string
		uri  string
	}{
		{"unsupported transport", "vless://uuid@example.com:443?type=xhttp"},
		{"legacy clash uri", "clash://" + base64.StdEncoding.EncodeToString([]byte(`{"name":"demo","type":"ss","server":"example.com","port":8388}`))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"raw_uri":%q,"auto_disable":true}`, tc.uri)
			req := httptest.NewRequest(http.MethodPost, "/api/admin/nodes/test", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			adm.adminTestNode(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			var resp map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp["ok"] != false || resp["disabled"] != true {
				t.Fatalf("expected ok=false disabled=true, got %#v", resp)
			}
			errStr, _ := resp["error"].(string)
			if errStr == "" {
				t.Fatalf("expected non-empty error reason")
			}
			// 不支持/不可解析路径写 healthMap 记录原因
			h := nodes.LoadHealth()[tc.uri]
			if h == nil || h.LastTestError == "" {
				t.Fatalf("expected health error recorded, got %#v", h)
			}
		})
	}
}

func TestAdminTestProxyNodeSkipsUnsupportedURI(t *testing.T) {
	adm := &AdminHandler{}
	body := `{"raw_uri":"vless://uuid@example.com:443?type=xhttp"}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/proxy-nodes/test", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	adm.adminTestProxyNode(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["ok"] != false {
		t.Fatalf("expected ok=false, got %#v", resp)
	}
	errStr, _ := resp["error"].(string)
	if !strings.Contains(errStr, "unsupported") {
		t.Fatalf("expected unsupported reason, got %q", errStr)
	}
}
