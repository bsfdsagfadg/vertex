package transport

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
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

func TestParseURISocks4ExplicitUnsupported(t *testing.T) {
	for _, uri := range []string{"socks4://1.2.3.4:1080#s4", "socks4a://example.com:1080#s4a"} {
		_, err := ParseURI(uri)
		var unsupported *ErrProtocolUnsupported
		if !errors.As(err, &unsupported) {
			t.Fatalf("ParseURI(%s) 应返回 ErrProtocolUnsupported，got %v", uri, err)
		}
		if unsupported.Protocol != "socks4" || !strings.Contains(unsupported.Reason, "socks5") {
			t.Fatalf("unexpected unsupported detail: %#v", unsupported)
		}
	}
}

func TestParseURISSH(t *testing.T) {
	out, err := ParseURI("ssh://user:secret@ssh.example.com:2222#ssh-node")
	if err != nil {
		t.Fatalf("ParseURI ssh returned error: %v", err)
	}
	if out["type"] != "ssh" || out["server"] != "ssh.example.com" || out["port"] != 2222 ||
		out["username"] != "user" || out["password"] != "secret" {
		t.Fatalf("unexpected ssh map: %#v", out)
	}

	// 缺密码（私钥无法经 URL 表达）或缺用户名 → unsupported
	for _, uri := range []string{"ssh://user@ssh.example.com:22", "ssh://:pass@ssh.example.com:22"} {
		_, err := ParseURI(uri)
		var unsupported *ErrProtocolUnsupported
		if !errors.As(err, &unsupported) || unsupported.Protocol != "ssh" {
			t.Fatalf("ParseURI(%s) 应返回 ssh unsupported，got %v", uri, err)
		}
	}
}

func TestParseURIBareHostPortFallbackToSocks5(t *testing.T) {
	cases := []struct {
		line string
		host string
		port int
	}{
		{"1.2.3.4:8080", "1.2.3.4", 8080},
		{"example.com:1080", "example.com", 1080},
		{"[::1]:8080", "::1", 8080},
		{"[2001:db8::1]:443", "2001:db8::1", 443},
	}
	for _, testCase := range cases {
		out, err := ParseURI(testCase.line)
		if err != nil {
			t.Fatalf("ParseURI(%q) returned error: %v", testCase.line, err)
		}
		if out["type"] != "socks5" || out["server"] != testCase.host || out["port"] != testCase.port {
			t.Fatalf("bare line %q parsed wrong: %#v", testCase.line, out)
		}
	}

	for _, bad := range []string{"no-port", "host:notaport", "host:70000", "host:port:extra", "[::1]:bad"} {
		if _, err := ParseURI(bad); err == nil {
			t.Fatalf("bare line %q should fail to parse", bad)
		}
	}
}

func TestParseURISSCipherAliasNormalized(t *testing.T) {
	// 明文 method:password 形式
	out, err := ParseURI("ss://chacha20-poly1305:secret@1.2.3.4:8388#demo")
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if got := out["cipher"]; got != "chacha20-ietf-poly1305" {
		t.Fatalf("cipher 未归一化，got %#v", got)
	}

	// base64(method:password) 整体编码形式（v2rayN 传统写法）
	raw := "ss://" + base64.StdEncoding.EncodeToString([]byte("chacha20-poly1305:secret")) + "@1.2.3.4:8388"
	out, err = ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI(base64 whole) returned error: %v", err)
	}
	if got := out["cipher"]; got != "chacha20-ietf-poly1305" {
		t.Fatalf("base 整体形式 cipher 未归一化，got %#v", got)
	}

	// 其他 cipher 原样保留（不猜测映射）
	out, err = ParseURI("ss://chacha20-ietf:secret@1.2.3.4:8388")
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if got := out["cipher"]; got != "chacha20-ietf" {
		t.Fatalf("未知 cipher 应原样保留，got %#v", got)
	}
}

func TestParseURIRealityDefaultsFingerprint(t *testing.T) {
	// vless reality 缺 fp → 补 chrome
	out, err := ParseURI("vless://12345678-1234-1234-1234-123456789012@cf.example.com:443?security=reality&sni=edge.example.com&pbk=pubkey&sid=abcd#demo")
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if got := out["client-fingerprint"]; got != "chrome" {
		t.Fatalf("reality 缺 fp 应补 chrome，got %#v", got)
	}

	// vmess tls 缺 fp → 补 chrome
	rawJSON := `{"v":"2","ps":"demo","add":"vmess.example.com","port":"443","id":"12345678-1234-1234-1234-123456789012","tls":"tls"}`
	raw := "vmess://" + base64.StdEncoding.EncodeToString([]byte(rawJSON))
	out, err = ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if got := out["client-fingerprint"]; got != "chrome" {
		t.Fatalf("vmess tls 缺 fp 应补 chrome，got %#v", got)
	}
}

