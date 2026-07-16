package transport

import (
	"testing"
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
