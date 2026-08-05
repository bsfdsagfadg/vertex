package recaptcha

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTokenPoolCacheAndInvalidate(t *testing.T) {
	var calls int32
	p := &TokenPool{fetch: func(_ string) (string, error) {
		n := atomic.AddInt32(&calls, 1)
		return fmt.Sprintf("tok-%d", n), nil
	}}

	// First call -> fetch 1
	tok1, err := p.GetToken(context.Background())
	if err != nil || tok1 != "tok-1" {
		t.Fatalf("Expected tok-1, got tok=%q err=%v", tok1, err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("Expected 1 fetch call, got %d", calls)
	}

	// Second call -> cached tok-1
	tok2, err := p.GetToken(context.Background())
	if err != nil || tok2 != "tok-1" {
		t.Fatalf("Expected cached tok-1, got tok=%q err=%v", tok2, err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("Expected 1 fetch call (cached), got %d", calls)
	}

	// Invalidate -> next call fetches tok-2
	p.Invalidate()
	tok3, err := p.GetToken(context.Background())
	if err != nil || tok3 != "tok-2" {
		t.Fatalf("Expected tok-2 after invalidate, got tok=%q err=%v", tok3, err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("Expected 2 fetch calls, got %d", calls)
	}
}

func TestTokenPoolSingleFlight(t *testing.T) {
	var calls int32
	p := &TokenPool{fetch: func(_ string) (string, error) {
		time.Sleep(50 * time.Millisecond)
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

	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("Expected singleflight to collapse to 1 fetch call, got %d", calls)
	}
	for i, tok := range toks {
		if tok != "tok-1" {
			t.Errorf("Goroutine %d got tok=%q, expected tok-1", i, tok)
		}
	}
}
