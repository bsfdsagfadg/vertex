package transport

import (
	"encoding/base64"
	"strings"
	"testing"
)

func vmessURIFromJSON(t *testing.T, rawJSON string) string {
	t.Helper()
	return "vmess://" + base64.StdEncoding.EncodeToString([]byte(rawJSON))
}

func TestParseURIShadowsocksKeepsPortAndPlugin(t *testing.T) {
	raw := "ss://YWVzLTEyOC1nY206aGFNTE1YaXJCeW42ckdWaA@example.com:20111/?plugin=simple-obfs%3Bobfs%3Dhttp%3Bobfs-host%3Dcdn.example.com#demo"

	n, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}

	if n.Port != 20111 {
		t.Fatalf("expected port 20111, got %d", n.Port)
	}
	if n.Cipher != "aes-128-gcm" {
		t.Fatalf("expected cipher aes-128-gcm, got %q", n.Cipher)
	}
	// C2：IR 存小写原始插件名 + 分段 join(";")
	if n.Plugin != "simple-obfs" {
		t.Fatalf("expected plugin simple-obfs, got %q", n.Plugin)
	}
	if n.PluginOptions != "obfs=http;obfs-host=cdn.example.com" {
		t.Fatalf("expected plugin options, got %q", n.PluginOptions)
	}
	if !n.Supported {
		t.Fatalf("expected supported=true, got unsupported: %s", n.UnsupportedReason)
	}
}

func TestParseURI_SSPlainCredentials(t *testing.T) {
	raw := "ss://chacha20-poly1305:password@example.com:443#demo"

	n, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if n.Cipher != "chacha20-ietf-poly1305" {
		t.Fatalf("expected normalized cipher chacha20-ietf-poly1305, got %q", n.Cipher)
	}
	if n.Password != "password" {
		t.Fatalf("expected password password, got %q", n.Password)
	}
}

func TestParseSS_PlainCredentialsNoHost(t *testing.T) {
	n, err := parseSS("ss://aes-128-gcm:password@:443")
	if err != nil {
		t.Fatalf("parseSS returned error: %v", err)
	}
	if n.Cipher != "aes-128-gcm" {
		t.Fatalf("expected plaintext cipher aes-128-gcm, got %q", n.Cipher)
	}
	if n.Password != "password" {
		t.Fatalf("expected plaintext password, got %q", n.Password)
	}
}

func TestParseURIVlessKeepsRealityAndWS(t *testing.T) {
	raw := "vless://12345678-1234-1234-1234-123456789012@cf.example.com:443?security=reality&sni=edge.example.com&fp=chrome&pbk=pubkey&sid=abcd&type=ws&host=edge.example.com&path=%2Fws&flow=xtls-rprx-vision#demo"

	n, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}

	if n.Type != "vless" || n.UUID != "12345678-1234-1234-1234-123456789012" {
		t.Fatalf("unexpected vless identity: %#v", n)
	}
	if n.TLS == nil || !n.TLS.Enabled {
		t.Fatalf("expected TLS enabled")
	}
	if n.TLS.ServerName != "edge.example.com" {
		t.Fatalf("expected servername edge.example.com, got %q", n.TLS.ServerName)
	}
	if n.TLS.Fingerprint != "chrome" {
		t.Fatalf("expected fingerprint chrome, got %q", n.TLS.Fingerprint)
	}
	if n.TLS.Reality == nil || n.TLS.Reality.PublicKey != "pubkey" || n.TLS.Reality.ShortID != "abcd" {
		t.Fatalf("unexpected reality opts: %#v", n.TLS.Reality)
	}
	if n.Flow != "xtls-rprx-vision" {
		t.Fatalf("expected flow preserved, got %q", n.Flow)
	}
	if n.Transport == nil || n.Transport.Type != "ws" {
		t.Fatalf("expected ws transport, got %#v", n.Transport)
	}
	if n.Transport.Path != "/ws" || n.Transport.Host != "edge.example.com" {
		t.Fatalf("unexpected ws transport: %#v", n.Transport)
	}
	if got := n.Transport.Headers["Host"]; len(got) != 1 || got[0] != "edge.example.com" {
		t.Fatalf("unexpected ws headers: %#v", n.Transport.Headers)
	}
	if !n.Supported {
		t.Fatalf("expected supported=true, got: %s", n.UnsupportedReason)
	}
}

