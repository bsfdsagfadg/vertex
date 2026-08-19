package vertex

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/transform"
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

	_, err := RunRace(ctx, cfg, run)
	if err == nil {
		t.Error("expected error from RunRace, got nil")
	}

	if count := atomic.LoadInt32(&launchCount); count > 1 {
		t.Errorf("expected launchCount <= 1, got %d", count)
	}
}

// TestRunRace_AllAtOnce_LaunchesAllCandidates verifies that when
// ParallelPoolDelayDynamic=false, all candidates launch simultaneously.
//
// 说明：不使用返回后读取计数的方式断言（那依赖 goroutine 调度时序，成功者先返回时
// 其余候选可能尚未进入 run，造成假失败）。改用双阶段同步屏障：started 记录候选真正
// 进入执行函数，release 阻塞候选直至全部启动，验证的是“全量模式下所有候选都已启动”
// 的目标语义。
func TestRunRace_AllAtOnce_LaunchesAllCandidates(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2", "uri3")
	defer nodes.ResetState()

	cfg := config.StaticProvider(raceTestConfigAllAtOnce())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	started := make(chan struct{}, 3)
	release := make(chan struct{})

	run := func(ctx context.Context, uri string) (string, error) {
		started <- struct{}{}
		select {
		case <-release:
			return "result-" + uri, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	resultCh := make(chan error, 1)
	go func() {
		_, err := RunRace(ctx, cfg, run)
		resultCh <- err
	}()

	for i := 0; i < 3; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("not all candidates entered run")
		}
	}

	close(release)
	if err := <-resultCh; err != nil {
		t.Fatal(err)
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
	_, err := RunRace(ctx, cfg, run)
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

	result, err := RunRace(ctx, cfg, run)
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
		// 上层预算循环通过 cause 链识别 context 错误而不发起对冲。
		return "", NewContextError(fmt.Errorf("upstream request: %w", context.DeadlineExceeded))
	}

	_, err := RunRace(ctx, cfg, run)

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

	run := func(ctx context.Context, uri string) (*transform.GeminiResponse, error) {
		order := atomic.AddInt32(&callOrder, 1)
		if order == 1 {
			// 第一个候选：不可重试硬错误（403）。
			return nil, NewPermissionDeniedError("forbidden", nil)
		}
		return &transform.GeminiResponse{
			Candidates: []*transform.Candidate{{FinishReason: "STOP"}},
		}, nil
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
		t.Fatalf("unexpected error: %v", err)
	}
	if finish := candidateFinishTyped(result); finish != "STOP" {
		t.Errorf("expected STOP finish reason, got %s", finish)
	}
	if count := atomic.LoadInt32(&callOrder); count < 2 {
		t.Errorf("expected 2 candidates to be launched, got %d", count)
	}
}

