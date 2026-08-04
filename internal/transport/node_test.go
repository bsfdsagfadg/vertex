package transport

import (
	"fmt"
	"strings"
	"testing"
)

func TestApplyCapability(t *testing.T) {
	tests := []struct {
		name        string
		transport   *TransportOptions
		wantSupport bool
	}{
		{name: "nil transport (bare tcp)", wantSupport: true},
		{name: "ws", transport: &TransportOptions{Type: "ws"}, wantSupport: true},
		{name: "httpupgrade", transport: &TransportOptions{Type: "httpupgrade"}, wantSupport: true},
		{name: "quic", transport: &TransportOptions{Type: "quic"}, wantSupport: true},
		{name: "grpc", transport: &TransportOptions{Type: "grpc"}, wantSupport: true},
		{name: "http", transport: &TransportOptions{Type: "http"}, wantSupport: true},
		{name: "xhttp", transport: &TransportOptions{Type: "xhttp"}, wantSupport: false},
		{name: "splithttp", transport: &TransportOptions{Type: "splithttp"}, wantSupport: false},
		{name: "h2", transport: &TransportOptions{Type: "h2"}, wantSupport: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &ParsedNode{Transport: tt.transport}
			applyCapability(n)
			if n.Supported != tt.wantSupport {
				t.Fatalf("Supported = %v, want %v", n.Supported, tt.wantSupport)
			}
			if !tt.wantSupport && !strings.Contains(n.UnsupportedReason, "not supported") {
				t.Fatalf("UnsupportedReason = %q, want contains 'not supported'", n.UnsupportedReason)
			}
		})
	}
}

func TestTCPAliasesRegistry(t *testing.T) {
	// tcpAliases 覆盖解析期应降级为 Transport=nil 的全部类型；
	// 该清单与 applyCapability 的 supportedTransports 互不重叠。
	want := map[string]bool{"tcp": true, "none": true, "raw": true, "tcpheader": true, "": true}
	for alias := range tcpAliases {
		if !want[alias] {
			t.Fatalf("unexpected tcp alias %q", alias)
		}
	}
	for alias := range want {
		if !tcpAliases[alias] {
			t.Fatalf("missing tcp alias %q", alias)
		}
		if supportedTransports[alias] {
			t.Fatalf("tcp alias %q must not be in supportedTransports", alias)
		}
	}
}

func TestIRCache(t *testing.T) {
	// 清空缓存，避免测试间污染
	ClearParsedNodeCache()

	uri := "vless://uuid@example.com:443"
	n := &ParsedNode{RawURI: uri, Type: "vless", Supported: true}

	CacheParsedNode(n)
	if got := peekCache(uri); got != n {
		t.Fatalf("peekCache after CacheParsedNode = %p, want %p", got, n)
	}

	InvalidateParsedNode(uri)
	if got := peekCache(uri); got != nil {
		t.Fatalf("peekCache after InvalidateParsedNode = %p, want nil", got)
	}

	CacheParsedNode(nil)
	if got := peekCache("vless://other@example.com:443"); got != nil {
		t.Fatalf("nil cache entry created: %#v", got)
	}
}

func TestIRCacheMaxRebuild(t *testing.T) {
	ClearParsedNodeCache()
	for i := 0; i < irCacheMax+10; i++ {
		CacheParsedNode(&ParsedNode{RawURI: fmt.Sprintf("socks5://h%d.example.com:1080", i), Type: "socks5", Supported: true})
	}
	irCacheMu.RLock()
	size := len(irCache)
	irCacheMu.RUnlock()
	if size >= irCacheMax {
		t.Fatalf("cache not rebuilt on overflow: size=%d max=%d", size, irCacheMax)
	}
	ClearParsedNodeCache()
	irCacheMu.RLock()
	size = len(irCache)
	irCacheMu.RUnlock()
	if size != 0 {
		t.Fatalf("ClearParsedNodeCache left %d entries", size)
	}
}
