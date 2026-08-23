package recaptcha

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/infra/config"
)

// TestTokenPoolNoCache 验证 30s 全局缓存已移除：
// 每次顺序调用 GetTokenShared 都必须发起新的抓取，返回全新 token。
func TestTokenPoolNoCache(t *testing.T) {
	var calls int32
	p := &TokenPool{fetch: func(_ string) (string, error) {
		n := atomic.AddInt32(&calls, 1)
		return fmt.Sprintf("tok-%d", n), nil
	}}

	// 第一次调用 -> fetch 1
	tok1, err := p.GetTokenShared(context.Background())
	if err != nil || tok1 != "tok-1" {
		t.Fatalf("Expected tok-1, got tok=%q err=%v", tok1, err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("Expected 1 fetch call, got %d", calls)
	}

	// 第二次调用必须重新抓取（无缓存），返回 tok-2
	tok2, err := p.GetTokenShared(context.Background())
	if err != nil || tok2 != "tok-2" {
		t.Fatalf("Expected fresh tok-2, got tok=%q err=%v", tok2, err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("Expected 2 fetch calls (no cache), got %d", calls)
	}

	tok3, err := p.GetTokenShared(context.Background())
	if err != nil || tok3 != "tok-3" {
		t.Fatalf("Expected tok-3, got tok=%q err=%v", tok3, err)
	}
	if atomic.LoadInt32(&calls) != 3 {
		t.Fatalf("Expected 3 fetch calls, got %d", calls)
	}
}

// TestTokenPoolConcurrentFresh 验证移除 singleflight 后，并发调用各自抓取独立 token：
// reCAPTCHA token 是单次的，每个并发请求必须持有互不相同的 token。
func TestTokenPoolConcurrentFresh(t *testing.T) {
	var calls int32
	p := &TokenPool{fetch: func(_ string) (string, error) {
		n := atomic.AddInt32(&calls, 1)
		return fmt.Sprintf("tok-%d", n), nil
	}}

	var wg sync.WaitGroup
	toks := make([]string, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			tok, err := p.GetTokenShared(context.Background())
			if err == nil {
				toks[idx] = tok
			}
		}(i)
	}
	wg.Wait()

	if atomic.LoadInt32(&calls) != 10 {
		t.Fatalf("Expected 10 independent fetch calls, got %d", calls)
	}
	seen := make(map[string]bool)
	for i, tok := range toks {
		if tok == "" {
			t.Errorf("Goroutine %d got empty token", i)
			continue
		}
		if seen[tok] {
			t.Errorf("Goroutine %d got duplicated token %q", i, tok)
		}
		seen[tok] = true
	}
}

// TestGetTokenShared_CancelledCtxSentinel 验证入口哨兵：
// 已取消的 ctx 直接拒绝，不发起任何抓取动作（含 custom fetch 桩路径）。
func TestGetTokenShared_CancelledCtxSentinel(t *testing.T) {
	var calls int32
	p := &TokenPool{fetch: func(_ string) (string, error) {
		atomic.AddInt32(&calls, 1)
		return "tok", nil
	}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tok, err := p.GetTokenShared(ctx)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("Expected context.Canceled, got tok=%q err=%v", tok, err)
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("Cancelled request must not trigger fetch, got %d calls", calls)
	}
}

// TestFallbackPollWidth 验证 rT 兜底轮询宽度跟随并发规格（parallel_pool_size），
// cfg 缺失或规格非法时退回历史固定值 5。
func TestFallbackPollWidth(t *testing.T) {
	cases := []struct {
		name string
		size int
		want int
	}{
		{"规格合法_按规格", 7, 7},
		{"规格为上限20", 20, 20},
		{"规格最小1", 1, 1},
		{"规格非法0_回退5", 0, 5},
		{"规格负数_回退5", -3, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.ParallelPoolSize = tc.size
			if got := fallbackPollWidth(config.StaticProvider(cfg)); got != tc.want {
				t.Errorf("fallbackPollWidth(size=%d) = %d, want %d", tc.size, got, tc.want)
			}
		})
	}

	t.Run("cfg缺失_回退5", func(t *testing.T) {
		if got := fallbackPollWidth(nil); got != 5 {
			t.Errorf("fallbackPollWidth(nil) = %d, want 5", got)
		}
	})
}
