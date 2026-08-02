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
	builder := func(mapping map[string]any) (constant.Proxy, error) {
		buildCount.Add(1)
		time.Sleep(25 * time.Millisecond)
		return adapter.ParseProxy(mapping)
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
	builder := func(mapping map[string]any) (constant.Proxy, error) {
		close(started)
		<-release
		return adapter.ParseProxy(mapping)
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

func testClashProxyURI(t *testing.T, mapping map[string]any) string {
	t.Helper()
	body, err := json.Marshal(mapping)
	if err != nil {
		t.Fatalf("marshal clash proxy: %v", err)
	}
	return "clash://" + base64.StdEncoding.EncodeToString(body)
}
