package recaptcha

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/nodes"
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

func TestTokenPoolUsesDynamicDefaultButExplicitEmptyMeansDirect(t *testing.T) {
	currentProxy := "http://proxy-a.example:7890"
	var gotProxy string
	p := &TokenPool{
		fetch: func(_ context.Context, proxyURI string) (string, error) {
			gotProxy = proxyURI
			return "token", nil
		},
		defaultProxy: func() string { return currentProxy },
		tryEntry:     func() bool { return true },
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
}

func TestTokenPoolEntryFirst(t *testing.T) {
	var calls []string
	p := &TokenPool{
		fetch: func(_ context.Context, proxyURI string) (string, error) {
			calls = append(calls, proxyURI)
			if proxyURI == "http://entry.example:7890" {
				return "tok-entry", nil
			}
			return "", fmt.Errorf("refused %s", proxyURI)
		},
		defaultProxy: func() string { return "http://entry.example:7890" },
		tryEntry:     func() bool { return true },
	}

	tok, err := p.GetTokenWithProxyContext(context.Background(), "node://1")
	if err != nil || tok != "tok-entry" {
		t.Fatalf("开启优先入口时应收敛到入口抓取: tok=%q err=%v", tok, err)
	}
	if len(calls) != 1 || calls[0] != "http://entry.example:7890" {
		t.Fatalf("应只尝试入口一次，got %v", calls)
	}
}

func TestTokenPoolEntryFallbackToNodes(t *testing.T) {
	var calls []string
	p := &TokenPool{
		fetch: func(_ context.Context, proxyURI string) (string, error) {
			calls = append(calls, proxyURI)
			if proxyURI == "http://entry.example:7890" {
				return "", fmt.Errorf("entry down")
			}
			if proxyURI == "http://proxy-a.example:7890" {
				return "tok-node", nil
			}
			return "", fmt.Errorf("refused %s", proxyURI)
		},
		defaultProxy: func() string { return "http://entry.example:7890" },
		tryEntry:     func() bool { return true },
		selectNodes: func() []nodes.Node {
			return []nodes.Node{
				{RawURI: "http://proxy-a.example:7890", Name: "A"},
				{RawURI: "http://proxy-b.example:7890", Name: "B"},
			}
		},
	}

	tok, err := p.GetTokenWithProxyContext(context.Background(), "")
	if err != nil || tok != "tok-node" {
		t.Fatalf("入口失败应回退节点轮询: tok=%q err=%v", tok, err)
	}
	if len(calls) != 2 {
		t.Fatalf("应尝试入口+第一个节点各一次，got %v", calls)
	}
}

func TestTokenPoolEntryDisabledSkipsEntryAndFallsBackDirect(t *testing.T) {
	var calls []string
	p := &TokenPool{
		fetch: func(_ context.Context, proxyURI string) (string, error) {
			calls = append(calls, proxyURI)
			if proxyURI == "http://proxy-a.example:7890" {
				return "", fmt.Errorf("node down")
			}
			return "tok-direct", nil
		},
		defaultProxy: func() string { return "http://entry.example:7890" },
		tryEntry:     func() bool { return false },
		selectNodes: func() []nodes.Node {
			return []nodes.Node{{RawURI: "http://proxy-a.example:7890", Name: "A"}}
		},
	}

	tok, err := p.GetTokenWithProxyContext(context.Background(), "")
	if err != nil || tok != "tok-direct" {
		t.Fatalf("关闭入口时应跳过入口、节点失败后兜底直连: tok=%q err=%v", tok, err)
	}
	if len(calls) != 2 {
		t.Fatalf("应尝试节点+直连各一次，got %v", calls)
	}
}

func TestTokenPoolAllFailReturnsError(t *testing.T) {
	p := &TokenPool{
		fetch: func(_ context.Context, proxyURI string) (string, error) {
			return "", fmt.Errorf("all down: %s", proxyURI)
		},
		defaultProxy: func() string { return "http://entry.example:7890" },
		tryEntry:     func() bool { return false },
		selectNodes:  func() []nodes.Node { return nil },
	}

	if _, err := p.GetTokenWithProxyContext(context.Background(), "node://1"); err == nil {
		t.Fatal("全部失败应返回错误")
	}
}