func TestParseURIRealityDefaultFingerprint(t *testing.T) {
	// reality 无显式 fp → Fingerprint=chrome
	raw := "vless://uuid@example.com:443?security=reality&pbk=pubkey"
	n, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if n.TLS == nil || n.TLS.Fingerprint != "chrome" {
		t.Fatalf("expected reality default fingerprint chrome, got %#v", n.TLS)
	}
	if n.TLS.Reality == nil || n.TLS.Reality.PublicKey != "pubkey" {
		t.Fatalf("expected reality public key, got %#v", n.TLS.Reality)
	}
}

func TestParseURIVmessKeepsSNIAndFingerprint(t *testing.T) {
	rawJSON := `{"v":"2","ps":"demo","add":"vmess.example.com","port":"443","id":"12345678-1234-1234-1234-123456789012","aid":"0","net":"ws","host":"edge.example.com","path":"/ws","tls":"tls","sni":"edge.example.com","fp":"chrome","alpn":"h2,http/1.1","allowInsecure":"1"}`
	raw := vmessURIFromJSON(t, rawJSON)

	n, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}

	if n.TLS == nil || n.TLS.ServerName != "edge.example.com" {
		t.Fatalf("expected servername edge.example.com, got %#v", n.TLS)
	}
	if n.TLS.Fingerprint != "chrome" {
		t.Fatalf("expected fingerprint chrome, got %q", n.TLS.Fingerprint)
	}
	if len(n.TLS.ALPN) != 2 || n.TLS.ALPN[0] != "h2" {
		t.Fatalf("alpn not preserved: %#v", n.TLS.ALPN)
	}
	if n.TLS.Insecure != true {
		t.Fatalf("allowInsecure=1 should set Insecure")
	}
	if n.Transport == nil || n.Transport.Type != "ws" || n.Transport.Path != "/ws" {
		t.Fatalf("unexpected transport: %#v", n.Transport)
	}
}

func TestParseURIVmessHostDualUse(t *testing.T) {
	// C9：sni 缺失 → ServerName 回退 host；ws → Transport.Host 也填 host
	rawJSON := `{"v":"2","ps":"demo","add":"vmess.example.com","port":443,"id":"uuid","net":"ws","host":"edge.example.com","path":"/ws","tls":"tls"}`
	raw := vmessURIFromJSON(t, rawJSON)

	n, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if n.TLS == nil || n.TLS.ServerName != "edge.example.com" {
		t.Fatalf("expected TLS.ServerName fallback to host, got %#v", n.TLS)
	}
	if n.Transport == nil || n.Transport.Host != "edge.example.com" {
		t.Fatalf("expected ws Transport.Host, got %#v", n.Transport)
	}
}

func TestParseURIVmessNetH2(t *testing.T) {
	// C8：vmess net=h2 → Transport.Type=http
	rawJSON := `{"v":"2","ps":"demo","add":"vmess.example.com","port":443,"id":"uuid","net":"h2","path":"/h2path","host":"edge.example.com","tls":"tls"}`
	raw := vmessURIFromJSON(t, rawJSON)

	n, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if n.Transport == nil || n.Transport.Type != "http" {
		t.Fatalf("expected transport http, got %#v", n.Transport)
	}
	if n.Transport.Path != "/h2path" {
		t.Fatalf("expected path preserved, got %q", n.Transport.Path)
	}
}

func TestParseURIVmessScy(t *testing.T) {
	// C6：scy → Security
	rawJSON := `{"v":"2","ps":"demo","add":"vmess.example.com","port":443,"id":"uuid","net":"tcp","scy":"zero"}`
	raw := vmessURIFromJSON(t, rawJSON)

	n, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if n.Security != "zero" {
		t.Fatalf("expected security zero, got %q", n.Security)
	}
	if n.Cipher != "auto" {
		t.Fatalf("expected cipher auto, got %q", n.Cipher)
	}

	rawJSON2 := `{"v":"2","ps":"demo","add":"vmess.example.com","port":443,"id":"uuid","net":"tcp"}`
	n2, err := ParseURI(vmessURIFromJSON(t, rawJSON2))
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if n2.Security != "auto" {
		t.Fatalf("expected default security auto, got %q", n2.Security)
	}
}

