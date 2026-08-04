package transport

import (
	"encoding/base64"
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

func TestParseURIHy2KeepsPortRange(t *testing.T) {
	raw := "hy2://secret@203.10.99.51:20000?sni=www.bing.com&insecure=1&ports=20000-55000#demo"

	n, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}

	// C15：hy2 端口归一为 "a:b" 格式
	if len(n.ServerPorts) != 1 || n.ServerPorts[0] != "20000:55000" {
		t.Fatalf("expected ServerPorts [20000:55000], got %#v", n.ServerPorts)
	}
	if n.TLS == nil || n.TLS.ServerName != "www.bing.com" {
		t.Fatalf("expected sni www.bing.com, got %#v", n.TLS)
	}
	if n.TLS.Insecure != true {
		t.Fatalf("expected insecure=true, got %#v", n.TLS)
	}
	if n.Password != "secret" {
		t.Fatalf("expected password secret, got %q", n.Password)
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

func TestParseURSocks5h(t *testing.T) {
	raw := "socks5h://admin:secret@10.0.0.1:1080#socks5h"
	n, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if n.Type != "socks5" {
		t.Fatalf("expected type socks5, got %q", n.Type)
	}
}

func TestParseURSocks(t *testing.T) {
	raw := "socks://user:pass@10.0.0.1:1080#socks"
	n, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if n.Type != "socks5" {
		t.Fatalf("expected type socks5, got %q", n.Type)
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

func TestParseURISSR(t *testing.T) {
	ssrDecoded := "1.2.3.4:1234:origin:aes-256-cfb:tls1.2_ticket_auth:dGVzdHBhc3M=?remarks=my-ssr"
	raw := "ssr://" + base64.StdEncoding.EncodeToString([]byte(ssrDecoded))

	n, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if n.Type != "ssr" {
		t.Fatalf("expected type ssr, got %q", n.Type)
	}
	if n.Name != "my-ssr" {
		t.Fatalf("expected name my-ssr, got %q", n.Name)
	}
	if n.Server != "1.2.3.4" {
		t.Fatalf("expected server 1.2.3.4, got %q", n.Server)
	}
	if n.Port != 1234 {
		t.Fatalf("expected port 1234, got %d", n.Port)
	}
	if n.Cipher != "aes-256-cfb" {
		t.Fatalf("expected cipher aes-256-cfb, got %q", n.Cipher)
	}
	if n.Password != "testpass" {
		t.Fatalf("expected password testpass, got %q", n.Password)
	}
	if n.Protocol != "origin" {
		t.Fatalf("expected protocol origin, got %q", n.Protocol)
	}
	if n.Obfs != "tls1.2_ticket_auth" {
		t.Fatalf("expected obfs tls1.2_ticket_auth, got %q", n.Obfs)
	}
}

func TestParseURISSRParamKeyCompat(t *testing.T) {
	// C5：三键兼容读（obfsparam|obfs_param|obfs-param、protoparam|protocol_param|protocol-param）
	tests := []struct {
		name       string
		paramQuery string
		wantObfs   string
		wantProto  string
	}{
		{"underscore keys", "?obfs_param=op2&protocol_param=pp2", "op2", "pp2"},
		{"canonical keys", "?obfsparam=op1&protoparam=pp1", "op1", "pp1"},
		{"dash keys", "?obfs-param=op3&protocol-param=pp3", "op3", "pp3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decoded := "1.2.3.4:1234:origin:aes-256-cfb:plain:dGVzdHBhc3M=" + tt.paramQuery
			raw := "ssr://" + base64.StdEncoding.EncodeToString([]byte(decoded))
			n, err := ParseURI(raw)
			if err != nil {
				t.Fatalf("ParseURI returned error: %v", err)
			}
			if n.ObfsParam != tt.wantObfs {
				t.Fatalf("ObfsParam = %q, want %q", n.ObfsParam, tt.wantObfs)
			}
			if n.ProtocolParam != tt.wantProto {
				t.Fatalf("ProtocolParam = %q, want %q", n.ProtocolParam, tt.wantProto)
			}
		})
	}
}

func TestParseURIShadowsocksr(t *testing.T) {
	ssrDecoded := "10.0.0.1:443:auth_aes128_md5:chacha20-ietf:http_simple:MTIzNDU2Nzg5MA==?remarks=ssr-node"
	raw := "shadowsocksr://" + base64.StdEncoding.EncodeToString([]byte(ssrDecoded))

	n, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if n.Type != "ssr" {
		t.Fatalf("expected type ssr, got %q", n.Type)
	}
	if n.Name != "ssr-node" {
		t.Fatalf("expected name ssr-node, got %q", n.Name)
	}
	if n.Password != "1234567890" {
		t.Fatalf("expected password 1234567890, got %q", n.Password)
	}
}

func TestParseURIHysteria(t *testing.T) {
	raw := "hysteria://host.example.com:443?auth=mysecret&obfs=xplus&sni=sni.example.com&insecure=1&alpn=h3#hysteria-node"
	n, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if n.Type != "hysteria" {
		t.Fatalf("expected type hysteria, got %q", n.Type)
	}
	if n.Name != "hysteria-node" {
		t.Fatalf("expected name hysteria-node, got %q", n.Name)
	}
	if n.Server != "host.example.com" {
		t.Fatalf("expected server host.example.com, got %q", n.Server)
	}
	if n.Port != 443 {
		t.Fatalf("expected port 443, got %d", n.Port)
	}
	if n.AuthString != "mysecret" {
		t.Fatalf("expected auth_str mysecret, got %q", n.AuthString)
	}
	if n.TLS == nil || !n.TLS.Enabled {
		t.Fatalf("expected tls enabled, got %#v", n.TLS)
	}
	if n.TLS.ServerName != "sni.example.com" {
		t.Fatalf("expected sni sni.example.com, got %q", n.TLS.ServerName)
	}
	if n.Obfs != "xplus" {
		t.Fatalf("expected obfs xplus, got %q", n.Obfs)
	}
	if !n.TLS.Insecure {
		t.Fatalf("expected insecure=true")
	}
	if len(n.TLS.ALPN) != 1 || n.TLS.ALPN[0] != "h3" {
		t.Fatalf("expected alpn [h3], got %#v", n.TLS.ALPN)
	}
}

func TestParseURIAnyTLS(t *testing.T) {
	raw := "anytls://mypassword@host.example.com:443#my-anytls"
	n, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if n.Type != "anytls" {
		t.Fatalf("expected type anytls, got %q", n.Type)
	}
	if n.Name != "my-anytls" {
		t.Fatalf("expected name my-anytls, got %q", n.Name)
	}
	if n.Server != "host.example.com" {
		t.Fatalf("expected server host.example.com, got %q", n.Server)
	}
	if n.Port != 443 {
		t.Fatalf("expected port 443, got %d", n.Port)
	}
	if n.Password != "mypassword" {
		t.Fatalf("expected password mypassword, got %q", n.Password)
	}
	if n.TLS == nil || !n.TLS.Enabled {
		t.Fatalf("expected tls enabled, got %#v", n.TLS)
	}
	if n.TLS.ServerName != "host.example.com" {
		t.Fatalf("expected sni host.example.com, got %q", n.TLS.ServerName)
	}
}

func TestParseURITuicPeerPriority(t *testing.T) {
	// tuic：sni 缺失时 peer 优先（对齐 builder）
	raw := "tuic://uuid:pass@x.example.com:443?peer=peer.example.com"
	n, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if n.TLS == nil || n.TLS.ServerName != "peer.example.com" {
		t.Fatalf("expected ServerName peer.example.com, got %#v", n.TLS)
	}
	if n.UUID != "uuid" || n.Password != "pass" {
		t.Fatalf("expected uuid/password, got uuid=%q password=%q", n.UUID, n.Password)
	}
}

func TestParseURITuicCongestionControl(t *testing.T) {
	raw := "tuic://uuid@x.example.com:443?congestion_control=bbr&udp_relay_mode=native"
	n, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if n.CongestionControl != "bbr" {
		t.Fatalf("expected congestion_control bbr, got %q", n.CongestionControl)
	}
	if n.UDPRelayMode != "native" {
		t.Fatalf("expected udp_relay_mode native, got %q", n.UDPRelayMode)
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
			if !tt.wantSupport && !stringsContains(n.UnsupportedReason, "not supported") {
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

func TestParseURISSH(t *testing.T) {
	for _, tt := range []struct {
		name string
		uri  string
		user string
		pass string
		pk   string
		psk  string
	}{
		{"password auth", "ssh://root:secret@203.0.113.9:22#ssh-1", "root", "secret", "", ""},
		{"key auth", "ssh://tunnel@203.0.113.9:2222?pk=QUJDRA%3D%3D&psk=pp", "tunnel", "", "QUJDRA==", "pp"},
		{"default port", "ssh://root@203.0.113.9", "root", "", "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			n, err := ParseURI(tt.uri)
			if err != nil {
				t.Fatalf("ParseURI returned error: %v", err)
			}
			if n.Type != "ssh" || n.Server != "203.0.113.9" {
				t.Fatalf("unexpected node: type=%q server=%q", n.Type, n.Server)
			}
			if n.Username != tt.user || n.Password != tt.pass {
				t.Fatalf("expected user=%q pass=%q, got %q/%q", tt.user, tt.pass, n.Username, n.Password)
			}
			if n.SSHPrivateKey != tt.pk || n.SSHPrivateKeyPassphrase != tt.psk {
				t.Fatalf("expected pk=%q psk=%q, got %q/%q", tt.pk, tt.psk, n.SSHPrivateKey, n.SSHPrivateKeyPassphrase)
			}
			if !n.Supported {
				t.Fatalf("expected Supported=true, got %q", n.UnsupportedReason)
			}
		})
	}
}

func TestParseURIUnsupportedProtocol(t *testing.T) {
	_, err := ParseURI("unknown://user@example.com:443")
	if err == nil {
		t.Fatal("expected error for unknown protocol, got nil")
	}
}

func stringsContains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || indexOf(s, substr) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
