package transport

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/constant"
)

func TestProxyDialerPoolSingleflight(t *testing.T) {
	pool := NewProxyDialerPool(nil)
	defer pool.StopAll()

	var buildCount atomic.Int32
	pool.builder = func(mapping map[string]any, options ...adapter.ProxyOption) (constant.Proxy, error) {
		buildCount.Add(1)
		time.Sleep(30 * time.Millisecond)
		return adapter.ParseProxy(mapping, options...)
	}

	key := NewProxyInstanceKey("socks5://127.0.0.1:1080#sf-test")
	const callers = 10
	var wg sync.WaitGroup
	errs := make(chan error, callers)

	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := pool.GetDialer(context.Background(), key, "req", false)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("GetDialer error: %v", err)
		}
	}
	if got := buildCount.Load(); got != 1 {
		t.Fatalf("expected 1 build, got %d", got)
	}
}

func TestProxyDialerPoolRemove(t *testing.T) {
	pool := NewProxyDialerPool(nil)
	defer pool.StopAll()

	uri := "socks5://127.0.0.1:1080#rem-test"
	key := NewProxyInstanceKey(uri)
	_, err := pool.GetDialer(context.Background(), key, "req", false)
	if err != nil {
		t.Fatalf("GetDialer error: %v", err)
	}

	pool.mu.RLock()
	count := len(pool.proxies)
	pool.mu.RUnlock()
	if count != 1 {
		t.Fatalf("expected 1 proxy in pool, got %d", count)
	}

	pool.Remove("socks5://127.0.0.1:1080#other-name")

	pool.mu.RLock()
	count = len(pool.proxies)
	pool.mu.RUnlock()
	if count != 0 {
		t.Fatalf("expected 0 proxies after Remove, got %d", count)
	}
}

func TestProxyDialerPoolIdleGC(t *testing.T) {
	pool := NewProxyDialerPool(nil)
	defer pool.StopAll()

	uri := "socks5://127.0.0.1:1080#gc-test"
	key := NewProxyInstanceKey(uri)
	_, err := pool.GetDialer(context.Background(), key, "req", false)
	if err != nil {
		t.Fatalf("GetDialer error: %v", err)
	}

	pool.mu.Lock()
	for _, entry := range pool.proxies {
		entry.lastUsedAt = time.Now().Add(-10 * time.Minute)
	}
	pool.mu.Unlock()

	pool.CleanupIdle(5 * time.Minute)

	pool.mu.RLock()
	count := len(pool.proxies)
	pool.mu.RUnlock()
	if count != 0 {
		t.Fatalf("expected idle proxy to be collected, remaining: %d", count)
	}
}

func TestProxyDialerPoolTwoHopEntryProxyError(t *testing.T) {
	pool := NewProxyDialerPool(nil)
	defer pool.StopAll()

	invalidEntry := "invalid-scheme://bad"
	targetNode := "socks5://127.0.0.1:1080#target"
	key := NewProxyInstanceKey(targetNode, invalidEntry)

	_, err := pool.GetDialer(context.Background(), key, "req", false)
	if err == nil {
		t.Fatal("expected error with invalid entry proxy, got nil")
	}

	var entryErr *EntryProxyError
	if !errors.As(err, &entryErr) {
		t.Fatalf("expected EntryProxyError, got %T: %v", err, err)
	}
	if entryErr.EntryURI != invalidEntry {
		t.Fatalf("expected EntryURI %q, got %q", invalidEntry, entryErr.EntryURI)
	}
}
