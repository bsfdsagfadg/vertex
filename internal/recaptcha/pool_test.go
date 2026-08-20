package recaptcha

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/transport"
)

// TestTokenPoolRealtime 验证每次 GetToken 都实时获取，且 Start/Stop 不阻塞、Stats 返回 0,0。
func TestTokenPoolRealtime(t *testing.T) {
	var calls int32
	p := NewTokenPoolCustom(func(_ string) (string, error) {
		n := atomic.AddInt32(&calls, 1)
		return fmt.Sprintf("tok-%d", n), nil
	})

	p.Start()
	if size, fill := p.Stats(); size != 0 || fill != 0 {
		t.Fatalf("Stats 应为 0,0，got %d,%d", size, fill)
	}

	for i := 1; i <= 3; i++ {
		tok, err := p.GetToken()
		if err != nil || tok == "" {
			t.Fatalf("第 %d 次 GetToken 失败：tok=%q err=%v", i, tok, err)
		}
		if int(atomic.LoadInt32(&calls)) != i {
			t.Fatalf("应每次实时获取，期望 %d 次，实际 %d", i, calls)
		}
	}

	p.Stop() // 不应阻塞
}

func TestTokenPoolSharedStartsFiveRoutesImmediately(t *testing.T) {
	var started atomic.Int32
	allStarted := make(chan struct{})
	p := &TokenPool{
		fetch: func(ctx context.Context, proxyURI string) (string, error) {
			if started.Add(1) == sharedTokenConcurrency {
				close(allStarted)
			}
			select {
			case <-allStarted:
			case <-ctx.Done():
				return "", ctx.Err()
			}
			if proxyURI == "route-3" {
				return "winner", nil
			}
			<-ctx.Done()
			return "", ctx.Err()
		},
		routes: func(context.Context) []string {
			return []string{"route-1", "route-2", "route-3", "route-4", "route-5"}
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	token, err := p.GetTokenSharedContext(ctx)
	if err != nil || token != "winner" {
		t.Fatalf("并发 token 竞速失败: token=%q err=%v", token, err)
	}
	if got := started.Load(); got != sharedTokenConcurrency {
		t.Fatalf("应立即启动 %d 路完整请求，实际 %d", sharedTokenConcurrency, got)
	}
}

func TestTokenPoolSharedCapsRoutesAtFive(t *testing.T) {
	var calls atomic.Int32
	p := &TokenPool{
		fetch: func(context.Context, string) (string, error) {
			calls.Add(1)
			return "", errors.New("failed")
		},
		routes: func(context.Context) []string {
			return []string{"1", "2", "3", "4", "5", "6"}
		},
	}
	if _, err := p.GetTokenSharedContext(context.Background()); err == nil {
		t.Fatal("全部路线失败时应返回错误")
	}
	if got := calls.Load(); got != sharedTokenConcurrency {
		t.Fatalf("并发上限应为 %d，实际调用 %d", sharedTokenConcurrency, got)
	}
}

func TestTokenPoolSharedDoesNotSingleflightCallers(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	p := &TokenPool{
		fetch: func(context.Context, string) (string, error) {
			n := calls.Add(1)
			<-release
			return fmt.Sprintf("token-%d", n), nil
		},
		routes: func(context.Context) []string { return []string{"route"} },
	}

	var wg sync.WaitGroup
	wg.Add(2)
	for range 2 {
		go func() {
			defer wg.Done()
			if _, err := p.GetTokenSharedContext(context.Background()); err != nil {
				t.Errorf("独立 token 获取失败: %v", err)
			}
		}()
	}
	deadline := time.Now().Add(time.Second)
	for calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := calls.Load(); got != 2 {
		close(release)
		wg.Wait()
		t.Fatalf("两个调用不应被 singleflight 合并，fetch 次数=%d", got)
	}
	close(release)
	wg.Wait()
}

func TestTokenPoolContextCancellation(t *testing.T) {
	p := NewTokenPoolCustomContext(func(ctx context.Context, _ string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.GetTokenWithProxyContext(ctx, "test-proxy")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消应传播到 token 获取函数: %v", err)
	}
}

func TestTokenPoolRouteFetchDoesNotUseSharedRace(t *testing.T) {
	want := transport.Route{GlobalProxyURI: "socks5://global.example:1080", RequestNodeURI: "vless://node@request.example:443"}
	var got transport.Route
	var sharedCalled bool
	p := &TokenPool{
		fetchRoute: func(_ context.Context, route transport.Route) (string, error) {
			got = route
			return "token", nil
		},
		routes: func(context.Context) []string {
			sharedCalled = true
			return []string{"wrong"}
		},
	}
	token, err := p.GetTokenWithRouteContext(context.Background(), want)
	if err != nil || token != "token" {
		t.Fatalf("route token=%q err=%v", token, err)
	}
	if got != want || sharedCalled {
		t.Fatalf("route affinity lost: got=%+v sharedCalled=%t", got, sharedCalled)
	}
}

func TestTokenPoolUsesDynamicDefaultButExplicitEmptyMeansDirect(t *testing.T) {
	currentProxy := "http://proxy-a.example:7890"
	var gotProxy string
	p := &TokenPool{
		fetch: func(_ context.Context, proxyURI string) (string, error) {
			gotProxy = proxyURI
			return "token", nil
		},
		defaultProxy: func(context.Context) string { return currentProxy },
	}

	if _, err := p.GetTokenContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotProxy != currentProxy {
		t.Fatalf("默认 token 获取应读取当前代理，got %q", gotProxy)
	}

	currentProxy = "http://proxy-b.example:7890"
	if _, err := p.GetTokenContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotProxy != currentProxy {
		t.Fatalf("代理热切换后应读取新值，got %q", gotProxy)
	}

	if _, err := p.GetTokenWithProxyContext(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if gotProxy != "" {
		t.Fatalf("显式空代理应直连，不能回退当前全局代理，got %q", gotProxy)
	}
}