func TestParseURISOCKS5(t *testing.T) {
	raw := "socks5://user:pass@192.168.1.1:1080#my-socks5"
	n, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if n.Type != "socks5" {
		t.Fatalf("expected type socks5, got %q", n.Type)
	}
	if n.Name != "my-socks5" {
		t.Fatalf("expected name my-socks5, got %q", n.Name)
	}
	if n.Server != "192.168.1.1" {
		t.Fatalf("expected server 192.168.1.1, got %q", n.Server)
	}
	if n.Port != 1080 {
		t.Fatalf("expected port 1080, got %d", n.Port)
	}
	if n.Username != "user" || n.Password != "pass" {
		t.Fatalf("expected user/pass, got username=%q password=%q", n.Username, n.Password)
	}
}

func TestParseURISOCKS5NoAuth(t *testing.T) {
	raw := "socks5://192.168.1.1:1080#no-auth"
	n, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if n.Type != "socks5" || n.Name != "no-auth" {
		t.Fatalf("unexpected node: %#v", n)
	}
	if n.Username != "" || n.Password != "" {
		t.Fatalf("expected empty credentials, got user=%q pass=%q", n.Username, n.Password)
	}
	if n.SOCKSVersion != "5" {
		t.Fatalf("expected default SOCKSVersion 5, got %q", n.SOCKSVersion)
	}
}

func TestParseURISOCKS4(t *testing.T) {
	for _, tt := range []struct {
		scheme  string
		version string
	}{
		{"socks4", "4"},
		{"socks4a", "4a"},
		{"socks", "5"},
		{"socks5h", "5"},
	} {
		raw := tt.scheme + "://203.0.113.7:4145#tag"
		n, err := ParseURI(raw)
		if err != nil {
			t.Fatalf("ParseURI(%s) returned error: %v", tt.scheme, err)
		}
		if n.Type != "socks5" {
			t.Fatalf("expected type socks5 for %s, got %q", tt.scheme, n.Type)
		}
		if n.Server != "203.0.113.7" || n.Port != 4145 {
			t.Fatalf("unexpected server/port for %s: %s:%d", tt.scheme, n.Server, n.Port)
		}
		if n.SOCKSVersion != tt.version {
			t.Fatalf("expected SOCKSVersion %q for %s, got %q", tt.version, tt.scheme, n.SOCKSVersion)
		}
		if !n.Supported {
			t.Fatalf("expected Supported=true for %s, got %q", tt.scheme, n.UnsupportedReason)
		}
	}
}

func TestParseURISocksNoPassword(t *testing.T) {
	// 有用户名但无密码 → 保留 Username（与 proxyMapToURI 转出对称），Password 空
	raw := "socks://Tag@h.example.com:1080"
	n, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if n.Type != "socks5" {
		t.Fatalf("expected type socks5, got %q", n.Type)
	}
	if n.Username != "Tag" {
		t.Fatalf("expected Username Tag, got %q", n.Username)
	}
	if n.Password != "" {
		t.Fatalf("expected empty Password, got %q", n.Password)
	}
}

func TestParseURIHTTP(t *testing.T) {
	raw := "http://user:pass@proxy.example.com:8080#http-proxy"
	n, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if n.Type != "http" {
		t.Fatalf("expected type http, got %q", n.Type)
	}
	if n.Name != "http-proxy" {
		t.Fatalf("expected name http-proxy, got %q", n.Name)
	}
	if n.Server != "proxy.example.com" {
		t.Fatalf("expected server proxy.example.com, got %q", n.Server)
	}
	if n.Port != 8080 {
		t.Fatalf("expected port 8080, got %d", n.Port)
	}
	if n.Username != "user" || n.Password != "pass" {
		t.Fatalf("expected user/pass, got user=%q pass=%q", n.Username, n.Password)
	}
	if n.TLS != nil {
		t.Fatalf("expected no tls for http, got %#v", n.TLS)
	}
}

