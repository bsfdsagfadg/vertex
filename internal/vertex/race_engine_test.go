package vertex

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
)

func setupRaceNodes(t *testing.T, uris ...string) {
	t.Helper()
	nodes.ResetState()
	ns := make([]nodes.Node, len(uris))
	for i, uri := range uris {
		ns[i] = nodes.Node{RawURI: uri, Name: fmt.Sprintf("node%d", i+1)}
	}
	nodes.MergeNodes(ns)
}

func raceTestConfig() config.AppConfig {
	return config.AppConfig{
		ParallelPoolEnabled:      true,
		ParallelPoolSize:         3,
		ParallelPoolDelayDynamic: true,
		ParallelNodeTopK:         80,
	}
}

func raceTestConfigAllAtOnce() config.AppConfig {
	return config.AppConfig{
		ParallelPoolEnabled: true,
		ParallelPoolSize:    3,
		ParallelNodeTopK:    80,
	}
}

// TestRunRace_NoLaunchAfterCtxCancel verifies that after ctx is canceled,
// RunRace does NOT launch new candidate nodes (scenario A, hedge mode).
func TestRunRace_NoLaunchAfterCtxCancel(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2", "uri3")
	defer nodes.ResetState()

	cfg := config.StaticProvider(raceTestConfig())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var launchCount int32

	run := func(ctx context.Context, uri string) (string, error) {
		atomic.AddInt32(&launchCount, 1)
		cancel()
		time.Sleep(5 * time.Millisecond)
		return "", fmt.Errorf("node failed")
	}

	_, err := RunRace[string](ctx, cfg, run)
	if err == nil {
		t.Error("expected error from RunRace, got nil")
	}

	if count := atomic.LoadInt32(&launchCount); count > 1 {
		t.Errorf("expected launchCount <= 1, got %d", count)
	}
}