func TestParseURIVmessDefaultsAlterID(t *testing.T) {
	rawJSON := `{"v":"2","ps":"demo","add":"vmess.example.com","port":"443","id":"12345678-1234-1234-1234-123456789012"}`
	raw := "vmess://" + base64.StdEncoding.EncodeToString([]byte(rawJSON))
	out, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if got := out["alterId"]; got != 0 {
		t.Fatalf("aid 缺失时 alterId 应默认 0，got %#v", got)
	}
}

func TestParseURIVmessTLSAndOpts(t *testing.T) {
	// 1. tls 为 bool true 的 vmess
	rawJSONBoolTLS := `{"v":"2","ps":"vmess-bool-tls","add":"vmess.example.com","port":"443","id":"12345678-1234-1234-1234-123456789012","tls":true,"net":"h2","host":"cdn.example.com","path":"/h2path","scy":"aes-128-gcm"}`
	rawBool := "vmess://" + base64.StdEncoding.EncodeToString([]byte(rawJSONBoolTLS))

	out, err := ParseURI(rawBool)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if out["tls"] != true {
		t.Fatalf("expected tls=true, got %#v", out["tls"])
	}
	if out["cipher"] != "aes-128-gcm" {
		t.Fatalf("expected cipher=aes-128-gcm, got %#v", out["cipher"])
	}
	if _, ok := out["h2-opts"]; !ok {
		t.Fatalf("expected h2-opts present for net=h2, got %#v", out)
	}
	if _, ok := out["http-opts"]; ok {
		t.Fatalf("http-opts should not exist for net=h2, got %#v", out)
	}

	// 2. net=http 的 vmess
	rawJSONHTTP := `{"v":"2","ps":"vmess-http","add":"vmess.example.com","port":"80","id":"12345678-1234-1234-1234-123456789012","net":"http","host":"cdn.example.com","path":"/httppath"}`
	rawHTTP := "vmess://" + base64.StdEncoding.EncodeToString([]byte(rawJSONHTTP))

	outHTTP, err := ParseURI(rawHTTP)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if _, ok := outHTTP["http-opts"]; !ok {
		t.Fatalf("expected http-opts present for net=http, got %#v", outHTTP)
	}
	if _, ok := outHTTP["h2-opts"]; ok {
		t.Fatalf("h2-opts should not exist for net=http, got %#v", outHTTP)
	}

	// 验证 adapter.ParseProxy
	if _, err := adapter.ParseProxy(out); err != nil {
		t.Fatalf("ParseProxy failed for vmess h2: %v", err)
	}
	if _, err := adapter.ParseProxy(outHTTP); err != nil {
		t.Fatalf("ParseProxy failed for vmess http: %v", err)
	}
}

func TestParseURIVlessAndTuicOpts(t *testing.T) {
	// vless h2
	h2URI := "vless://12345678-1234-1234-1234-123456789012@cf.example.com:443?security=tls&type=h2&path=%2Fh2&host=cdn.example.com#vless-h2"
	outH2, err := ParseURI(h2URI)
	if err != nil {
		t.Fatalf("ParseURI vless h2 error: %v", err)
	}
	if h2Opts, ok := outH2["h2-opts"].(map[string]any); !ok {
		t.Fatalf("expected h2-opts in vless h2, got %#v", outH2)
	} else if h2Opts["path"] != "/h2" {
		t.Fatalf("unexpected h2 path: %#v", h2Opts["path"])
	}

	// vless xhttp
	xhttpURI := "vless://12345678-1234-1234-1234-123456789012@cf.example.com:443?security=tls&type=xhttp&path=%2Fx&host=cdn.example.com&mode=auto#vless-xhttp"
	outXHTTP, err := ParseURI(xhttpURI)
	if err != nil {
		t.Fatalf("ParseURI vless xhttp error: %v", err)
	}
	if xhttpOpts, ok := outXHTTP["xhttp-opts"].(map[string]any); !ok {
		t.Fatalf("expected xhttp-opts in vless xhttp, got %#v", outXHTTP)
	} else if xhttpOpts["mode"] != "auto" {
		t.Fatalf("unexpected xhttp mode: %#v", xhttpOpts["mode"])
	}

	// tuic 密码提取
	tuicURI := "tuic://12345678-1234-1234-1234-123456789012:mypassword@tuic.example.com:8443#tuic-node"
	outTuic, err := ParseURI(tuicURI)
	if err != nil {
		t.Fatalf("ParseURI tuic error: %v", err)
	}
	if outTuic["uuid"] != "12345678-1234-1234-1234-123456789012" {
		t.Fatalf("tuic uuid missing, got %#v", outTuic["uuid"])
	}
	if outTuic["password"] != "mypassword" {
		t.Fatalf("tuic password missing, got %#v", outTuic["password"])
	}

	// 验证 adapter.ParseProxy
	if _, err := adapter.ParseProxy(outH2); err != nil {
		t.Fatalf("ParseProxy vless h2 failed: %v", err)
	}
	if _, err := adapter.ParseProxy(outXHTTP); err != nil {
		t.Fatalf("ParseProxy vless xhttp failed: %v", err)
	}
	if _, err := adapter.ParseProxy(outTuic); err != nil {
		t.Fatalf("ParseProxy tuic failed: %v", err)
	}
}

