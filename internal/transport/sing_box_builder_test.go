package transport

import (
	"encoding/base64"
	"net/url"
	"strings"
	"testing"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
)

func TestBuildOutbound_Success(t *testing.T) {
	tests := []struct {
		name string
		uri  string
	}{
		{"vless", "vless://12345678-1234-1234-1234-123456789012@example.com:443"},
		{"vmess", "vmess://eyJhZGQiOiJleGFtcGxlLmNvbSIsInBvcnQiOiI0NDMiLCJpZCI6IjEyMzQ1Njc4LTEyMzQtMTIzNC0xMjM0LTEyMzQ1Njc4OTAxMiIsImFpZCI6IjAiLCJuZXQiOiJ0Y3AiLCJ0eXBlIjoibm9uZSIsImhvc3QiOiIifQ=="},
		{"shadowsocks", "ss://YWVzLTEyOC1nY206cGFzc3dvcmQ@example.com:443"},
		{"trojan", "trojan://password@example.com:443"},
		{"hysteria2", "hysteria2://password@example.com:443"},
		{"tuic", "tuic://uuid:password@example.com:443"},
		{"socks5", "socks5://user:pass@example.com:1080"},
		{"socks5h", "socks5h://user:pass@example.com:1080"},
		{"socks", "socks://user:pass@example.com:1080"},
		{"http", "http://user:pass@example.com:8080"},
		{"https", "https://example.com:443"},
		{"hysteria", "hysteria://example.com:443?protocol=udp"},
		{"anytls", "anytls://password@example.com:443"},
		{"hy2", "hy2://password@example.com:443"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ob, err := buildOutbound(tt.uri)
			if err != nil {
				t.Fatalf("buildOutbound(%q) unexpected error: %v", tt.uri, err)
			}
			if ob.Tag == "" {
				t.Fatal("expected non-empty Tag")
			}
		})
	}
}

func TestBuildOutbound_UnknownProtocol(t *testing.T) {
	_, err := buildOutbound("unknown://user@example.com:443")
	if err == nil {
		t.Fatal("expected error for unknown protocol, got nil")
	}
}

func TestNormalizeURI(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"socks5://127.0.0.1:1080", "socks5://127.0.0.1:1080"},
		{"vless://uuid@example.com:443", "vless://uuid@example.com:443"},
		{"vless://uuid@example.com:443/", "vless://uuid@example.com:443"},
		{"  vless://uuid@example.com:443  ", "vless://uuid@example.com:443"},
		{"", ""},
		{"  ", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeURI(tt.input)
			if got != tt.expected {
				t.Fatalf("normalizeURI(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestBuildOutbound_InvalidURI(t *testing.T) {
	tests := []struct {
		name string
		uri  string
	}{
		{"vless no uuid", "vless://example.com:443"},
		{"vmess invalid base64", "vmess://invalid"},
		{"ss no method", "ss://example.com:443"},
		{"trojan no password", "trojan://example.com:443"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := buildOutbound(tt.uri)
			if err == nil {
				t.Fatal("expected error for invalid URI, got nil")
			}
		})
	}
}

func TestBuildOutbound_UnsupportedTransport(t *testing.T) {
	_, err := buildOutbound("vless://12345678-1234-1234-1234-123456789012@example.com:443?type=xhttp")
	if err == nil {
		t.Fatal("expected error for unsupported transport, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected unsupported error, got: %v", err)
	}
}

func TestBuildOutbound_ParseErrorPassthrough(t *testing.T) {
	_, err := buildOutbound("vmess://not-base64!")
	if err == nil {
		t.Fatal("expected parse error to pass through, got nil")
	}
}

func TestBuildOutboundFromNode_AllProtocols(t *testing.T) {
	tests := []struct {
		name     string
		uri      string
		wantType string
	}{
		{"vless", "vless://12345678-1234-1234-1234-123456789012@example.com:443", C.TypeVLESS},
		{"vmess", "vmess://eyJhZGQiOiJleGFtcGxlLmNvbSIsInBvcnQiOiI0NDMiLCJpZCI6IjEyMzQ1Njc4LTEyMzQtMTIzNC0xMjM0LTEyMzQ1Njc4OTAxMiIsImFpZCI6IjAiLCJuZXQiOiJ0Y3AiLCJ0eXBlIjoibm9uZSIsImhvc3QiOiIifQ==", C.TypeVMess},
		{"ss", "ss://YWVzLTEyOC1nY206cGFzc3dvcmQ@example.com:443", C.TypeShadowsocks},
		{"trojan", "trojan://password@example.com:443", C.TypeTrojan},
		{"hysteria2", "hysteria2://password@example.com:443", C.TypeHysteria2},
		{"tuic", "tuic://uuid:password@example.com:443", C.TypeTUIC},
		{"socks5", "socks5://user:pass@example.com:1080", C.TypeSOCKS},
		{"http", "http://user:pass@example.com:8080", C.TypeHTTP},
		{"https", "https://example.com:443", C.TypeHTTP},
		{"hysteria", "hysteria://example.com:443?protocol=udp", C.TypeHysteria},
		{"anytls", "anytls://password@example.com:443", C.TypeAnyTLS},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, err := ParseURI(tt.uri)
			if err != nil {
				t.Fatalf("ParseURI(%q) unexpected error: %v", tt.uri, err)
			}
			ob, err := buildOutboundFromNode(n)
			if err != nil {
				t.Fatalf("buildOutboundFromNode(%q) unexpected error: %v", tt.uri, err)
			}
			if ob.Type != tt.wantType {
				t.Fatalf("type = %q, want %q", ob.Type, tt.wantType)
			}
			if ob.Options == nil {
				t.Fatal("expected non-nil Options")
			}
		})
	}
}

