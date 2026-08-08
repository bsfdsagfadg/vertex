package transport

import (
	"encoding/base64"
	"encoding/json"
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

func TestGetOrStartProxyDialerForwardsEntryURI(t *testing.T) {
	StopAllProxies()
	t.Cleanup(StopAllProxies)

	entryURI := "socks5://127.0.0.1:1080#entry"
	secondURI := "http://127.0.0.1:8080#second"
	if _, err := getOrStartProxyDialer(secondURI, "test", false, entryURI); err != nil {
		t.Fatalf("build proxy chain through wrapper: %v", err)
	}
	proxyMutex.RLock()
	_, chained := proxyMap[proxyCacheKey(secondURI, entryURI)]
	_, unchained := proxyMap[proxyCacheKey(secondURI, "")]
	proxyMutex.RUnlock()
	if !chained || unchained {
		t.Fatalf("entry URI was not forwarded: chained=%v unchained=%v", chained, unchained)
	}
}

func TestProxyChainCacheUsesNormalizedTwoHopIdentity(t *testing.T) {
	StopAllProxies()
	t.Cleanup(StopAllProxies)

	entryURI := "socks5://127.0.0.1:1080#entry"
	secondURI := "http://127.0.0.1:8080#second"
	var buildCount atomic.Int32
	builder := func(mapping map[string]any, options ...adapter.ProxyOption) (constant.Proxy, error) {
		buildCount.Add(1)
		return adapter.ParseProxy(mapping, options...)
	}
	if _, err := getOrStartProxyDialerWithBuilder(secondURI, "test", false, builder, entryURI); err != nil {
		t.Fatal(err)
	}
	if _, err := getOrStartProxyDialerWithBuilder("HTTP://127.0.0.1:8080#renamed", "test", false, builder, "SOCKS5://127.0.0.1:1080#renamed-entry"); err != nil {
		t.Fatal(err)
	}
	if got := buildCount.Load(); got != 2 {
		t.Fatalf("normalized URI variants initialized %d proxies, want 2", got)
	}
	RemoveProxy("socks5://127.0.0.1:1080#different-label")
	proxyMutex.RLock()
	defer proxyMutex.RUnlock()
	if len(proxyMap) != 0 {
		t.Fatalf("normalized entry removal left cached chains: %d", len(proxyMap))
	}
}

func TestProxyChainRejectsSelfReference(t *testing.T) {
	StopAllProxies()
	t.Cleanup(StopAllProxies)

	uri := "socks5://127.0.0.1:1080#entry"
	if _, err := getOrStartProxyDialerWithBuilder(uri, "test", false, func(mapping map[string]any, options ...adapter.ProxyOption) (constant.Proxy, error) {
		return adapter.ParseProxy(mapping, options...)
	}, "SOCKS5://127.0.0.1:1080#candidate"); err == nil {
		t.Fatal("self-referencing proxy chain was accepted")
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
