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

func TestInvalidateParsedNodesBatch(t *testing.T) {
	ClearParsedNodeCache()
	uris := []string{"socks5://a:1080", "socks5://b:1080", "socks5://c:1080"}
	for _, u := range uris {
		CacheParsedNode(&ParsedNode{RawURI: u, Type: "socks5", Supported: true})
	}
	InvalidateParsedNodesBatch([]string{uris[0], uris[1]})
	if got := peekCache(uris[0]); got != nil {
		t.Fatalf("batch invalidate left %s in cache", uris[0])
	}
	if got := peekCache(uris[1]); got != nil {
		t.Fatalf("batch invalidate left %s in cache", uris[1])
	}
	if got := peekCache(uris[2]); got == nil {
		t.Fatalf("batch invalidate must not touch %s", uris[2])
	}
	// 空列表与不存在 URI 均幂等
	InvalidateParsedNodesBatch(nil)
	InvalidateParsedNodesBatch([]string{"socks5://ghost:1080"})
	ClearParsedNodeCache()
}

func TestCheckSupportedBatch(t *testing.T) {
	ClearParsedNodeCache()
	supportedURI := "vless://uuid@example.com:443"
	unsupportedURI := "vless://uuid@example.com:443?type=xhttp"
	CacheParsedNode(&ParsedNode{RawURI: supportedURI, Type: "vless", Supported: true})

	got := CheckSupportedBatch([]string{supportedURI, unsupportedURI, "unknown://bad"})
	if !got[supportedURI] {
		t.Errorf("supportedURI 应判定支持, got %v", got)
	}
	if got[unsupportedURI] {
		t.Errorf("unsupportedURI 应判定不支持, got %v", got)
	}
	if got["unknown://bad"] {
		t.Errorf("解析失败 URI 应判定不支持, got %v", got)
	}
	// 空列表不 panic
	if got := CheckSupportedBatch(nil); len(got) != 0 {
		t.Errorf("空列表应返回空 map, got %v", got)
	}
	// 补算结果应已写入缓存
	if got := peekCache(unsupportedURI); got == nil {
		t.Error("补算的 unsupportedURI 应已缓存")
	}
	ClearParsedNodeCache()
}

func TestPrewarmURIs(t *testing.T) {
	ClearParsedNodeCache()
	uris := []string{
		"vless://uuid@example.com:443",
		"socks5://user:pass@example.com:1080",
		"vless://uuid2@example.com:443",
		"unknown://bad-scheme",
	}
	PrewarmURIs(uris)
	for _, u := range uris[:3] {
		if got := peekCache(u); got == nil {
			t.Errorf("PrewarmURIs 后 %s 应已缓存", u)
		}
	}
	if got := peekCache(uris[3]); got != nil {
		t.Errorf("解析失败的 URI 不应缓存: %s", uris[3])
	}
	// 二次预热：全部命中应无新增解析（幂等）
	PrewarmURIs(uris)
	ClearParsedNodeCache()
	// 空列表幂等
	PrewarmURIs(nil)
}