func TestBuildOutboundFromNode_TCPAliases(t *testing.T) {
	for _, typ := range []string{"tcp", "none", "raw", "tcpheader", ""} {
		t.Run(typ, func(t *testing.T) {
			q := url.Values{}
			q.Set("type", typ)
			uri := "vless://12345678-1234-1234-1234-123456789012@example.com:443"
			if typ != "" {
				uri += "?" + q.Encode()
			}
			n, err := ParseURI(uri)
			if err != nil {
				t.Fatalf("ParseURI unexpected error: %v", err)
			}
			if n.Transport != nil {
				t.Fatalf("type %q: expected nil Transport, got %+v", typ, n.Transport)
			}
			ob, err := buildOutboundFromNode(n)
			if err != nil {
				t.Fatalf("buildOutboundFromNode unexpected error: %v", err)
			}
			opts := ob.Options.(*option.VLESSOutboundOptions)
			if opts.Transport != nil {
				t.Fatalf("type %q: expected nil outbound Transport, got %+v", typ, opts.Transport)
			}
		})
	}
}

func TestBuildOutboundFromNode_WSTransport(t *testing.T) {
	n, err := ParseURI("vless://12345678-1234-1234-1234-123456789012@example.com:443?type=ws&path=/ws&host=cdn.example.com")
	if err != nil {
		t.Fatalf("ParseURI unexpected error: %v", err)
	}
	ob, err := buildOutboundFromNode(n)
	if err != nil {
		t.Fatalf("buildOutboundFromNode unexpected error: %v", err)
	}
	opts := ob.Options.(*option.VLESSOutboundOptions)
	if opts.Transport == nil || opts.Transport.Type != "ws" {
		t.Fatalf("expected ws transport, got %+v", opts.Transport)
	}
	if opts.Transport.WebsocketOptions.Path != "/ws" {
		t.Fatalf("ws path = %q, want /ws", opts.Transport.WebsocketOptions.Path)
	}
	host := opts.Transport.WebsocketOptions.Headers["Host"]
	if len(host) != 1 || host[0] != "cdn.example.com" {
		t.Fatalf("ws Host header = %v, want [cdn.example.com]", host)
	}
}