func TestParseURISSPluginFlagsAndV2RayPlugin(t *testing.T) {
	// 带纯旗标 (tls, mux) 的 v2ray-plugin
	raw := "ss://YWVzLTEyOC1nY206aGFNTE1YaXJCeW42ckdWaA@example.com:20111/?plugin=v2ray-plugin%3Btls%3Bhost%3Dcdn.example.com%3Bmux#ss-v2ray"
	out, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI ss v2ray-plugin error: %v", err)
	}
	if out["plugin"] != "v2ray-plugin" {
		t.Fatalf("expected plugin v2ray-plugin, got %#v", out["plugin"])
	}
	pluginOpts, ok := out["plugin-opts"].(map[string]any)
	if !ok {
		t.Fatalf("plugin-opts missing or invalid type: %#v", out["plugin-opts"])
	}
	if pluginOpts["tls"] != true || pluginOpts["mux"] != true || pluginOpts["host"] != "cdn.example.com" {
		t.Fatalf("unexpected plugin-opts: %#v", pluginOpts)
	}

	if _, err := adapter.ParseProxy(out); err != nil {
		t.Fatalf("ParseProxy ss v2ray-plugin failed: %v", err)
	}
}

func TestMihomoParseProxyAllProtocolsClashURI(t *testing.T) {
	// 模拟各类节点从 JSON 编码再 json.Unmarshal(map[string]any) 后的属性 (数字变成 float64)
	testCases := []map[string]any{
		{
			"name": "ss-node", "type": "ss", "server": "1.2.3.4", "port": float64(8388),
			"cipher": "chacha20-ietf-poly1305", "password": "password", "udp": true,
		},
		{
			"name": "vmess-ws-tls", "type": "vmess", "server": "1.2.3.4", "port": float64(443),
			"uuid": "12345678-1234-1234-1234-123456789012", "alterId": float64(0), "cipher": "auto",
			"tls": true, "network": "ws", "ws-opts": map[string]any{"path": "/ws", "headers": map[string]any{"Host": "example.com"}},
			"client-fingerprint": "chrome", "udp": true,
		},
		{
			"name": "vless-reality", "type": "vless", "server": "1.2.3.4", "port": float64(443),
			"uuid": "12345678-1234-1234-1234-123456789012", "encryption": "none", "tls": true,
			"reality-opts": map[string]any{"public-key": "MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI", "short-id": "abcd"},
			"servername": "example.com", "client-fingerprint": "chrome", "udp": true,
		},
		{
			"name": "trojan-grpc", "type": "trojan", "server": "1.2.3.4", "port": float64(443),
			"password": "password", "network": "grpc", "grpc-opts": map[string]any{"grpc-service-name": "service"},
			"sni": "example.com", "udp": true,
		},
		{
			"name": "hy2-node", "type": "hysteria2", "server": "1.2.3.4", "port": float64(443),
			"password": "password", "ports": "20000-30000", "sni": "example.com", "udp": true,
		},
		{
			"name": "tuic-node", "type": "tuic", "server": "1.2.3.4", "port": float64(8443),
			"uuid": "12345678-1234-1234-1234-123456789012", "password": "password",
			"congestion-controller": "bbr", "udp": true,
		},
	}

	for _, tc := range testCases {
		name := tc["name"].(string)
		t.Run(name, func(t *testing.T) {
			// 先 Marshal 为 json 再 Unmarshal 为 map[string]any 模拟 clash:// 的行为
			b, err := json.Marshal(tc)
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}
			var unmarshaled map[string]any
			if err := json.Unmarshal(b, &unmarshaled); err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}

			proxy, err := adapter.ParseProxy(unmarshaled)
			if err != nil {
				t.Fatalf("ParseProxy failed for %s: %v\nMap: %#v", name, err, unmarshaled)
			}
			if proxy == nil {
				t.Fatalf("ParseProxy returned nil for %s", name)
			}
			if closer, ok := proxy.(interface{ Close() error }); ok {
				_ = closer.Close()
			}
		})
	}
}
