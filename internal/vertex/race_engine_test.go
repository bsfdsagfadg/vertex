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
	}
}

func raceTestConfigAllAtOnce() config.AppConfig {
	return config.AppConfig{
		ParallelPoolEnabled: true,
		ParallelPoolSize:    3,
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

// TestRunRace_Streaming_PermissionDenied_NoFailFast 验证 403 不会触发 fail-fast：
// Node 1 返回 403 PermissionDenied，Node 2 延迟后成功，竞速引擎应淘汰 Node 1 并返回 Node 2 结果。
func TestRunRace_Streaming_PermissionDenied_NoFailFast(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2")
	defer nodes.ResetState()

	cfg := config.StaticProvider(raceTestConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var launched sync.WaitGroup

	run := func(ctx context.Context, uri string) (<-chan StreamChunk, error) {
		if uri == "uri1" {
			return nil, NewPermissionDeniedError("forbidden", nil)
		}
		launched.Add(1)
		defer launched.Done()
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		ch := make(chan StreamChunk, 1)
		ch <- StreamChunk{Data: map[string]any{"text": "success"}}
		close(ch)
		return ch, nil
	}

	result, err := RunRace(ctx, cfg, run, WithPreserveRaceCtxOnWin[<-chan StreamChunk](), WithFailFastOnHardError[<-chan StreamChunk]())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	launched.Wait()
	first := <-result
	if first.Err != nil {
		t.Fatalf("unexpected error chunk: %v", first.Err)
	}
}

// TestRunRace_Streaming_GlobalHardError_FailFast 验证全局硬错误（invalid 400）仍触发 fail-fast：
// Node 1 返回 400 InvalidArgument，应在对冲定时器触发前终止竞速，不启动 Node 2。
func TestRunRace_Streaming_GlobalHardError_FailFast(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2")
	defer nodes.ResetState()

	cfg := config.StaticProvider(raceTestConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var launchCount int32

	run := func(ctx context.Context, uri string) (<-chan StreamChunk, error) {
		atomic.AddInt32(&launchCount, 1)
		return nil, NewInvalidArgumentError("invalid model name", nil)
	}

	_, err := RunRace(ctx, cfg, run, WithFailFastOnHardError[<-chan StreamChunk]())
	if err == nil {
		t.Error("expected error, got nil")
	}
	if count := atomic.LoadInt32(&launchCount); count > 1 {
		t.Errorf("expected launchCount <= 1 (fail-fast on global hard error), got %d", count)
	}
}

// TestStreamParallel_FailoverOnEmptyResponse 验证 StreamParallel 在节点 A 返回空流
// （channel 无数据直接关闭）时能故障转移到节点 B。
func TestStreamParallel_FailoverOnEmptyResponse(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2")
	defer nodes.ResetState()

	cfg := config.StaticProvider(raceTestConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var data atomic.Value
	data.Store("")

	var launched sync.WaitGroup

	op := func(ctx context.Context, uri string) <-chan StreamChunk {
		ch := make(chan StreamChunk, 64)
		go func() {
			defer close(ch)
			if uri == "uri1" {
				// Node A：channel 直接关闭（空流），触发 wrappedOp 的 !ok 分支。
				return
			}
			launched.Add(1)
			defer launched.Done()
			select {
			case <-time.After(50 * time.Millisecond):
			case <-ctx.Done():
				return
			}
			ch <- StreamChunk{Data: map[string]any{"text": "node-b-success"}}
		}()
		return ch
	}

	var gotChunks []StreamChunk
	yield := func(chunk StreamChunk) bool {
		gotChunks = append(gotChunks, chunk)
		return true
	}

	StreamParallel(ctx, cfg, op, yield)
	launched.Wait()

	if len(gotChunks) == 0 {
		t.Fatal("expected at least one chunk from node B")
	}
	if gotChunks[0].Err != nil {
		t.Fatalf("expected success chunk, got error: %v", gotChunks[0].Err)
	}
}

// TestRunRace_Streaming_FailFastOnHardError 验证流式首帧的全局硬错误快速终止：
// 第一个候选返回 notfound 硬错误后直接终止，不启动对冲节点。
func TestRunRace_Streaming_FailFastOnHardError(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2", "uri3")
	defer nodes.ResetState()

	cfg := config.StaticProvider(raceTestConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var launchCount int32

	run := func(ctx context.Context, uri string) (<-chan StreamChunk, error) {
		atomic.AddInt32(&launchCount, 1)
		return nil, NewNotFoundError("model not found", nil)
	}

	_, err := RunRace[<-chan StreamChunk](ctx, cfg, run, WithFailFastOnHardError[<-chan StreamChunk]())

	if err == nil {
		t.Error("expected error, got nil")
	}
	if count := atomic.LoadInt32(&launchCount); count > 1 {
		t.Errorf("expected launchCount <= 1 (fail-fast on global hard error), got %d", count)
	}
}

// TestRunRace_AuthErrorDisablesNode 验证 auth 错误分支：
// RunRace 遇到 Kind == "auth" 的 VertexError 后，通过 BatchUpdateNodesDisabled
// 将该节点在节点池中标记为禁用。
func TestRunRace_AuthErrorDisablesNode(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2")
	defer nodes.ResetState()

	cfg := config.StaticProvider(raceTestConfigAllAtOnce())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	run := func(ctx context.Context, uri string) (string, error) {
		return "", NewAuthenticationError("test auth failure", nil)
	}

	_, err := RunRace[string](ctx, cfg, run)
	if err == nil {
		t.Error("expected error from RunRace, got nil")
	}

	ns := nodes.LoadNodes()
	for _, n := range ns {
		if !n.Disabled {
			t.Errorf("Expected %s to be disabled after auth error, but Disabled=false", n.RawURI)
		}
	}
}

// TestRunRace_ContextCanceled_PreservesCollectedResultsAndFailedErrors 验证 Context 取消分支
// 不得绕过终结评估逻辑（race_engine.go 第 346 行）：
//   - 子测试 1：collectedResults 已含非即时胜出结果（MAX_TOKENS）时，后续节点返回 Context 取消错误，
//     引擎应返回已收集的有效结果而非 Context 错误。
//   - 子测试 2：failedErrors 已含 429 限流错误时，后续节点返回 Context 取消错误，
//     引擎应通过 pickBestError 返回优先级最高（1）的 429 错误而非 Context 错误。
func TestRunRace_ContextCanceled_PreservesCollectedResultsAndFailedErrors(t *testing.T) {
	t.Run("PreservesCollectedResults", func(t *testing.T) {
		setupRaceNodes(t, "uri1", "uri2")
		defer nodes.ResetState()

		cfg := config.StaticProvider(raceTestConfig())
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// 竞速引擎按全局轮询索引挑选候选，首个启动的节点不确定（uri1/uri2 皆可），
		// 因此按调用顺序而非 URI 决定行为：首个节点产出非即时胜出结果，次个节点返回 Context 取消错误。
		var callOrder int32
		run := func(ctx context.Context, uri string) (map[string]any, error) {
			order := atomic.AddInt32(&callOrder, 1)
			if order == 1 {
				return map[string]any{
					"candidates": []any{map[string]any{
						"finishReason": "MAX_TOKENS",
						"content":      map[string]any{"parts": []any{map[string]any{"text": "node1-answer"}}, "role": "model"},
					}},
				}, nil
			}
			return nil, NewContextError(context.Canceled)
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
			t.Fatalf("expected nil error (collected result should win over context error), got: %v", err)
		}
		if finish := candidateFinish(result); finish != "MAX_TOKENS" {
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

		_, err := RunRace[map[string]any](ctx, cfg, run)
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

// TestRunRace_CandidatePanic_HandledGracefully 验证 launchNode 内部协程 panic 时：
// RunRace 不崩溃、能收敛返回健康节点结果，且 in-flight 计数无泄露。
// 修复前：未 recover 的 panic 会打穿竞速引擎，使 active 计数失步、竞速循环挂死。
func TestRunRace_CandidatePanic_HandledGracefully(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2")
	defer nodes.ResetState()

	cfg := config.StaticProvider(raceTestConfigAllAtOnce())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	run := func(ctx context.Context, uri string) (string, error) {
		if uri == "uri1" {
			panic("simulated node panic")
		}
		return "result-uri2", nil
	}

	result, err := RunRace[string](ctx, cfg, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "result-uri2" {
		t.Errorf("expected healthy node result, got %q", result)
	}

	// in-flight 计数应在竞速收敛后归零（无泄露）。
	waitForZeroInFlight(t, "uri1", "uri2")
}

// waitForZeroInFlight 轮询等待给定节点 InFlight 归零（带超时），检测 goroutine/计数泄露。
func waitForZeroInFlight(t *testing.T, uris ...string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		health := nodes.LoadHealth()
		ok := true
		for _, u := range uris {
			if h, exists := health[u]; exists {
				if atomic.LoadInt32(&h.InFlight) != 0 {
					ok = false
					break
				}
			}
		}
		if ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("InFlight 计数未在期限内归零: %s", uris)
}

// TestRunRace_StreamIdleTimeout_TriggersRateLimitCooldown 验证流式包间空闲超时
// （ErrStreamIdleTimeout）会触发节点的短时避让（RecordRateLimit），使僵死/慢节点被隔离，
// 避免反复被选中。修复前：空闲超时的节点仅记失败、无冷却，极易被下一轮竞速再次选中。
func TestRunRace_StreamIdleTimeout_TriggersRateLimitCooldown(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2")
	defer nodes.ResetState()

	cfg := config.StaticProvider(raceTestConfigAllAtOnce())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	run := func(ctx context.Context, uri string) (string, error) {
		return "", NewNetworkError(ErrStreamIdleTimeout)
	}

	if _, err := RunRace[string](ctx, cfg, run); err == nil {
		t.Fatal("expected error, got nil")
	}

	now := time.Now().Unix()
	health := nodes.LoadHealth()
	for _, uri := range []string{"uri1", "uri2"} {
		h, exists := health[uri]
		if !exists {
			t.Errorf("node %s 缺少 health 记录", uri)
			continue
		}
		if h.CooldownUntil <= now {
			t.Errorf("node %s 应进入 15 秒冷却避让, CooldownUntil=%d (now=%d)", uri, h.CooldownUntil, now)
		}
	}
}

// TestRunRace_ParentContextCanceled_ReturnsContextErrorOverFailedErrors 验证外部父 Context 被取消时，
// RunRace 必须优先返回 context 错误，而不是已经积累的历史节点 502 错误。
// 修复前：终结评估按 failedErrors 挑错误，会把客户端断开误报为历史节点 502。
func TestRunRace_ParentContextCanceled_ReturnsContextErrorOverFailedErrors(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2")
	defer nodes.ResetState()

	cfg := config.StaticProvider(raceTestConfigAllAtOnce())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// uri1 立即返回节点级错误（存入 failedErrors）；uri2 阻塞，等待 ctx 取消。
	run := func(ctx context.Context, uri string) (string, error) {
		if uri == "uri1" {
			return "", NewEmptyResponseError("gateway 502", nil)
		}
		<-ctx.Done()
		return "", ctx.Err()
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := RunRace[string](ctx, cfg, run)
		errCh <- err
	}()

	// 等待 uri1 的 502 已被收集（failedErrors 非空）后再取消父 ctx。
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected errors.Is(err, context.Canceled)=true, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunRace did not return after parent context canceled")
	}
}

func TestRunRace_PickBestError_Priority(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2")
	defer nodes.ResetState()

	cfg := config.StaticProvider(raceTestConfig())

	t.Run("retryable_wins_over_nonretryable", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var callCount int32
		run := func(ctx context.Context, uri string) (map[string]any, error) {
			order := atomic.AddInt32(&callCount, 1)
			if order == 1 {
				return nil, NewEmptyResponseError("empty", nil)
			}
			return nil, NewInvalidArgumentError("bad request", nil)
		}

		_, err := RunRace[map[string]any](ctx, cfg, run)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		var ve *VertexError
		if !errors.As(err, &ve) {
			t.Fatalf("expected *VertexError, got %T", err)
		}
		if !ve.IsRetryable() {
			t.Errorf("expected retryable error when one exists, got Kind=%s Code=%d", ve.Kind, ve.Code)
		}
	})

	t.Run("nonretryable_global_wins_over_other_nonretryable", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var callCount int32
		run := func(ctx context.Context, uri string) (map[string]any, error) {
			order := atomic.AddInt32(&callCount, 1)
			if order == 1 {
				return nil, NewInvalidArgumentError("bad request", nil)
			}
			return nil, NewPermissionDeniedError("forbidden", nil)
		}

		_, err := RunRace[map[string]any](ctx, cfg, run)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		var ve *VertexError
		if !errors.As(err, &ve) {
			t.Fatalf("expected *VertexError, got %T", err)
		}
		if !ve.IsGlobalHardError() {
			t.Errorf("expected GlobalHardError when all errors are non-retryable, got Kind=%s Code=%d", ve.Kind, ve.Code)
		}
	})

	t.Run("ratelimit_wins_over_other_retryable", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var callCount int32
		run := func(ctx context.Context, uri string) (map[string]any, error) {
			order := atomic.AddInt32(&callCount, 1)
			if order == 1 {
				return nil, NewRateLimitError("too many", 0, nil)
			}
			return nil, NewEmptyResponseError("empty", nil)
		}

		_, err := RunRace[map[string]any](ctx, cfg, run)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		var ve *VertexError
		if !errors.As(err, &ve) {
			t.Fatalf("expected *VertexError, got %T", err)
		}
		if ve.Kind != "ratelimit" && ve.Code != 429 {
			t.Errorf("expected ratelimit error as highest priority retryable, got Kind=%s Code=%d", ve.Kind, ve.Code)
		}
	})
}