func TestParseURIHTTPS(t *testing.T) {
	raw := "https://user:pass@proxy.example.com:443#https-proxy"
	n, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if n.Type != "http" {
		t.Fatalf("expected type http, got %q", n.Type)
	}
	if n.Name != "https-proxy" {
		t.Fatalf("expected name https-proxy, got %q", n.Name)
	}
	if n.Server != "proxy.example.com" {
		t.Fatalf("expected server proxy.example.com, got %q", n.Server)
	}
	if n.Port != 443 {
		t.Fatalf("expected port 443, got %d", n.Port)
	}
	if n.TLS == nil || !n.TLS.Enabled || n.TLS.ServerName != "proxy.example.com" {
		t.Fatalf("expected tls enabled for https, got %#v", n.TLS)
	}
}

func TestParseURICapabilityMatrix(t *testing.T) {
	tests := []struct {
		name        string
		uri         string
		wantSupport bool
	}{
		{"ws", "vless://uuid@example.com:443?type=ws", true},
		{"grpc", "vless://uuid@example.com:443?type=grpc", true},
		{"http", "vless://uuid@example.com:443?type=http", true},
		{"httpupgrade", "vless://uuid@example.com:443?type=httpupgrade", true},
		{"quic", "vless://uuid@example.com:443?type=quic", true},
		{"xhttp", "vless://uuid@example.com:443?type=xhttp", false},
		{"splithttp", "vless://uuid@example.com:443?type=splithttp", false},
		{"h2", "vless://uuid@example.com:443?type=h2", false},
		{"tcp", "vless://uuid@example.com:443?type=tcp", true},
		{"none", "vless://uuid@example.com:443?type=none", true},
		{"raw", "vless://uuid@example.com:443?type=raw", true},
		{"tcpheader", "vless://uuid@example.com:443?type=tcpheader", true},
		{"empty", "vless://uuid@example.com:443", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, err := ParseURI(tt.uri)
			if err != nil {
				t.Fatalf("ParseURI returned error: %v", err)
			}
			if tt.name == "tcp" || tt.name == "none" || tt.name == "raw" || tt.name == "tcpheader" || tt.name == "empty" {
				if n.Transport != nil {
					t.Fatalf("expected Transport=nil for %q, got %#v", tt.uri, n.Transport)
				}
			}
			if n.Supported != tt.wantSupport {
				t.Fatalf("Supported = %v, want %v (reason=%q)", n.Supported, tt.wantSupport, n.UnsupportedReason)
			}
			if !tt.wantSupport && !strings.Contains(n.UnsupportedReason, "not supported") {
				t.Fatalf("UnsupportedReason = %q, want contains 'not supported'", n.UnsupportedReason)
			}
		})
	}
}

func TestParseURIVmessNetAliasesDowngrade(t *testing.T) {
	// 回归：vmess net=none/raw/tcpheader 等价裸 TCP（tcpAliases）→ Transport=nil, Supported=true
	for _, netType := range []string{"none", "raw", "tcpheader"} {
		raw := "vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"v":"2","ps":"x","add":"example.com","port":443,"id":"abc","net":"`+netType+`"}`))
		n, err := ParseURI(raw)
		if err != nil {
			t.Fatalf("ParseURI(%s) returned error: %v", netType, err)
		}
		if n.Transport != nil {
			t.Fatalf("net=%s: expected Transport=nil, got %#v", netType, n.Transport)
		}
		if !n.Supported {
			t.Fatalf("net=%s: expected Supported=true, got %q", netType, n.UnsupportedReason)
		}
	}
}

func TestParseURIClashRejected(t *testing.T) {
	// C1：clash:// 自定义格式已删除，走 default 报错
	raw := "clash://" + base64.StdEncoding.EncodeToString([]byte(`{"name":"demo","type":"ss","server":"example.com","port":8388}`))
	_, err := ParseURI(raw)
	if err == nil {
		t.Fatal("expected error for clash:// URI, got nil")
	}
}