func TestBuildOutboundFromNode_HTTPTransport(t *testing.T) {
	n, err := ParseURI("vless://12345678-1234-1234-1234-123456789012@example.com:443?type=http&path=/http&host=h.example.com&method=POST")
	if err != nil {
		t.Fatalf("ParseURI unexpected error: %v", err)
	}
	ob, err := buildOutboundFromNode(n)
	if err != nil {
		t.Fatalf("buildOutboundFromNode unexpected error: %v", err)
	}
	opts := ob.Options.(*option.VLESSOutboundOptions)
	if opts.Transport == nil || opts.Transport.Type != "http" {
		t.Fatalf("expected http transport, got %+v", opts.Transport)
	}
	if opts.Transport.HTTPOptions.Path != "/http" {
		t.Fatalf("http path = %q, want /http", opts.Transport.HTTPOptions.Path)
	}
	if opts.Transport.HTTPOptions.Method != "POST" {
		t.Fatalf("http method = %q, want POST", opts.Transport.HTTPOptions.Method)
	}
	host := opts.Transport.HTTPOptions.Host
	if len(host) != 1 || host[0] != "h.example.com" {
		t.Fatalf("http Host = %v, want [h.example.com]", host)
	}
}

func TestBuildOutboundFromNode_HTTPUpgradeTransport(t *testing.T) {
	n, err := ParseURI("vless://12345678-1234-1234-1234-123456789012@example.com:443?type=httpupgrade&path=/up&host=hu.example.com")
	if err != nil {
		t.Fatalf("ParseURI unexpected error: %v", err)
	}
	ob, err := buildOutboundFromNode(n)
	if err != nil {
		t.Fatalf("buildOutboundFromNode unexpected error: %v", err)
	}
	opts := ob.Options.(*option.VLESSOutboundOptions)
	if opts.Transport == nil || opts.Transport.Type != "httpupgrade" {
		t.Fatalf("expected httpupgrade transport, got %+v", opts.Transport)
	}
	if opts.Transport.HTTPUpgradeOptions.Path != "/up" {
		t.Fatalf("httpupgrade path = %q, want /up", opts.Transport.HTTPUpgradeOptions.Path)
	}
	if opts.Transport.HTTPUpgradeOptions.Host != "hu.example.com" {
		t.Fatalf("httpupgrade host = %q, want hu.example.com", opts.Transport.HTTPUpgradeOptions.Host)
	}
}

func TestBuildOutboundFromNode_QUICTransport(t *testing.T) {
	n, err := ParseURI("vless://12345678-1234-1234-1234-123456789012@example.com:443?type=quic")
	if err != nil {
		t.Fatalf("ParseURI unexpected error: %v", err)
	}
	ob, err := buildOutboundFromNode(n)
	if err != nil {
		t.Fatalf("buildOutboundFromNode unexpected error: %v", err)
	}
	opts := ob.Options.(*option.VLESSOutboundOptions)
	if opts.Transport == nil || opts.Transport.Type != "quic" {
		t.Fatalf("expected quic transport, got %+v", opts.Transport)
	}
	if opts.Transport.QUICOptions != (option.V2RayQUICOptions{}) {
		t.Fatalf("expected empty QUICOptions, got %+v", opts.Transport.QUICOptions)
	}
}

func TestBuildOutboundFromNode_RealityDefaultFingerprint(t *testing.T) {
	n, err := ParseURI("vless://12345678-1234-1234-1234-123456789012@example.com:443?security=reality&pbk=public-key-value&sid=abcd")
	if err != nil {
		t.Fatalf("ParseURI unexpected error: %v", err)
	}
	ob, err := buildOutboundFromNode(n)
	if err != nil {
		t.Fatalf("buildOutboundFromNode unexpected error: %v", err)
	}
	opts := ob.Options.(*option.VLESSOutboundOptions)
	if opts.TLS == nil || opts.TLS.Reality == nil || !opts.TLS.Reality.Enabled {
		t.Fatalf("expected reality enabled, got %+v", opts.TLS)
	}
	if opts.TLS.Reality.PublicKey != "public-key-value" {
		t.Fatalf("reality public key = %q, want public-key-value", opts.TLS.Reality.PublicKey)
	}
	if opts.TLS.UTLS == nil || opts.TLS.UTLS.Fingerprint != "chrome" {
		t.Fatalf("fingerprint = %+v, want chrome", opts.TLS.UTLS)
	}
}