// TestRunRace_CompleteChat_PickBestNonSTOP 验证非流式多个非 STOP 结果收集后
// 使用 pickBestResult 规则：MAX_TOKENS 优先，然后按内容长度。
func TestRunRace_CompleteChat_PickBestNonSTOP(t *testing.T) {
	makeResult := func(finish, text string) *transform.GeminiResponse {
		return &transform.GeminiResponse{
			Candidates: []*transform.Candidate{{
				FinishReason: finish,
				Content:      &transform.Content{Role: "model", Parts: []transform.Part{{Text: text}}},
			}},
		}
	}

	pickFinalizer := func(results []raceResult[*transform.GeminiResponse]) (*transform.GeminiResponse, error) {
		cr := make([]candidateResult, len(results))
		for i, r := range results {
			cr[i] = candidateResult{proxyURI: r.uri, resp: r.val, err: r.err}
		}
		return pickBestResult(cr, &transform.TextStrategy{})
	}

	t.Run("MAX_TOKENS_over_longer_non_MAX_TOKENS", func(t *testing.T) {
		setupRaceNodes(t, "uri1", "uri2")
		defer nodes.ResetState()

		cfg := config.StaticProvider(raceTestConfig())
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var callOrder int32
		run := func(ctx context.Context, uri string) (*transform.GeminiResponse, error) {
			order := atomic.AddInt32(&callOrder, 1)
			if order == 1 {
				return makeResult("MAX_TOKENS", "short"), nil
			}
			return makeResult("SAFETY", "this is a much longer response"), nil
		}

		result, err := RunRace(ctx, cfg, run, WithWinningCheck(func(resp *transform.GeminiResponse) bool {
			return candidateFinishTyped(resp) == "STOP"
		}), WithCollectedFinalizer(pickFinalizer))

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if finish := candidateFinishTyped(result); finish != "MAX_TOKENS" {
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
		run := func(ctx context.Context, uri string) (*transform.GeminiResponse, error) {
			order := atomic.AddInt32(&callOrder, 1)
			if order == 1 {
				return makeResult("OTHER_FINISH", "short"), nil
			}
			return makeResult("OTHER_FINISH", "this is a much longer response"), nil
		}

		result, err := RunRace(ctx, cfg, run, WithWinningCheck(func(resp *transform.GeminiResponse) bool {
			return candidateFinishTyped(resp) == "STOP"
		}), WithCollectedFinalizer(pickFinalizer))

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if finish := candidateFinishTyped(result); finish != "OTHER_FINISH" {
			t.Errorf("expected OTHER_FINISH finish, got %s", finish)
		}
		text := responseContentLengthTyped(result)
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
		ch <- StreamChunk{Data: &transform.GeminiChunk{Candidates: []*transform.Candidate{{Content: &transform.Content{Parts: []transform.Part{{Text: "success"}}}}}}}
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
			ch <- StreamChunk{Data: &transform.GeminiChunk{Candidates: []*transform.Candidate{{Content: &transform.Content{Parts: []transform.Part{{Text: "node-b-success"}}}}}}}
		}()
		return ch
	}

	var gotChunks []StreamChunk
	yield := func(chunk StreamChunk) bool {
		gotChunks = append(gotChunks, chunk)
		return true
	}

	StreamParallel(ctx, cfg, "gemini-2.5-flash", op, yield, nil)
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

	_, err := RunRace(ctx, cfg, run, WithFailFastOnHardError[<-chan StreamChunk]())

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

	_, err := RunRace(ctx, cfg, run)
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

	result, err := RunRace(ctx, cfg, run)
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

	if _, err := RunRace(ctx, cfg, run); err == nil {
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

func TestRunRace_PickBestError_Priority(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2")
	defer nodes.ResetState()

	cfg := config.StaticProvider(raceTestConfig())

	t.Run("nonretryable_wins_over_retryable", func(t *testing.T) {
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

		_, err := RunRace(ctx, cfg, run)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		var ve *VertexError
		if !errors.As(err, &ve) {
			t.Fatalf("expected *VertexError, got %T", err)
		}
		if !ve.IsGlobalHardError() {
			t.Errorf("expected deterministic hard error (invalid) to win over transient, got Kind=%s Code=%d", ve.Kind, ve.Code)
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

		_, err := RunRace(ctx, cfg, run)
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

		_, err := RunRace(ctx, cfg, run)
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

// TestRunRace_Convergence_PriorityLadder 验证收敛路径（pickBestError）的优先级梯队：
// 确定性业务真相（FailFast）> ratelimit > 其他瞬态；Committed（Truncated）压顶一切；
// 优先级与节点多数无关（防把方案误解为多数决）。不注入 failFast 选项（CompleteChat 语义）。
func TestRunRace_Convergence_PriorityLadder(t *testing.T) {
	uris := make([]string, 20)
	for i := range uris {
		uris[i] = fmt.Sprintf("uri%d", i+1)
	}
	setupRaceNodes(t, uris...)
	defer nodes.ResetState()

	cfg := config.StaticProvider(config.AppConfig{
		ParallelPoolEnabled: true,
		ParallelPoolSize:    20,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	newNetwork := func() *VertexError { return NewNetworkError(fmt.Errorf("boom")) }

	runRaceWith := func(one func(uri string) error) error {
		_, err := RunRace(ctx, cfg, func(ctx context.Context, uri string) (map[string]any, error) {
			return nil, one(uri)
		})
		return err
	}

	t.Run("safety_wins_over_transient", func(t *testing.T) {
		one := func(uri string) error {
			if uri == "uri20" {
				return NewSafetyError("Blocked", "RECITATION", nil)
			}
			return newNetwork()
		}
		err := runRaceWith(one)
		ve := asVertexError(err)
		if ve == nil || ve.Kind != "safety" || ve.Status != "RECITATION" {
			t.Fatalf("期望收敛为 safety(RECITATION)，实际 %v", err)
		}
	})

	t.Run("invalid_wins_over_transient", func(t *testing.T) {
		one := func(uri string) error {
			if uri == "uri20" {
				return NewInvalidArgumentError("bad request", nil)
			}
			return newNetwork()
		}
		err := runRaceWith(one)
		ve := asVertexError(err)
		if ve == nil || ve.Kind != "invalid" {
			t.Fatalf("期望收敛为 invalid，实际 %v", err)
		}
	})

	t.Run("safety_not_majority_vote", func(t *testing.T) {
		one := func(uri string) error {
			if uri == "uri20" {
				return newNetwork()
			}
			return NewSafetyError("Blocked", "RECITATION", nil)
		}
		err := runRaceWith(one)
		ve := asVertexError(err)
		if ve == nil || ve.Kind != "safety" {
			t.Fatalf("优先级与多数无关：19×safety+1×502 仍应收敛为 safety，实际 %v", err)
		}
	})

	t.Run("committed_truncated_wins_over_all", func(t *testing.T) {
		one := func(uri string) error {
			if uri == "uri20" {
				return NewSafetyError("Blocked", "RECITATION", nil)
			}
			return newNetwork().WithTruncated()
		}
		err := runRaceWith(one)
		ve := asVertexError(err)
		if ve == nil || !ve.Truncated || ve.ClassifyBatch() != Committed {
			t.Fatalf("期望收敛为 Committed(Truncated) 错误，实际 %v", err)
		}
	})
}

// TestStreamParallel_SingleCandidate_RetryLoop 验证预算循环：
// 池关闭时走单候选直连分支；第 1 轮返回 Transient 网络错误（预算内退避重试），
// 第 2 轮成功交付内容 → 客户端看到内容且无错误帧。
// 注：MaxRetries=1（总退避 ~1.5s），避免多轮真实退避拉长单用例耗时。
func TestStreamParallel_SingleCandidate_RetryLoop(t *testing.T) {
	start := time.Now()
	defer func() {
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("预算循环退避应短促，实际 %v", elapsed)
		}
	}()

	nodes.ResetState()
	defer nodes.ResetState()

	cfg := config.StaticProvider(config.AppConfig{
		ParallelPoolEnabled: false,
		MaxRetries:          1,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var attempts int32
	op := func(ctx context.Context, uri string) <-chan StreamChunk {
		ch := make(chan StreamChunk, 1)
		go func() {
			defer close(ch)
			n := atomic.AddInt32(&attempts, 1)
			if n < 2 {
				ch <- StreamChunk{Err: NewNetworkError(fmt.Errorf("tcp reset"))}
				return
			}
			ch <- StreamChunk{Data: &transform.GeminiChunk{Candidates: []*transform.Candidate{{Content: &transform.Content{Parts: []transform.Part{{Text: "recovered"}}}}}}}
		}()
		return ch
	}

	var gotData string
	yield := func(chunk StreamChunk) bool {
		if chunk.Err != nil {
			t.Errorf("unexpected error chunk: %v", chunk.Err)
			return false
		}
		gotData = firstPartText(chunk.Data)
		return true
	}

	StreamParallel(ctx, cfg, "test-model", op, yield, nil)

	if gotData != "recovered" {
		t.Errorf("expected retry loop to recover content, got %q", gotData)
	}
	if n := atomic.LoadInt32(&attempts); n != 2 {
		t.Errorf("expected exactly 2 attempts (1 retry), got %d", n)
	}
}

// TestStreamParallel_MultiCandidate_RetryWholeBatch 验证多候选场景下竞速失败后整批重试：
// 两个节点首轮均瞬态失败 → 预算内第 2 轮 uri1 成功交付。
func TestStreamParallel_MultiCandidate_RetryWholeBatch(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2")
	defer nodes.ResetState()

	cfg := config.StaticProvider(config.AppConfig{
		ParallelPoolEnabled: true,
		ParallelPoolSize:    2,
		MaxRetries:          1,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var attempts int32
	op := func(ctx context.Context, uri string) <-chan StreamChunk {
		ch := make(chan StreamChunk, 1)
		go func() {
			defer close(ch)
			n := atomic.AddInt32(&attempts, 1)
			if n <= 2 {
				ch <- StreamChunk{Err: NewNetworkError(fmt.Errorf("dial failed"))}
				return
			}
			ch <- StreamChunk{Data: &transform.GeminiChunk{Candidates: []*transform.Candidate{{Content: &transform.Content{Parts: []transform.Part{{Text: "ok-" + uri}}}}}}}
		}()
		return ch
	}

	var gotText string
	yield := func(chunk StreamChunk) bool {
		if chunk.Err != nil {
			t.Errorf("unexpected error chunk: %v", chunk.Err)
			return false
		}
		if chunk.Data != nil {
			gotText = firstPartText(chunk.Data)
		}
		return true
	}

	StreamParallel(ctx, cfg, "test-model", op, yield, nil)

	if !strings.HasPrefix(gotText, "ok-") {
		t.Errorf("expected whole-batch retry to deliver content, got %q", gotText)
	}
	if n := atomic.LoadInt32(&attempts); n < 3 {
		t.Errorf("expected >= 3 attempts (whole batch retried), got %d", n)
	}
}

// TestStreamParallel_Truncated_Committed_NoRetry 验证截断（Committed）错误不触发重试：
// 首帧后断流 → 单轮即止，Truncated 错误如实透传。
func TestStreamParallel_Truncated_Committed_NoRetry(t *testing.T) {
	nodes.ResetState()
	defer nodes.ResetState()

	cfg := config.StaticProvider(config.AppConfig{
		ParallelPoolEnabled: false,
		MaxRetries:          3,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var attempts int32
	op := func(ctx context.Context, uri string) <-chan StreamChunk {
		ch := make(chan StreamChunk, 1)
		go func() {
			defer close(ch)
			atomic.AddInt32(&attempts, 1)
			ch <- StreamChunk{Err: NewNetworkError(ErrStreamIdleTimeout).WithTruncated()}
		}()
		return ch
	}

	var gotErr *VertexError
	StreamParallel(ctx, cfg, "test-model", op, func(chunk StreamChunk) bool {
		if chunk.Err != nil {
			gotErr = chunk.Err
		}
		return true
	}, nil)

	if gotErr == nil {
		t.Fatal("expected truncated error chunk, got nil")
	}
	if !gotErr.Truncated {
		t.Errorf("expected Truncated flag set, got %+v", gotErr)
	}
	if gotErr.ClassifyBatch() != Committed {
		t.Errorf("expected Committed disposition, got %v", gotErr.ClassifyBatch())
	}
	if n := atomic.LoadInt32(&attempts); n != 1 {
		t.Errorf("expected no retry for Committed error, got %d attempts", n)
	}
}

// TestStreamParallel_ClientCancel_Silent 验证客户端取消后 StreamParallel 静默返回：
// 不产错误帧、不产数据帧。
func TestStreamParallel_ClientCancel_Silent(t *testing.T) {
	nodes.ResetState()
	defer nodes.ResetState()

	cfg := config.StaticProvider(config.AppConfig{
		ParallelPoolEnabled: false,
		MaxRetries:          0,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	op := func(ctx context.Context, uri string) <-chan StreamChunk {
		ch := make(chan StreamChunk, 1)
		go func() {
			defer close(ch)
			<-ctx.Done()
			ch <- StreamChunk{Err: NormalizeError(ctx.Err())}
		}()
		return ch
	}

	var got []StreamChunk
	StreamParallel(ctx, cfg, "test-model", op, func(chunk StreamChunk) bool {
		got = append(got, chunk)
		return true
	}, nil)

	if len(got) != 0 {
		t.Errorf("expected silent return on client cancel, got %d chunks", len(got))
	}
}

// TestStreamParallel_FailFast_NoRetry 验证 FailFast（invalid 等全局硬错误）在预算循环
// 首轮即终止：attempts==1，错误帧如实透传，不启动重试轮。
func TestStreamParallel_FailFast_NoRetry(t *testing.T) {
	nodes.ResetState()
	defer nodes.ResetState()

	cfg := config.StaticProvider(config.AppConfig{
		ParallelPoolEnabled: false,
		MaxRetries:          3,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var attempts int32
	op := func(ctx context.Context, uri string) <-chan StreamChunk {
		ch := make(chan StreamChunk, 1)
		go func() {
			defer close(ch)
			atomic.AddInt32(&attempts, 1)
			ch <- StreamChunk{Err: NewInvalidArgumentError("invalid model name", nil)}
		}()
		return ch
	}

	var gotErr *VertexError
	StreamParallel(ctx, cfg, "test-model", op, func(chunk StreamChunk) bool {
		if chunk.Err != nil {
			gotErr = chunk.Err
		}
		return true
	}, nil)

	if gotErr == nil {
		t.Fatal("expected error chunk, got nil")
	}
	if gotErr.ClassifyBatch() != FailFast {
		t.Errorf("expected FailFast disposition, got %v", gotErr.ClassifyBatch())
	}
	if n := atomic.LoadInt32(&attempts); n != 1 {
		t.Errorf("expected no retry for FailFast error, got %d attempts", n)
	}
}

// TestStreamParallel_Terminal_Converges 验证 Terminal（permission 等）错误不触发重试
// 且不触发 fail-fast 旁路：单候选下 attempts==1，错误帧透传为 Terminal。
func TestStreamParallel_Terminal_Converges(t *testing.T) {
	nodes.ResetState()
	defer nodes.ResetState()

	cfg := config.StaticProvider(config.AppConfig{
		ParallelPoolEnabled: false,
		MaxRetries:          3,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var attempts int32
	op := func(ctx context.Context, uri string) <-chan StreamChunk {
		ch := make(chan StreamChunk, 1)
		go func() {
			defer close(ch)
			atomic.AddInt32(&attempts, 1)
			ch <- StreamChunk{Err: NewPermissionDeniedError("forbidden", nil)}
		}()
		return ch
	}

	var gotErr *VertexError
	StreamParallel(ctx, cfg, "test-model", op, func(chunk StreamChunk) bool {
		if chunk.Err != nil {
			gotErr = chunk.Err
		}
		return true
	}, nil)

	if gotErr == nil {
		t.Fatal("expected error chunk, got nil")
	}
	if gotErr.ClassifyBatch() != Terminal {
		t.Errorf("expected Terminal disposition, got %v", gotErr.ClassifyBatch())
	}
	if n := atomic.LoadInt32(&attempts); n != 1 {
		t.Errorf("expected no retry for Terminal error, got %d attempts", n)
	}
}

// TestApplyNodeFailure_EmptyURI_NoOp 验证空 URI（直连分支）的 ApplyNodeFailure 无副作用：
// 不产生健康记录、不 panic、不误塞 auth 判定。
func TestApplyNodeFailure_EmptyURI_NoOp(t *testing.T) {
	nodes.ResetState()
	defer nodes.ResetState()

	ApplyNodeFailure("", NewAuthenticationError("boom", nil))
	ApplyNodeFailure("", NewNetworkError(ErrStreamIdleTimeout))
	ApplyNodeFailure("", NewRateLimitError("too many", 429, nil))

	if len(nodes.LoadHealth()) != 0 {
		t.Errorf("空 URI 不应产生任何健康记录，实际 %d 条", len(nodes.LoadHealth()))
	}
}
