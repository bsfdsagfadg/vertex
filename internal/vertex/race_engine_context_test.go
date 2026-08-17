package vertex

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/transform"
)

// TestRunRace_DeadlineExceeded_ReturnsBestErrorWhenFailedErrorsExist 验证服务端请求超时 (DeadlineExceeded) 时：
// 若竞速已经收集到了 429 节点错误，RunRace 必须返回 429 VertexError，不得被 DeadlineExceeded 覆盖为 500。
func TestRunRace_DeadlineExceeded_ReturnsBestErrorWhenFailedErrorsExist(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2")
	defer nodes.ResetState()

	cfg := config.StaticProvider(raceTestConfigAllAtOnce())
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	run := func(ctx context.Context, uri string) (string, error) {
		if uri == "uri1" {
			return "", NewRateLimitError("too many requests 429", 30, nil)
		}
		// uri2 挂起直到 deadline 到期
		<-ctx.Done()
		return "", ctx.Err()
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := RunRace(ctx, cfg, run)
		errCh <- err
	}()

	// 通过可观察状态（uri1 节点 health 的 429 冷却记录）确认 429 已被竞速循环收集
	deadline := time.Now().Add(2 * time.Second)
	var observed429 bool
	for time.Now().Before(deadline) {
		h := nodes.LoadHealth()
		if nodeH, exists := h["uri1"]; exists && nodeH.CooldownUntil > time.Now().Unix() {
			observed429 = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !observed429 {
		t.Fatal("uri1 429 cooldown was not observed before deadline")
	}

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		var ve *VertexError
		if !errors.As(err, &ve) {
			t.Fatalf("expected *VertexError, got %T (%v)", err, err)
		}
		if ve.Code != 429 || ve.Kind != "ratelimit" {
			t.Fatalf("expected 429 ratelimit error, got Code=%d Kind=%s Msg=%s", ve.Code, ve.Kind, ve.Message)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunRace timed out waiting for deadline return")
	}
}

// TestRunRace_ParentContextCanceled_ReturnsContextErrorOverFailedErrors 验证外部父 Context 被取消时，
// RunRace 必须优先返回 context 错误，而不是已经积累的历史节点 502/429 错误。
func TestRunRace_ParentContextCanceled_ReturnsContextErrorOverFailedErrors(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2")
	defer nodes.ResetState()

	cfg := config.StaticProvider(raceTestConfigAllAtOnce())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	run := func(ctx context.Context, uri string) (string, error) {
		if uri == "uri1" {
			return "", NewEmptyResponseError("gateway 502", nil)
		}
		<-ctx.Done()
		return "", ctx.Err()
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := RunRace(ctx, cfg, run)
		errCh <- err
	}()

	// 确认 uri1 已发生失败记录
	deadline := time.Now().Add(2 * time.Second)
	var recordedFailure bool
	for time.Now().Before(deadline) {
		h := nodes.LoadHealth()
		if nodeH, exists := h["uri1"]; exists && nodeH.FailCount > 0 {
			recordedFailure = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !recordedFailure {
		t.Fatal("uri1 failure was not recorded before cancel")
	}

	cancel()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected errors.Is(err, context.Canceled)=true, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunRace did not return after parent context canceled")
	}
}

// TestRunRace_ContextCanceled_PreservesCollectedResultsAndFailedErrors 验证内部候选返回 Context 取消错误时的终结评估
func TestRunRace_ContextCanceled_PreservesCollectedResultsAndFailedErrors(t *testing.T) {
	t.Run("PreservesCollectedResults", func(t *testing.T) {
		setupRaceNodes(t, "uri1", "uri2")
		defer nodes.ResetState()

		cfg := config.StaticProvider(raceTestConfig())
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var callOrder int32
		run := func(ctx context.Context, uri string) (*transform.GeminiResponse, error) {
			order := atomic.AddInt32(&callOrder, 1)
			if order == 1 {
				return &transform.GeminiResponse{
					Candidates: []*transform.Candidate{{
						FinishReason: "MAX_TOKENS",
						Content:      &transform.Content{Role: "model", Parts: []transform.Part{{Text: "node1-answer"}}},
					}},
				}, nil
			}
			return nil, NewContextError(context.Canceled)
		}

		result, err := RunRace(ctx, cfg, run, WithWinningCheck(func(resp *transform.GeminiResponse) bool {
			return candidateFinishTyped(resp) == "STOP"
		}), WithCollectedFinalizer(func(results []raceResult[*transform.GeminiResponse]) (*transform.GeminiResponse, error) {
			cr := make([]candidateResult, len(results))
			for i, r := range results {
				cr[i] = candidateResult{proxyURI: r.uri, resp: r.val, err: r.err}
			}
			return pickBestResult(cr, &transform.TextStrategy{})
		}))

		if err != nil {
			t.Fatalf("expected nil error (collected result should win over context error), got: %v", err)
		}
		if finish := candidateFinishTyped(result); finish != "MAX_TOKENS" {
			t.Errorf("expected first node MAX_TOKENS result to be returned, got finishReason=%q", finish)
		}
		if count := atomic.LoadInt32(&callOrder); count != 2 {
			t.Errorf("expected both candidates to launch, got %d", count)
		}
	})

	t.Run("PreservesFailedErrors", func(t *testing.T) {
		setupRaceNodes(t, "uri1", "uri2")
		defer nodes.ResetState()

		cfg := config.StaticProvider(raceTestConfig())
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var callOrder int32
		run := func(ctx context.Context, uri string) (map[string]any, error) {
			order := atomic.AddInt32(&callOrder, 1)
			if order == 1 {
				return nil, NewRateLimitError("rate limited", 0, nil)
			}
			return nil, NewContextError(context.Canceled)
		}

		_, err := RunRace(ctx, cfg, run)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		var ve *VertexError
		if !errors.As(err, &ve) {
			t.Fatalf("expected *VertexError, got %T", err)
		}
		if ve.Kind != "ratelimit" && ve.Code != 429 {
			t.Errorf("expected highest-priority ratelimit (429) error, got Kind=%s Code=%d", ve.Kind, ve.Code)
		}
	})
}

// TestRunRace_FailuresAlwaysNormalizedToVertexError 覆盖单节点降级、空候选、普通 error、包装 context error、finalizeCollected error
func TestRunRace_FailuresAlwaysNormalizedToVertexError(t *testing.T) {
	t.Run("SingleNodeFallbackNormalized", func(t *testing.T) {
		nodes.ResetState()
		cfg := config.StaticProvider(config.AppConfig{ParallelPoolEnabled: false})
		rawErr := errors.New("raw socket broken")

		_, err := RunRace(context.Background(), cfg, func(ctx context.Context, proxyURI string) (string, error) {
			return "", rawErr
		})

		var ve *VertexError
		if !errors.As(err, &ve) {
			t.Fatalf("expected *VertexError, got %T", err)
		}
		if !errors.Is(err, rawErr) {
			t.Errorf("expected cause unwrap to match rawErr")
		}
	})

	t.Run("FinalizeCollectedErrorNormalized", func(t *testing.T) {
		setupRaceNodes(t, "uri1")
		defer nodes.ResetState()
		cfg := config.StaticProvider(raceTestConfigAllAtOnce())

		_, err := RunRace(context.Background(), cfg, func(ctx context.Context, proxyURI string) (string, error) {
			return "some-val", nil
		}, WithWinningCheck(func(val string) bool {
			return false // Collect
		}), WithCollectedFinalizer(func(res []raceResult[string]) (string, error) {
			return "", errors.New("cannot finalize result")
		}))

		var ve *VertexError
		if !errors.As(err, &ve) {
			t.Fatalf("expected *VertexError, got %T", err)
		}
	})

	t.Run("FailFastGlobalHardErrorReturnsDirectly", func(t *testing.T) {
		setupRaceNodes(t, "uri1", "uri2")
		defer nodes.ResetState()
		cfg := config.StaticProvider(raceTestConfigAllAtOnce())

		hardErr := NewInvalidArgumentError("invalid model argument", nil)
		_, err := RunRace(context.Background(), cfg, func(ctx context.Context, proxyURI string) (string, error) {
			return "", hardErr
		}, WithFailFastOnHardError[string]())

		var ve *VertexError
		if !errors.As(err, &ve) {
			t.Fatalf("expected *VertexError, got %T", err)
		}
		if ve.Kind != "invalid" || ve.Code != 400 {
			t.Errorf("expected 400 invalid, got %v", ve)
		}
	})
}