func TestBuildOutboundFromNode_SSPlugin(t *testing.T) {
	n, err := ParseURI("ss://YWVzLTEyOC1nY206cGFzc3dvcmQ@example.com:443?plugin=simple-obfs%3Bobfs%3Dhttp%3Bobfs-host%3Dcdn.example.com")
	if err != nil {
		t.Fatalf("ParseURI unexpected error: %v", err)
	}
	ob, err := buildOutboundFromNode(n)
	if err != nil {
		t.Fatalf("buildOutboundFromNode unexpected error: %v", err)
	}
	opts := ob.Options.(*option.ShadowsocksOutboundOptions)
	if opts.Plugin != "simple-obfs" {
		t.Fatalf("plugin = %q, want simple-obfs", opts.Plugin)
	}
	if opts.PluginOptions != "obfs=http;obfs-host=cdn.example.com" {
		t.Fatalf("plugin opts = %q, want obfs=http;obfs-host=cdn.example.com", opts.PluginOptions)
	}
}

func TestBuildOutboundFromNode_SSRParams(t *testing.T) {
	raw := "example.com:443:auth_sha1_v4:aes-128-cfb:tls1.2_ticket_auth:" +
		base64.StdEncoding.EncodeToString([]byte("secret")) +
		"?obfsparam=abc&protoparam=xyz"
	uri := "ssr://" + base64.StdEncoding.EncodeToString([]byte(raw))

	n, err := ParseURI(uri)
	if err != nil {
		t.Fatalf("ParseURI unexpected error: %v", err)
	}
	if n.Type != "ssr" {
		t.Fatalf("type = %q, want ssr", n.Type)
	}
	ob, err := buildOutboundFromNode(n)
	if err != nil {
		t.Fatalf("buildOutboundFromNode unexpected error: %v", err)
	}
	opts := ob.Options.(*option.ShadowsocksROutboundOptions)
	if opts.Method != "aes-128-cfb" || opts.Password != "secret" {
		t.Fatalf("ssr credentials = %q/%q", opts.Method, opts.Password)
	}
	if opts.Obfs != "tls1.2_ticket_auth" || opts.Protocol != "auth_sha1_v4" {
		t.Fatalf("ssr obfs/protocol = %q/%q", opts.Obfs, opts.Protocol)
	}
	if opts.ObfsParam != "abc" || opts.ProtocolParam != "xyz" {
		t.Fatalf("ssr params = %q/%q, want abc/xyz", opts.ObfsParam, opts.ProtocolParam)
	}
}

func TestBuildOutboundFromNode_Hysteria2Ports(t *testing.T) {
	n, err := ParseURI("hysteria2://password@example.com:443?ports=50000-53000")
	if err != nil {
		t.Fatalf("ParseURI unexpected error: %v", err)
	}
	ob, err := buildOutboundFromNode(n)
	if err != nil {
		t.Fatalf("buildOutboundFromNode unexpected error: %v", err)
	}
	opts := ob.Options.(*option.Hysteria2OutboundOptions)
	if len(opts.ServerPorts) != 1 || opts.ServerPorts[0] != "50000:53000" {
		t.Fatalf("ServerPorts = %v, want [50000:53000]", opts.ServerPorts)
	}

	n2, err := ParseURI("hysteria2://password@example.com:443?mport=30000,30002")
	if err != nil {
		t.Fatalf("ParseURI unexpected error: %v", err)
	}
	ob2, err := buildOutboundFromNode(n2)
	if err != nil {
		t.Fatalf("buildOutboundFromNode unexpected error: %v", err)
	}
	opts2 := ob2.Options.(*option.Hysteria2OutboundOptions)
	want := []string{"30000:30000", "30002:30002"}
	if len(opts2.ServerPorts) != len(want) {
		t.Fatalf("ServerPorts = %v, want %v", opts2.ServerPorts, want)
	}
	for i := range want {
		if opts2.ServerPorts[i] != want[i] {
			t.Fatalf("ServerPorts[%d] = %q, want %q", i, opts2.ServerPorts[i], want[i])
		}
	}
}

