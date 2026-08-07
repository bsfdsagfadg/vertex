package transport

import (
	"encoding/base64"
	"net/url"
	"testing"

	"github.com/metacubex/mihomo/adapter"
)

func TestParseURIShadowsocksKeepsPortAndPlugin(t *testing.T) {
	raw := "ss://YWVzLTEyOC1nY206aGFNTE1YaXJCeW42ckdWaA@example.com:20111/?plugin=simple-obfs%3Bobfs%3Dhttp%3Bobfs-host%3Dcdn.example.com#demo"

	out, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}

	if got := out["port"]; got != 20111 {
		t.Fatalf("expected port 20111, got %#v", got)
	}
	if got := out["plugin"]; got != "obfs" {
		t.Fatalf("expected plugin obfs, got %#v", got)
	}
	opts, ok := out["plugin-opts"].(map[string]any)
	if !ok {
		t.Fatalf("plugin-opts missing or wrong type: %#v", out["plugin-opts"])
	}
	if opts["mode"] != "http" || opts["host"] != "cdn.example.com" {
		t.Fatalf("unexpected plugin opts: %#v", opts)
	}
}

func TestParseURIAdditionalMihomoProtocols(t *testing.T) {
	password := base64.RawURLEncoding.EncodeToString([]byte("secret"))
	remarks := base64.RawURLEncoding.EncodeToString([]byte("SSR 节点"))
	ssrBody := "ssr.example.com:8388:auth_sha1_v4:aes-256-cfb:tls1.2_ticket_auth:" + password + "/?remarks=" + url.QueryEscape(remarks)
	ssrURI := "ssr://" + base64.RawURLEncoding.EncodeToString([]byte(ssrBody))

	cases := []struct {
		name string
		uri  string
		typ  string
	}{
		{"SOCKS5", "socks5://user:pass@127.0.0.1:1080#local-socks", "socks5"},
		{"HTTPS", "https://user:pass@127.0.0.1:8443?sni=proxy.example.com&insecure=1#secure-http", "http"},
		{"SSR", ssrURI, "ssr"},
		{"Hysteria", "hysteria://auth@hy.example.com:443?upmbps=100&downmbps=200&sni=edge.example.com#hy", "hysteria"},
		{"AnyTLS", "anytls://secret@any.example.com:443?sni=edge.example.com&insecure=1#any", "anytls"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			mapping, err := ParseURI(testCase.uri)
			if err != nil {
				t.Fatalf("ParseURI: %v", err)
			}
			if mapping["type"] != testCase.typ {
				t.Fatalf("type=%v, want %s", mapping["type"], testCase.typ)
			}
			proxy, err := adapter.ParseProxy(mapping)
			if err != nil {
				t.Fatalf("mihomo adapter.ParseProxy: %v; map=%#v", err, mapping)
			}
			if closer, ok := proxy.(interface{ Close() error }); ok {
				_ = closer.Close()
			}
		})
	}
}

func TestParseURIAdditionalProtocolsRejectsIncompleteInput(t *testing.T) {
	for _, uri := range []string{
		"socks5://",
		"hysteria://auth@hy.example.com:443",
		"anytls://any.example.com:443",
	} {
		if _, err := ParseURI(uri); err == nil {
			t.Fatalf("incomplete URI should fail: %s", uri)
		}
	}
}

func TestParseURIVlessKeepsRealityAndWS(t *testing.T) {
	raw := "vless://12345678-1234-1234-1234-123456789012@cf.example.com:443?security=reality&sni=edge.example.com&fp=chrome&pbk=pubkey&sid=abcd&type=ws&host=edge.example.com&path=%2Fws&flow=xtls-rprx-vision#demo"

	out, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}

	if got := out["servername"]; got != "edge.example.com" {
		t.Fatalf("expected servername edge.example.com, got %#v", got)
	}
	if got := out["client-fingerprint"]; got != "chrome" {
		t.Fatalf("expected client-fingerprint chrome, got %#v", got)
	}
	if got := out["flow"]; got != "xtls-rprx-vision" {
		t.Fatalf("expected flow preserved, got %#v", got)
	}
	realityOpts, ok := out["reality-opts"].(map[string]any)
	if !ok {
		t.Fatalf("reality-opts missing or wrong type: %#v", out["reality-opts"])
	}
	if realityOpts["public-key"] != "pubkey" || realityOpts["short-id"] != "abcd" {
		t.Fatalf("unexpected reality opts: %#v", realityOpts)
	}
	if got := out["network"]; got != "ws" {
		t.Fatalf("expected network ws, got %#v", got)
	}
	wsOpts, ok := out["ws-opts"].(map[string]any)
	if !ok {
		t.Fatalf("ws-opts missing or wrong type: %#v", out["ws-opts"])
	}
	headers, ok := wsOpts["headers"].(map[string]any)
	if !ok {
		t.Fatalf("ws headers missing or wrong type: %#v", wsOpts["headers"])
	}
	if wsOpts["path"] != "/ws" || headers["Host"] != "edge.example.com" {
		t.Fatalf("unexpected ws opts: %#v", wsOpts)
	}
}

func TestParseURIHy2KeepsPortRange(t *testing.T) {
	raw := "hy2://secret@203.10.99.51:20000?sni=www.bing.com&insecure=1&ports=20000-55000#demo"

	out, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}

	if got := out["ports"]; got != "20000-55000" {
		t.Fatalf("expected ports preserved, got %#v", got)
	}
	if got := out["sni"]; got != "www.bing.com" {
		t.Fatalf("expected sni preserved, got %#v", got)
	}
	if got := out["skip-cert-verify"]; got != true {
		t.Fatalf("expected skip-cert-verify=true, got %#v", got)
	}
}