func TestParseURI_WebsocketEarlyData(t *testing.T) {
	rawVless := "vless://12345678-1234-1234-1234-123456789012@example.com:443?type=ws&ed=2560&early_data_header_name=Sec-WebSocket-Protocol"
	n, err := ParseURI(rawVless)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if n.Transport == nil || n.Transport.Type != "ws" {
		t.Fatalf("expected ws transport, got %#v", n.Transport)
	}
	if n.Transport.MaxEarlyData != 2560 {
		t.Fatalf("MaxEarlyData = %d, want 2560", n.Transport.MaxEarlyData)
	}
	if n.Transport.EarlyDataHeaderName != "Sec-WebSocket-Protocol" {
		t.Fatalf("EarlyDataHeaderName = %q, want Sec-WebSocket-Protocol", n.Transport.EarlyDataHeaderName)
	}

	rawVMessJSON := `{"v":"2","ps":"demo","add":"example.com","port":443,"id":"12345678-1234-1234-1234-123456789012","net":"ws","path":"/ws","max_early_data":"2048","early_data_header_name":"Sec-WebSocket-Protocol"}`
	rawVMess := vmessURIFromJSON(t, rawVMessJSON)
	nVMess, err := ParseURI(rawVMess)
	if err != nil {
		t.Fatalf("ParseURI VMess returned error: %v", err)
	}
	if nVMess.Transport == nil || nVMess.Transport.Type != "ws" {
		t.Fatalf("expected ws transport, got %#v", nVMess.Transport)
	}
	if nVMess.Transport.MaxEarlyData != 2048 {
		t.Fatalf("VMess MaxEarlyData = %d, want 2048", nVMess.Transport.MaxEarlyData)
	}
	if nVMess.Transport.EarlyDataHeaderName != "Sec-WebSocket-Protocol" {
		t.Fatalf("VMess EarlyDataHeaderName = %q, want Sec-WebSocket-Protocol", nVMess.Transport.EarlyDataHeaderName)
	}
}

func TestParseURIUnsupportedProtocol(t *testing.T) {
	_, err := ParseURI("unknown://user@example.com:443")
	if err == nil {
		t.Fatal("expected error for unknown protocol, got nil")
	}
}

func TestParseVless_ECH(t *testing.T) {
	const base = "vless://12345678-1234-1234-1234-123456789012@example.com:443?"
	cases := []struct {
		name      string
		query     string
		pubName   string
		configURL string
	}{
		{
			name:      "完整格式",
			query:     "security=tls&sni=real.example.com&ech=cloudflare-ech.com%2Bhttps%3A%2F%2Fdns.alidns.com%2Fdns-query",
			pubName:   "cloudflare-ech.com",
			configURL: "https://dns.alidns.com/dns-query",
		},
		{
			name:    "仅公钥名",
			query:   "security=tls&sni=real.example.com&ech=cloudflare-ech.com",
			pubName: "cloudflare-ech.com",
		},
		{
			name:  "无ech",
			query: "security=tls&sni=real.example.com",
		},
		{
			name:  "reality互斥",
			query: "security=reality&sni=real.example.com&pbk=pubkey&ech=cloudflare-ech.com",
		},
		{
			name:  "含空白非法",
			query: "security=tls&sni=real.example.com&ech=a%20b",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n, err := ParseURI(base + tc.query)
			if err != nil {
				t.Fatalf("ParseURI returned error: %v", err)
			}
			if tc.pubName == "" {
				if n.TLS != nil && n.TLS.ECH != nil {
					t.Fatalf("expected TLS.ECH == nil, got %#v", n.TLS.ECH)
				}
				return
			}
			if n.TLS == nil || n.TLS.ECH == nil {
				t.Fatalf("expected TLS.ECH set, got TLS=%#v", n.TLS)
			}
			if n.TLS.ECH.PublicName != tc.pubName {
				t.Fatalf("PublicName = %q, want %q", n.TLS.ECH.PublicName, tc.pubName)
			}
			if n.TLS.ECH.ConfigURL != tc.configURL {
				t.Fatalf("ConfigURL = %q, want %q", n.TLS.ECH.ConfigURL, tc.configURL)
			}
		})
	}
}

func TestParseTrojan_ECH(t *testing.T) {
	raw := "trojan://password@example.com:443?ech=cloudflare-ech.com%2Bhttps%3A%2F%2Fdns.alidns.com%2Fdns-query"
	n, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if n.TLS == nil || n.TLS.ECH == nil {
		t.Fatalf("expected TLS.ECH set, got TLS=%#v", n.TLS)
	}
	if n.TLS.ECH.PublicName != "cloudflare-ech.com" {
		t.Fatalf("PublicName = %q, want cloudflare-ech.com", n.TLS.ECH.PublicName)
	}
	if n.TLS.ECH.ConfigURL != "https://dns.alidns.com/dns-query" {
		t.Fatalf("ConfigURL = %q, want https://dns.alidns.com/dns-query", n.TLS.ECH.ConfigURL)
	}
}