func TestBuildOutboundFromNode_Hysteria2InvalidPorts(t *testing.T) {
	for _, raw := range []string{"invalid", "70000-80000", "50000-1000", "-1", "abc-def"} {
		t.Run(raw, func(t *testing.T) {
			n, err := ParseURI("hysteria2://password@example.com:443?ports=" + url.QueryEscape(raw))
			if err != nil {
				t.Fatalf("ParseURI unexpected error: %v", err)
			}
			ob, err := buildOutboundFromNode(n)
			if err != nil {
				t.Fatalf("buildOutboundFromNode unexpected error: %v", err)
			}
			opts := ob.Options.(*option.Hysteria2OutboundOptions)
			if len(opts.ServerPorts) != 0 {
				t.Fatalf("ServerPorts for %q = %v, want empty (fallback to main port)", raw, opts.ServerPorts)
			}
		})
	}
}

func TestBuildOutboundFromNode_Hysteria2Obfs(t *testing.T) {
	n, err := ParseURI("hysteria2://password@example.com:443?obfs=salamander&obfs-password=obfspwd")
	if err != nil {
		t.Fatalf("ParseURI unexpected error: %v", err)
	}
	ob, err := buildOutboundFromNode(n)
	if err != nil {
		t.Fatalf("buildOutboundFromNode unexpected error: %v", err)
	}
	opts := ob.Options.(*option.Hysteria2OutboundOptions)
	if opts.Obfs == nil || opts.Obfs.Type != "salamander" || opts.Obfs.Password != "obfspwd" {
		t.Fatalf("obfs = %+v, want salamander/obfspwd", opts.Obfs)
	}
}

func TestBuildOutboundFromNode_Hysteria2BBRDefault(t *testing.T) {
	n, err := ParseURI("hysteria2://password@example.com:443")
	if err != nil {
		t.Fatalf("ParseURI unexpected error: %v", err)
	}
	ob, err := buildOutboundFromNode(n)
	if err != nil {
		t.Fatalf("buildOutboundFromNode unexpected error: %v", err)
	}
	opts := ob.Options.(*option.Hysteria2OutboundOptions)
	if opts.UpMbps != 0 || opts.DownMbps != 0 {
		t.Fatalf("UpMbps/DownMbps = %d/%d, want 0/0 (BBR default)", opts.UpMbps, opts.DownMbps)
	}
}

func TestBuildOutboundFromNode_WebsocketEarlyData(t *testing.T) {
	n, err := ParseURI("vless://12345678-1234-1234-1234-123456789012@example.com:443?type=ws&ed=2560&early_data_header_name=Sec-WebSocket-Protocol")
	if err != nil {
		t.Fatalf("ParseURI unexpected error: %v", err)
	}
	ob, err := buildOutboundFromNode(n)
	if err != nil {
		t.Fatalf("buildOutboundFromNode unexpected error: %v", err)
	}
	opts := ob.Options.(*option.VLESSOutboundOptions)
	if opts.Transport == nil || opts.Transport.Type != "ws" {
		t.Fatalf("expected ws transport, got %+v", opts.Transport)
	}
	wsOpts := opts.Transport.WebsocketOptions
	if wsOpts.MaxEarlyData != 2560 {
		t.Fatalf("MaxEarlyData = %d, want 2560", wsOpts.MaxEarlyData)
	}
	if wsOpts.EarlyDataHeaderName != "Sec-WebSocket-Protocol" {
		t.Fatalf("EarlyDataHeaderName = %q, want Sec-WebSocket-Protocol", wsOpts.EarlyDataHeaderName)
	}
}

