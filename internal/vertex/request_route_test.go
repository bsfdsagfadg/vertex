package vertex

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/db"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/recaptcha"
)

func TestRequestTokenStateSharesOneFetch(t *testing.T) {
	var fetches atomic.Int32
	pool := recaptcha.NewTokenPoolCustomContext(func(_ context.Context, proxyURI string) (string, error) {
		fetches.Add(1)
		if proxyURI != "socks5://token-route" {
			t.Fatalf("unexpected token route: %q", proxyURI)
		}
		time.Sleep(20 * time.Millisecond)
		return "shared-token", nil
	})
	state := &requestTokenState{proxyURI: "socks5://token-route"} //nolint:exhaustruct

	const callers = 12
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := state.get(context.Background(), pool)
			if err == nil && token != "shared-token" {
				err = errors.New("caller did not receive the shared token")
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := fetches.Load(); got != 1 {
		t.Fatalf("token fetched %d times, want 1", got)
	}
}

func TestRequestTokenStateSharesFetchFailure(t *testing.T) {
	var fetches atomic.Int32
	pool := recaptcha.NewTokenPoolCustomContext(func(context.Context, string) (string, error) {
		fetches.Add(1)
		time.Sleep(20 * time.Millisecond)
		return "", errors.New("fetch failed")
	})
	state := &requestTokenState{proxyURI: "socks5://token-route"} //nolint:exhaustruct

	const callers = 12
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := state.get(context.Background(), pool); err == nil {
				t.Error("expected shared fetch error")
			}
		}()
	}
	wg.Wait()
	if got := fetches.Load(); got != 1 {
		t.Fatalf("failed token fetch repeated %d times, want 1", got)
	}
}

func TestRequestTokenStateFetchHonorsCancellation(t *testing.T) {
	started := make(chan struct{})
	pool := recaptcha.NewTokenPoolCustomContext(func(ctx context.Context, _ string) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	})
	state := &requestTokenState{} //nolint:exhaustruct
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := state.get(ctx, pool)
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("token fetch error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("token fetch did not stop after request cancellation")
	}
}

func TestRequestTokenStateWaiterHonorsCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	pool := recaptcha.NewTokenPoolCustomContext(func(context.Context, string) (string, error) {
		close(started)
		<-release
		return "shared-token", nil
	})
	state := &requestTokenState{} //nolint:exhaustruct
	ownerDone := make(chan error, 1)
	go func() {
		_, err := state.get(context.Background(), pool)
		ownerDone <- err
	}()
	<-started

	waiterCtx, cancelWaiter := context.WithCancel(context.Background())
	waiterDone := make(chan error, 1)
	go func() {
		_, err := state.get(waiterCtx, pool)
		waiterDone <- err
	}()
	cancelWaiter()
	select {
	case err := <-waiterDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiting caller error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiting caller did not stop after request cancellation")
	}
	close(release)
	if err := <-ownerDone; err != nil {
		t.Fatal(err)
	}
}

func TestRequestTokenStateIgnoresStaleInvalidation(t *testing.T) {
	var fetches atomic.Int32
	pool := recaptcha.NewTokenPoolCustomContext(func(context.Context, string) (string, error) {
		if fetches.Add(1) == 1 {
			return "token-1", nil
		}
		return "token-2", nil
	})
	state := &requestTokenState{} //nolint:exhaustruct
	first, err := state.get(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if !state.invalidate(first) {
		t.Fatal("first refresh was unexpectedly rejected")
	}
	second, err := state.get(context.Background(), pool)
	if err != nil || second != "token-2" {
		t.Fatalf("refresh failed: token=%q err=%v", second, err)
	}
	if !state.invalidate(first) {
		t.Fatal("stale invalidation should observe the refreshed generation")
	}
	stillSecond, err := state.get(context.Background(), pool)
	if err != nil || stillSecond != second || fetches.Load() != 2 {
		t.Fatalf("stale invalidation discarded refreshed token: token=%q err=%v fetches=%d", stillSecond, err, fetches.Load())
	}
}

func TestRequestTokenStateRefreshesAtMostOnce(t *testing.T) {
	var fetches atomic.Int32
	pool := recaptcha.NewTokenPoolCustomContext(func(context.Context, string) (string, error) {
		return "token-" + string(rune('0'+fetches.Add(1))), nil
	})
	state := &requestTokenState{} //nolint:exhaustruct
	first, err := state.get(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if !state.invalidate(first) {
		t.Fatal("first refresh was rejected")
	}
	second, err := state.get(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if state.invalidate(second) {
		t.Fatal("second refresh in the same request must be rejected")
	}
	if got, err := state.get(context.Background(), pool); err != nil || got != second || fetches.Load() != 2 {
		t.Fatalf("refresh limit changed token state: token=%q err=%v fetches=%d", got, err, fetches.Load())
	}
}

func TestPrepareRequestFallsBackWithoutClearingEntryCooldown(t *testing.T) {
	db.CloseDB()
	if err := db.InitDB(filepath.Join(t.TempDir(), "request-route.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.CloseDB)

	entryURI := "socks5://127.0.0.1:18080#entry"
	if _, err := config.AddProxyCandidate(entryURI); err != nil {
		t.Fatal(err)
	}
	fallbackURI := "socks5://127.0.0.1:18081#healthy"
	nodes.MergeNodes([]nodes.Node{{Type: "socks5", Name: "healthy", RawURI: fallbackURI}})
	t.Cleanup(func() { nodes.DeleteNode(fallbackURI) })

	var mu sync.Mutex
	var routes []string
	pool := recaptcha.NewTokenPoolCustomContext(func(_ context.Context, proxyURI string) (string, error) {
		mu.Lock()
		routes = append(routes, proxyURI)
		mu.Unlock()
		if proxyURI == entryURI {
			return "", errors.New("entry unavailable")
		}
		return "fallback-token", nil
	})
	cfg := config.DefaultConfig()
	client := &VertexAIClient{pool: pool, cfg: config.StaticProvider(cfg)} //nolint:exhaustruct
	routedCtx, err := client.prepareRequest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 || routes[0] != entryURI || routes[1] == "" || routes[1] == entryURI {
		t.Fatalf("unexpected token acquisition routes: %#v", routes)
	}
	route := routeFromContext(routedCtx)
	if route == nil || route.entryURI != entryURI || route.token.proxyURI != routes[1] {
		t.Fatalf("request route was not fixed correctly: %+v", route)
	}
	items := config.ListProxyCandidates()
	if len(items) != 1 || items[0].CooldownUntil <= time.Now().Unix() {
		t.Fatalf("entry cooldown was cleared by fallback success: %+v", items)
	}
}