func TestParseURIVmessKeepsSNIAndFingerprint(t *testing.T) {
	rawJSON := `{"v":"2","ps":"demo","add":"vmess.example.com","port":"443","id":"12345678-1234-1234-1234-123456789012","aid":"0","net":"ws","host":"edge.example.com","path":"/ws","tls":"tls","sni":"edge.example.com","fp":"chrome","alpn":"h2,http/1.1","allowInsecure":"1"}`
	raw := "vmess://" + base64.StdEncoding.EncodeToString([]byte(rawJSON))

	out, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}

	if out["servername"] != "edge.example.com" || out["client-fingerprint"] != "chrome" {
		t.Fatalf("tls metadata not preserved: %#v", out)
	}
	alpn, ok := out["alpn"].([]string)
	if !ok || len(alpn) != 2 || alpn[0] != "h2" {
		t.Fatalf("alpn not preserved: %#v", out["alpn"])
	}
}

func TestParseURITuicKeepsPassword(t *testing.T) {
	out, err := ParseURI("tuic://12345678-1234-1234-1234-123456789012:secret@tuic.example.com:443#demo")
	if err != nil {
		t.Fatal(err)
	}
	if out["uuid"] != "12345678-1234-1234-1234-123456789012" || out["password"] != "secret" {
		t.Fatalf("unexpected tuic credentials: %#v", out)
	}
}

func TestParseURITuicWithoutUserInfoDoesNotPanic(t *testing.T) {
	if _, err := ParseURI("tuic://tuic.example.com:443#demo"); err != nil {
		t.Fatal(err)
	}
}

func TestParseURIVmessBoolTLSAndH2(t *testing.T) {
	rawJSON := `{"v":"2","ps":"demo","add":"vmess.example.com","port":"443","id":"12345678-1234-1234-1234-123456789012","tls":true,"net":"h2","host":"cdn.example.com","path":"/h2","scy":"aes-128-gcm"}`
	out, err := ParseURI("vmess://" + base64.StdEncoding.EncodeToString([]byte(rawJSON)))
	if err != nil {
		t.Fatal(err)
	}
	if out["tls"] != true || out["cipher"] != "aes-128-gcm" || out["client-fingerprint"] != "chrome" {
		t.Fatalf("vmess tls metadata mismatch: %#v", out)
	}
	if _, ok := out["h2-opts"]; !ok {
		t.Fatalf("missing h2-opts: %#v", out)
	}
	if _, ok := out["http-opts"]; ok {
		t.Fatalf("h2 must not use http-opts: %#v", out)
	}
}

func TestParseURIVlessTransportOptions(t *testing.T) {
	h2, err := ParseURI("vless://12345678-1234-1234-1234-123456789012@cf.example.com:443?security=tls&type=h2&path=%2Fh2&host=cdn.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := h2["h2-opts"]; !ok {
		t.Fatalf("missing h2-opts: %#v", h2)
	}
	xhttp, err := ParseURI("vless://12345678-1234-1234-1234-123456789012@cf.example.com:443?security=tls&type=xhttp&path=%2Fx&host=cdn.example.com&mode=auto")
	if err != nil {
		t.Fatal(err)
	}
	if opts, ok := xhttp["xhttp-opts"].(map[string]any); !ok || opts["mode"] != "auto" {
		t.Fatalf("missing xhttp opts: %#v", xhttp)
	}
	unknown, err := ParseURI("vless://12345678-1234-1234-1234-123456789012@cf.example.com:443?security=tls&type=unknown")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := unknown["network"]; ok {
		t.Fatalf("unknown transport must not be forwarded to mihomo: %#v", unknown)
	}
}

func TestParseURIHy2OnlyKeepsCertificateFingerprint(t *testing.T) {
	out, err := ParseURI("hy2://secret@hy.example.com:443?fp=chrome")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["fingerprint"]; ok {
		t.Fatalf("browser fingerprint must not be used for certificate pinning: %#v", out)
	}
	fingerprint := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	out, err = ParseURI("hy2://secret@hy.example.com:443?fp=" + fingerprint)
	if err != nil || out["fingerprint"] != fingerprint {
		t.Fatalf("certificate fingerprint missing: %#v err=%v", out, err)
	}
}

func TestParseURISSPluginFlags(t *testing.T) {
	raw := "ss://YWVzLTEyOC1nY206aGFNTE1YaXJCeW42ckdWaA@example.com:20111/?plugin=v2ray-plugin%3Btls%3Bmux%3Bhost%3Dcdn.example.com"
	out, err := ParseURI(raw)
	if err != nil {
		t.Fatal(err)
	}
	opts, ok := out["plugin-opts"].(map[string]any)
	if !ok || opts["tls"] != true || opts["mux"] != true || opts["host"] != "cdn.example.com" {
		t.Fatalf("unexpected plugin opts: %#v", out)
	}
}

func TestParseURINormalizesShadowsocksCipherAlias(t *testing.T) {
	raw := "ss://Y2hhY2hhMjAtcG9seTEzMDU6c2VjcmV0@example.com:8388"
	out, err := ParseURI(raw)
	if err != nil {
		t.Fatal(err)
	}
	if out["cipher"] != "chacha20-ietf-poly1305" {
		t.Fatalf("unexpected cipher: %#v", out["cipher"])
	}
}
