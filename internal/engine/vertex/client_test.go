package vertex

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/engine/recaptcha"
	"github.com/bsfdsagfadg/vertex/internal/infra/config"
	"github.com/bsfdsagfadg/vertex/internal/infra/transport"
)

// ── runPrepareTasks 并行性（确定性 channel 握手，零墙钟断言） ──

// TestRunPrepareTasks_TrueParallelism 验证两组前置任务真并行执行：
// 双方互相以"对方已启动"为放行条件，若退化为串行必然互相等待至超时失败。
func TestRunPrepareTasks_TrueParallelism(t *testing.T) {
	sessionStarted := make(chan struct{})
	tokenStarted := make(chan struct{})

	sessFn := func() (*transport.Session, error) {
		close(sessionStarted) // 宣告会话任务已启动
		select {
		case <-tokenStarted: // 等到对方启动信号后才收尾
			return &transport.Session{}, nil
		case <-time.After(2 * time.Second):
			return nil, errors.New("token 任务未启动：执行已退化为串行")
		}
	}
	tokFn := func() (string, error) {
		close(tokenStarted)
		select {
		case <-sessionStarted:
			return "tok", nil
		case <-time.After(2 * time.Second):
			return "", errors.New("session 任务未启动：执行已退化为串行")
		}
	}

	sess, tok, sessErr, tokErr := runPrepareTasks(sessFn, tokFn)
	if sessErr != nil || tokErr != nil {
		t.Fatalf("并行执行失败: sessionErr=%v tokenErr=%v", sessErr, tokErr)
	}
	if sess == nil || tok != "tok" {
		t.Fatalf("结果异常: sess=%v tok=%q", sess, tok)
	}
}

// ── joinPrepared 裁决矩阵（全表穷举） ──

func TestJoinPrepared_DispositionMatrix(t *testing.T) {
	newSess := func(t *testing.T) *transport.Session {
		sess, err := transport.NewNetworkClient(nil).CreateSession(1, "", "join-matrix")
		if err != nil {
			t.Fatalf("构造测试会话失败: %v", err)
		}
		return sess
	}

	cases := []struct {
		name     string
		sessErr  error
		tok      string
		tokErr   error
		wantKind string // 空串表示期望成功移交
	}{
		{"双成功_原样移交", nil, "tok", nil, ""},
		{"仅rT错误_infra", nil, "", errors.New("rT exhausted"), "infra"},
		{"仅rT为空_infra", nil, "", nil, "infra"},
		{"仅会话失败_internal", errors.New("dial refused"), "tok", nil, "internal"},
		{"双失败_rT优先_infra", errors.New("dial refused"), "", errors.New("rT exhausted"), "infra"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var sess *transport.Session
			if tc.sessErr == nil {
				sess = newSess(t)
			}
			gotSess, gotTok, verr := joinPrepared(sess, tc.sessErr, tc.tok, tc.tokErr)

			if tc.wantKind == "" {
				if verr != nil {
					t.Fatalf("期望成功，得到错误: %+v", verr)
				}
				if gotSess != sess || gotTok != tc.tok {
					t.Fatal("成功路径必须原样移交会话与 token")
				}
				gotSess.Close()
				return
			}

			if verr == nil {
				t.Fatal("期望裁决错误，得到 nil")
			}
			if gotSess != nil || gotTok != "" {
				t.Fatal("失败路径不得移交任何资源")
			}
			if verr.Kind != tc.wantKind {
				t.Errorf("Kind = %s, want %s", verr.Kind, tc.wantKind)
			}
		})
	}
}

// ── prepareCandidate 取消语义 ──

// TestPrepareCandidate_CancelledCtx_WinsUnconditionally 验证取消无条件最优先：
// 预先取消的 ctx 上发起准备，取消错误直接上浮（errors.Is 可穿透 VertexError），
// 且 rT 抓取桩零调用（GetTokenShared 入口哨兵拦截，杜绝幽灵抓取）。
func TestPrepareCandidate_CancelledCtx_WinsUnconditionally(t *testing.T) {
	fetchCalls := 0
	vc := &VertexAIClient{
		net: transport.NewNetworkClient(nil),
		pool: recaptcha.NewTokenPoolCustom(func(proxyURI string) (string, error) {
			fetchCalls++
			return "tok", nil
		}),
		cfg: config.StaticProvider(config.DefaultConfig()),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sess, tok, verr := vc.prepareCandidate(ctx, "")
	if sess != nil || tok != "" {
		t.Fatal("取消路径不得移交任何资源")
	}
	if verr == nil || !errors.Is(verr, context.Canceled) {
		t.Fatalf("期望取消错误上浮，得到: %+v", verr)
	}
	if fetchCalls != 0 {
		t.Fatalf("已取消请求不应发起 rT 抓取，实际 %d 次", fetchCalls)
	}
}
