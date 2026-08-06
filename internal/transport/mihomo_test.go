package transport

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/constant"
)

func TestGetOrStartProxyDialerInitializesSameURIOnce(t *testing.T) {
	StopAllProxies()
	t.Cleanup(StopAllProxies)

	uri := testClashProxyURI(t, map[string]any{
		"name":   "concurrent-http",
		"type":   "http",
		"server": "127.0.0.1",
		"port":   7890,
	})
	var buildCount atomic.Int32
	builder := func(mapping map[string]any, options ...adapter.ProxyOption) (constant.Proxy, error) {
		buildCount.Add(1)
		time.Sleep(25 * time.Millisecond)
		return adapter.ParseProxy(mapping, options...)
	}

	const callers = 16
	start := make(chan struct{})
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := getOrStartProxyDialerWithBuilder(uri, "test", false, builder)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("getOrStartProxyDialerWithBuilder returned error: %v", err)
		}
	}
	if got := buildCount.Load(); got != 1 {
		t.Fatalf("expected one proxy initialization, got %d", got)
	}
}

func TestRemoveProxyCancelsInFlightInitialization(t *testing.T) {
	StopAllProxies()
	t.Cleanup(StopAllProxies)

	uri := testClashProxyURI(t, map[string]any{
		"name":   "removed-http",
		"type":   "http",
		"server": "127.0.0.1",
		"port":   7890,
	})
	started := make(chan struct{})
	release := make(chan struct{})
	builder := func(mapping map[string]any, options ...adapter.ProxyOption) (constant.Proxy, error) {
		close(started)
		<-release
		return adapter.ParseProxy(mapping, options...)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := getOrStartProxyDialerWithBuilder(uri, "test", false, builder)
		errCh <- err
	}()
	<-started
	RemoveProxy(uri)
	close(release)

	if err := <-errCh; err == nil {
		t.Fatal("expected removed proxy initialization to be canceled")
	}
	proxyMutex.RLock()
	_, cached := proxyMap[uri]
	proxyMutex.RUnlock()
	if cached {
		t.Fatal("removed proxy was inserted into the cache after initialization completed")
	}
}

func TestProxyChainBuildsEntryAndSecondHop(t *testing.T) {
	StopAllProxies()
	t.Cleanup(StopAllProxies)

	entryURI := "socks5://127.0.0.1:1080#entry"
	secondURI := "http://127.0.0.1:8080#second"
	var buildCount atomic.Int32
	var chained atomic.Bool
	builder := func(mapping map[string]any, options ...adapter.ProxyOption) (constant.Proxy, error) {
		buildCount.Add(1)
		if len(options) > 0 {
			chained.Store(true)
		}
		return adapter.ParseProxy(mapping, options...)
	}

	if _, err := getOrStartProxyDialerWithBuilder(secondURI, "test", false, builder, entryURI); err != nil {
		t.Fatalf("build proxy chain: %v", err)
	}
	if buildCount.Load() != 2 || !chained.Load() {
		t.Fatalf("expected entry and chained second hop, builds=%d chained=%v", buildCount.Load(), chained.Load())
	}
	if _, err := getOrStartProxyDialerWithBuilder(secondURI, "test", false, builder, entryURI); err != nil {
		t.Fatalf("reuse proxy chain: %v", err)
	}
	if buildCount.Load() != 2 {
		t.Fatalf("expected cached proxy chain, builds=%d", buildCount.Load())
	}
}

func TestValidateNodeURI(t *testing.T) {
	ssWhole := func(method string) string {
		return "ss://" + base64.StdEncoding.EncodeToString([]byte(method+":secret")) + "@1.2.3.4:8388"
	}
	cases := []struct {
		name        string
		uri         string
		wantEmpty   bool
		wantKeyword string
	}{
		{"socks4 显式 unsupported", "socks4://1.2.3.4:1080#s4", false, "socks4"},
		{"ssh 缺密码 unsupported", "ssh://user@ssh.example.com:22", false, "ssh"},
		{"vless 合法", "vless://12345678-1234-1234-1234-123456789012@example.com:443#demo", true, ""},
		{"ss chacha20-poly1305 归一后可用", ssWhole("chacha20-poly1305"), true, ""},
		{"ss chacha20-ietf-poly1305 可用", ssWhole("chacha20-ietf-poly1305"), true, ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := ValidateNodeURI(testCase.uri)
			if testCase.wantEmpty {
				if got != "" {
					t.Fatalf("ValidateNodeURI(%s) 应可用（空串），got %q", testCase.uri, got)
				}
				return
			}
			if got == "" {
				t.Fatalf("ValidateNodeURI(%s) 应返回非空原因", testCase.uri)
			}
			if !strings.Contains(got, testCase.wantKeyword) {
				t.Fatalf("ValidateNodeURI(%s) 原因应包含 %q，got %q", testCase.uri, testCase.wantKeyword, got)
			}
		})
	}
}

func testClashProxyURI(t *testing.T, mapping map[string]any) string {
	t.Helper()
	body, err := json.Marshal(mapping)
	if err != nil {
		t.Fatalf("marshal clash proxy: %v", err)
	}
	return "clash://" + base64.StdEncoding.EncodeToString(body)
}
