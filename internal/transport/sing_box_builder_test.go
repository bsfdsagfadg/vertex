package transport

import (
	"net/url"
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

func TestParseV2RayTransport_Downgrade(t *testing.T) {
	for _, typ := range []string{"tcp", "none", "raw", "tcpheader"} {
		t.Run(typ, func(t *testing.T) {
			q := url.Values{}
			q.Set("type", typ)
			tr := parseV2RayTransport(q)
			if tr != nil {
				t.Fatalf("parseV2RayTransport(%q) = %+v, want nil", typ, tr)
			}
		})
	}
}

func TestParseV2RayTransport_Passthrough(t *testing.T) {
	q := url.Values{}
	q.Set("type", "xhttp")
	tr := parseV2RayTransport(q)
	if tr == nil {
		t.Fatal("expected non-nil transport for xhttp passthrough")
	}
	if tr.Type != "xhttp" {
		t.Fatalf("tr.Type = %q, want %q", tr.Type, "xhttp")
	}
}

func TestParseV2RayTransport_CaseAndSpaces(t *testing.T) {
	q := url.Values{}
	q.Set("type", "  WS ")
	tr := parseV2RayTransport(q)
	if tr == nil || tr.Type != "ws" {
		t.Fatalf("got %+v, want ws transport", tr)
	}
}

func TestParseTLSOptions_RealityDefaultFingerprint(t *testing.T) {
	q := url.Values{}
	q.Set("security", "reality")
	q.Set("pbk", "public-key-value")
	tlsOpts := parseTLSOptions(q, "example.com", false)
	if tlsOpts == nil {
		t.Fatal("expected non-nil TLS options for reality")
	}
	if tlsOpts.Reality == nil || !tlsOpts.Reality.Enabled {
		t.Fatal("expected reality enabled")
	}
	if tlsOpts.UTLS == nil || !tlsOpts.UTLS.Enabled {
		t.Fatalf("expected UTLS enabled, got %+v", tlsOpts.UTLS)
	}
	if tlsOpts.UTLS.Fingerprint != "chrome" {
		t.Fatalf("fingerprint = %q, want %q", tlsOpts.UTLS.Fingerprint, "chrome")
	}
}

func TestBuildHysteria2Outbound_PortRange(t *testing.T) {
	u, _ := url.Parse("hysteria2://password@example.com:443")
	q := url.Values{}
	q.Set("ports", "50000-53000")
	opts, err := buildHysteria2Outbound(u, q)
	if err != nil {
		t.Fatalf("buildHysteria2Outbound unexpected error: %v", err)
	}
	if len(opts.ServerPorts) != 1 || opts.ServerPorts[0] != "50000:53000" {
		t.Fatalf("ServerPorts = %v, want [50000:53000]", opts.ServerPorts)
	}
}

func TestBuildHysteria2Outbound_PortList(t *testing.T) {
	u, _ := url.Parse("hysteria2://password@example.com:443")
	q := url.Values{}
	q.Set("mport", "30000,30002")
	opts, err := buildHysteria2Outbound(u, q)
	if err != nil {
		t.Fatalf("buildHysteria2Outbound unexpected error: %v", err)
	}
	want := []string{"30000:30000", "30002:30002"}
	if len(opts.ServerPorts) != len(want) {
		t.Fatalf("ServerPorts = %v, want %v", opts.ServerPorts, want)
	}
	for i := range want {
		if opts.ServerPorts[i] != want[i] {
			t.Fatalf("ServerPorts[%d] = %q, want %q", i, opts.ServerPorts[i], want[i])
		}
	}
}

func TestBuildHysteria2Outbound_InvalidPortRange(t *testing.T) {
	u, _ := url.Parse("hysteria2://password@example.com:443")
	for _, raw := range []string{"invalid", "70000-80000", "50000-1000", "-1", "abc-def"} {
		q := url.Values{}
		q.Set("ports", raw)
		opts, err := buildHysteria2Outbound(u, q)
		if err != nil {
			t.Fatalf("buildHysteria2Outbound(%q) unexpected error: %v", raw, err)
		}
		if len(opts.ServerPorts) != 0 {
			t.Fatalf("ServerPorts for %q = %v, want empty (fallback to main port)", raw, opts.ServerPorts)
		}
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
