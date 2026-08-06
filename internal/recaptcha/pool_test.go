package recaptcha

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
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
	tok1, err := p.GetToken(context.Background())
	if err != nil || tok1 != "tok-1" {
		t.Fatalf("Expected tok-1, got tok=%q err=%v", tok1, err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("Expected 1 fetch call, got %d", calls)
	}

	// 第二次调用必须重新抓取（无缓存），返回 tok-2
	tok2, err := p.GetToken(context.Background())
	if err != nil || tok2 != "tok-2" {
		t.Fatalf("Expected fresh tok-2, got tok=%q err=%v", tok2, err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("Expected 2 fetch calls (no cache), got %d", calls)
	}

	// Invalidate 为空操作，不影响后续抓取
	p.Invalidate()
	tok3, err := p.GetToken(context.Background())
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