func TestNormalizeSSMethod(t *testing.T) {
	tests := map[string]string{
		"chacha20-poly1305":       "chacha20-ietf-poly1305",
		"chacha20poly1305":        "chacha20-ietf-poly1305",
		"chacha20-ietf":           "chacha20-ietf-poly1305",
		"aes-128-poly1305":        "aes-128-gcm",
		"aes-256-poly1305":        "aes-256-gcm",
		"aes-256-gcm":             "aes-256-gcm",
		"CHACHA20-POLY1305":       "chacha20-ietf-poly1305",
	}
	for in, want := range tests {
		if got := normalizeSSMethod(in); got != want {
			t.Fatalf("normalizeSSMethod(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildShadowsocks_PlainCredentials(t *testing.T) {
	uris := []string{
		"ss://chacha20-poly1305:password@example.com:443",
		"ss://aes-256-gcm:password@example.com:443",
	}
	for _, uri := range uris {
		ob, err := buildOutbound(uri)
		if err != nil {
			t.Fatalf("buildOutbound(%q) unexpected error: %v", uri, err)
		}
		if ob.Type != C.TypeShadowsocks {
			t.Fatalf("type = %v, want %v", ob.Type, C.TypeShadowsocks)
		}
		opts, ok := ob.Options.(*option.ShadowsocksOutboundOptions)
		if !ok {
			t.Fatalf("expected ShadowsocksOutboundOptions, got %T", ob.Options)
		}
		if opts.Password != "password" {
			t.Fatalf("password = %q, want %q", opts.Password, "password")
		}
	}
}

func TestBuildShadowsocks_PlainMethodNormalized(t *testing.T) {
	ob, err := buildOutbound("ss://chacha20-poly1305:password@example.com:443")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	opts := ob.Options.(*option.ShadowsocksOutboundOptions)
	if opts.Method != "chacha20-ietf-poly1305" {
		t.Fatalf("method = %q, want chacha20-ietf-poly1305", opts.Method)
	}
}

func TestBuildSOCKS_NoPassword(t *testing.T) {
	ob, err := buildOutbound("socks://Tag@example.com:1080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	opts := ob.Options.(*option.SOCKSOutboundOptions)
	if opts.Username != "" || opts.Password != "" {
		t.Fatalf("expected empty credentials, got user=%q pass=%q", opts.Username, opts.Password)
	}
	if opts.Version != "" {
		t.Fatalf("expected empty Version for socks5, got %q", opts.Version)
	}
}

func TestBuildSOCKS4_VersionPassthrough(t *testing.T) {
	for _, tt := range []struct {
		uri     string
		version string
	}{
		{"socks4://203.0.113.7:4145", "4"},
		{"socks4a://203.0.113.7:4145", "4a"},
		{"socks5://203.0.113.7:4145", ""},
	} {
		ob, err := buildOutbound(tt.uri)
		if err != nil {
			t.Fatalf("buildOutbound(%s) returned error: %v", tt.uri, err)
		}
		opts := ob.Options.(*option.SOCKSOutboundOptions)
		if opts.Version != tt.version {
			t.Fatalf("Version for %s = %q, want %q", tt.uri, opts.Version, tt.version)
		}
		if ob.Type != C.TypeSOCKS {
			t.Fatalf("type = %v, want %v", ob.Type, C.TypeSOCKS)
		}
	}
}

func TestBuildSSH_Params(t *testing.T) {
	ob, err := buildOutbound("ssh://tunnel:secret@203.0.113.9:2222?pk=QUJDRA%3D%3D&psk=pp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ob.Type != C.TypeSSH {
		t.Fatalf("type = %v, want %v", ob.Type, C.TypeSSH)
	}
	opts := ob.Options.(*option.SSHOutboundOptions)
	if opts.User != "tunnel" || opts.Password != "secret" {
		t.Fatalf("expected user/pass, got %q/%q", opts.User, opts.Password)
	}
	if len(opts.PrivateKey) != 1 || opts.PrivateKey[0] != "QUJDRA==" {
		t.Fatalf("expected private key passthrough, got %#v", opts.PrivateKey)
	}
	if opts.PrivateKeyPassphrase != "pp" {
		t.Fatalf("expected passphrase pp, got %q", opts.PrivateKeyPassphrase)
	}
}

func TestBuildSSH_PasswordOnly(t *testing.T) {
	ob, err := buildOutbound("ssh://root@203.0.113.9:22")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	opts := ob.Options.(*option.SSHOutboundOptions)
	if opts.User != "root" || opts.Password != "" {
		t.Fatalf("expected user root with empty password, got %q/%q", opts.User, opts.Password)
	}
	if len(opts.PrivateKey) != 0 {
		t.Fatalf("expected no private key, got %#v", opts.PrivateKey)
	}
}

func TestBuildHTTP_NoPassword(t *testing.T) {
	ob, err := buildOutbound("http://Tag@example.com:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	opts := ob.Options.(*option.HTTPOutboundOptions)
	if opts.Username != "" || opts.Password != "" {
		t.Fatalf("expected empty credentials, got user=%q pass=%q", opts.Username, opts.Password)
	}
}
