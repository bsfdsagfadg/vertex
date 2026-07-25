package recaptcha

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
)

// TestTokenPoolRealtime 验证每次 GetToken 都实时获取，且 Start/Stop 不阻塞、Stats 返回 0,0。
func TestTokenPoolRealtime(t *testing.T) {
	var calls int32
	p := &TokenPool{fetch: func(_ string) (string, error) {
		n := atomic.AddInt32(&calls, 1)
		return fmt.Sprintf("tok-%d", n), nil
	}}

	p.Start()
	if size, fill := p.Stats(); size != 0 || fill != 0 {
		t.Fatalf("Stats 应为 0,0，got %d,%d", size, fill)
	}

	for i := 1; i <= 3; i++ {
		tok, err := p.GetToken(context.Background())
		if err != nil || tok == "" {
			t.Fatalf("第 %d 次 GetToken 失败：tok=%q err=%v", i, tok, err)
		}
		if int(atomic.LoadInt32(&calls)) != i {
			t.Fatalf("应每次实时获取，期望 %d 次，实际 %d", i, calls)
		}
	}

	p.Stop() // 不应阻塞
}
