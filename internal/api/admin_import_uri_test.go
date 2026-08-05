package api

import (
	"net/url"
	"testing"

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
		"type":       "ssr",
		"name":       "SSR-1",
		"server":     "ss.example.com",
		"port":       8388,
		"cipher":     "aes-256-cfb",
		"password":   "p@ss word",
		"protocol":   "auth_aes128_md5",
		"obfs":       "tls1.2_ticket_auth",
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
		"type":                   "ssh",
		"name":                   "SSH-1",
		"server":                 "203.0.113.10",
		"port":                   22,
		"username":               "root",
		"password":               "secret",
		"private-key":            "QUJDRA==",
		"private-key-passphrase": "pp",
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