// TestRunRace_AllAtOnce_LaunchesAllCandidates verifies that when
// ParallelPoolDelayDynamic=false, all candidates launch simultaneously.
func TestRunRace_AllAtOnce_LaunchesAllCandidates(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2", "uri3")
	defer nodes.ResetState()

	cfg := config.StaticProvider(raceTestConfigAllAtOnce())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var launchCount int32

	run := func(ctx context.Context, uri string) (string, error) {
		atomic.AddInt32(&launchCount, 1)
		return fmt.Sprintf("result-%s", uri), nil
	}

	_, err := RunRace[string](ctx, cfg, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if count := atomic.LoadInt32(&launchCount); count != 3 {
		t.Errorf("expected all 3 candidates to launch, got %d", count)
	}
}

// TestRunRace_HangWithTimeout_NoResend verifies that RunRace returns promptly
// on ctx timeout and does not keep launching new nodes (scenario B).
func TestRunRace_HangWithTimeout_NoResend(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2", "uri3")
	defer nodes.ResetState()

	cfg := config.StaticProvider(raceTestConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	var launchCount int32

	run := func(ctx context.Context, uri string) (string, error) {
		atomic.AddInt32(&launchCount, 1)
		<-ctx.Done()
		return "", ctx.Err()
	}

	start := time.Now()
	_, err := RunRace[string](ctx, cfg, run)
	elapsed := time.Since(start)

	if elapsed > 3*time.Second {
		t.Errorf("RunRace took too long: %v", elapsed)
	}
	if err == nil {
		t.Error("expected error from RunRace, got nil")
	}

	count := atomic.LoadInt32(&launchCount)
	if count > 3 {
		t.Errorf("expected launchCount <= 3, got %d", count)
	}
}

// TestRunRace_SuccessAfterHedge verifies that normal hedge racing still works
// (scenario C): first node is slow, hedge launches a fast backup → success.
func TestRunRace_SuccessAfterHedge(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2", "uri3")
	defer nodes.ResetState()

	cfg := config.StaticProvider(config.AppConfig{
		ParallelPoolEnabled:      true,
		ParallelPoolSize:         3,
		ParallelPoolDelayDynamic: false,
		ParallelNodeTopK:         80,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var callOrder int32

	run := func(ctx context.Context, uri string) (string, error) {
		order := atomic.AddInt32(&callOrder, 1)
		if order == 1 {
			select {
			case <-time.After(200 * time.Millisecond):
				return "slow-success", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		return "fast-success", nil
	}

	result, err := RunRace[string](ctx, cfg, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "fast-success" && result != "slow-success" {
		t.Errorf("unexpected result: %s", result)
	}

	if count := atomic.LoadInt32(&callOrder); count < 2 {
		t.Errorf("expected at least 2 calls (hedge), got %d", count)
	}
}

// TestRunRace_NoHedgeOnWrappedContextError verifies that RunRace does NOT
// launch hedge nodes when the error is a VertexError wrapping a context
// deadline (scenario D: Fix 3 effective through Unwrap chain).
func TestRunRace_NoHedgeOnWrappedContextError(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2", "uri3")
	defer nodes.ResetState()

	cfg := config.StaticProvider(raceTestConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var launchCount int32

	run := func(ctx context.Context, uri string) (string, error) {
		atomic.AddInt32(&launchCount, 1)
		// Simulate the real streaming error chain:
		// executeStreamingAttempt wraps with %w,
		// executeStreamingWithRetries catches it via change B,
		// wraps in NewContextError.
		return "", NewContextError(fmt.Errorf("upstream request: %w", context.DeadlineExceeded))
	}

	_, err := RunRace[string](ctx, cfg, run)

	// 1. In hedge mode, the node returns context error so no hedge should launch.
	if count := atomic.LoadInt32(&launchCount); count > 1 {
		t.Errorf("expected launchCount <= 1 (no hedge), got %d", count)
	}

	// 2. RunRace returned quickly (no hang).
	// 3. The returned error unwraps to DeadlineExceeded.
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected errors.Is(err, DeadlineExceeded) == true, got %v", err)
	}
}

// TestRunRace_CompleteChat_HardErrorDoesNotBlockSTOP 验证 CompleteChat 语义：
// 第一个候选返回不可重试硬错误时不会终止竞速，第二个候选的 STOP 仍可胜出。
func TestRunRace_CompleteChat_HardErrorDoesNotBlockSTOP(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2")
	defer nodes.ResetState()

	cfg := config.StaticProvider(raceTestConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var callOrder int32

	run := func(ctx context.Context, uri string) (map[string]any, error) {
		order := atomic.AddInt32(&callOrder, 1)
		if order == 1 {
			// 第一个候选：不可重试硬错误（403）。
			return nil, NewPermissionDeniedError("forbidden", nil)
		}
		return map[string]any{
			"candidates": []any{map[string]any{"finishReason": "STOP"}},
		}, nil
	}

	result, err := RunRace(ctx, cfg, run, WithWinningCheck(func(resp map[string]any) bool {
		return candidateFinish(resp) == "STOP"
	}), WithCollectedFinalizer(func(results []raceResult[map[string]any]) (map[string]any, error) {
		cr := make([]candidateResult, len(results))
		for i, r := range results {
			cr[i] = candidateResult{proxyURI: r.uri, resp: r.val, err: r.err}
		}
		return pickBestResult(cr)
	}))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if finish := candidateFinish(result); finish != "STOP" {
		t.Errorf("expected STOP finish reason, got %s", finish)
	}
	if count := atomic.LoadInt32(&callOrder); count < 2 {
		t.Errorf("expected 2 candidates to be launched, got %d", count)
	}
}

// TestRunRace_CompleteChat_PickBestNonSTOP 验证非流式多个非 STOP 结果收集后
// 使用 pickBestResult 规则：MAX_TOKENS 优先，然后按内容长度。
func TestRunRace_CompleteChat_PickBestNonSTOP(t *testing.T) {
	makeResult := func(finish, text string) map[string]any {
		return map[string]any{
			"candidates": []any{map[string]any{
				"finishReason": finish,
				"content":      map[string]any{"parts": []any{map[string]any{"text": text}}, "role": "model"},
			}},
		}
	}

	pickFinalizer := func(results []raceResult[map[string]any]) (map[string]any, error) {
		cr := make([]candidateResult, len(results))
		for i, r := range results {
			cr[i] = candidateResult{proxyURI: r.uri, resp: r.val, err: r.err}
		}
		return pickBestResult(cr)
	}

	t.Run("MAX_TOKENS_over_longer_non_MAX_TOKENS", func(t *testing.T) {
		setupRaceNodes(t, "uri1", "uri2")
		defer nodes.ResetState()

		cfg := config.StaticProvider(raceTestConfig())
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var callOrder int32
		run := func(ctx context.Context, uri string) (map[string]any, error) {
			order := atomic.AddInt32(&callOrder, 1)
			if order == 1 {
				return makeResult("MAX_TOKENS", "short"), nil
			}
			return makeResult("SAFETY", "this is a much longer response"), nil
		}

		result, err := RunRace(ctx, cfg, run, WithWinningCheck(func(resp map[string]any) bool {
			return candidateFinish(resp) == "STOP"
		}), WithCollectedFinalizer(pickFinalizer))

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if finish := candidateFinish(result); finish != "MAX_TOKENS" {
			t.Errorf("expected MAX_TOKENS to win, got %s", finish)
		}
		if count := atomic.LoadInt32(&callOrder); count != 2 {
			t.Errorf("expected both candidates to launch, got %d", count)
		}
	})

	t.Run("longer_text_when_same_finish_reason", func(t *testing.T) {
		setupRaceNodes(t, "uri1", "uri2")
		defer nodes.ResetState()

		cfg := config.StaticProvider(raceTestConfig())
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var callOrder int32
		run := func(ctx context.Context, uri string) (map[string]any, error) {
			order := atomic.AddInt32(&callOrder, 1)
			if order == 1 {
				return makeResult("SAFETY", "short"), nil
			}
			return makeResult("SAFETY", "this is a much longer response"), nil
		}

		result, err := RunRace(ctx, cfg, run, WithWinningCheck(func(resp map[string]any) bool {
			return candidateFinish(resp) == "STOP"
		}), WithCollectedFinalizer(pickFinalizer))

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if finish := candidateFinish(result); finish != "SAFETY" {
			t.Errorf("expected SAFETY finish, got %s", finish)
		}
		text := responseContentLength(result)
		if text <= len("short") {
			t.Errorf("expected longer text to win, got content length %d", text)
		}
	})
}

// TestRunRace_Streaming_FailFastOnHardError 验证流式首帧的不可重试错误快速终止：
// 第一个候选返回硬错误后直接终止，不启动对冲节点。
func TestRunRace_Streaming_FailFastOnHardError(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2", "uri3")
	defer nodes.ResetState()

	cfg := config.StaticProvider(raceTestConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var launchCount int32

	run := func(ctx context.Context, uri string) (<-chan StreamChunk, error) {
		atomic.AddInt32(&launchCount, 1)
		return nil, NewPermissionDeniedError("forbidden", nil)
	}

	_, err := RunRace[<-chan StreamChunk](ctx, cfg, run, WithFailFastOnHardError[<-chan StreamChunk]())

	if err == nil {
		t.Error("expected error, got nil")
	}
	if count := atomic.LoadInt32(&launchCount); count > 1 {
		t.Errorf("expected launchCount <= 1 (fail-fast), got %d", count)
	}
}

// TestRunRace_BackgroundCollector_IgnoresContextCancel 验证 Win 后后台收集器
// 忽略 context.Canceled 错误，不错误地 evict 落败节点。
func TestRunRace_BackgroundCollector_IgnoresContextCancel(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2")
	defer nodes.ResetState()

	stickyPool := nodes.GetStickyPool()
	stickyPool.Add("uri2") // 预先加入，验证取消不会误 evict

	cfg := config.StaticProvider(raceTestConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var launched sync.WaitGroup

	// 注册收集器完成信号。
	collectorDone := make(chan struct{})
	SetCollectorDoneHook(collectorDone)
	defer SetCollectorDoneHook(nil)

	// barrier 确保 uri2 已进入 run 函数体后才让 uri1 返回。
	// uri1 阻塞直到 uri2 中的 close(barrier) 发出启动信号。
	barrier := make(chan struct{})

	run := func(ctx context.Context, uri string) (string, error) {
		if uri == "uri1" {
			<-barrier // 等待 uri2 已启动
			return "winner", nil
		}
		launched.Add(1)
		defer launched.Done()
		close(barrier) // 通知 uri1：uri2 已进入 run 函数体
		<-ctx.Done()
		return "", ctx.Err()
	}

	result, err := RunRace[string](ctx, cfg, run, WithNoCancelOnSuccess[string]())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "winner" {
		t.Errorf("expected 'winner', got %s", result)
	}

	launched.Wait() // 等待落败候选完成

	// 等待收集器处理完所有结果，而非 time.Sleep。
	select {
	case <-collectorDone:
	case <-time.After(5 * time.Second):
		t.Fatal("collector did not finish within 5s")
	}

	if !stickyPool.IsSticky("uri2") {
		t.Error("uri2 should still be in sticky pool (context cancel should not evict)")
	}
}
