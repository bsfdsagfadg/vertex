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

	"github.com/bsfdsagfadg/vertex/internal/engine/transform"
	"github.com/bsfdsagfadg/vertex/internal/infra/config"
	"github.com/bsfdsagfadg/vertex/internal/node/exitpool"
)

// testNodes 是引擎域集成测试的共享出口节点池实例（无库内存模式，
// 各用例开头显式 Reset 隔离；同包测试串行执行，无并发争用）。
var testNodes = exitpool.NewManager(nil, nil, exitpool.Hooks{}) //nolint:gochecknoglobals

func setupRaceNodes(t *testing.T, uris ...string) {
	t.Helper()
	testNodes.Reset()
	ns := make([]exitpool.Node, len(uris))
	for i, uri := range uris {
		ns[i] = exitpool.Node{RawURI: uri, Name: fmt.Sprintf("node%d", i+1)}
	}
	testNodes.MergeNodes(ns)
}

// raceTestConfigSize 返回指定窗口宽度的引擎测试配置。
// 注意：AppConfig 字面量 MaxRetries 零值即 0，发射预算 = 窗口宽度；
// 需要"整批补满一轮"语义的用例应显式设 MaxRetries=1。
func raceTestConfigSize(size int) config.AppConfig {
	return config.AppConfig{
		ParallelPoolEnabled: true,
		ParallelPoolSize:    size,
	}
}

// raceTestConfig 是窗口宽度 3 的默认引擎测试配置。
func raceTestConfig() config.AppConfig {
	return raceTestConfigSize(3)
}

// TestRunRace_NoRefillAfterCtxCancel 验证父 ctx 取消后补位立即停止：
// 初始拉满窗口（min(W, 可用节点)=3）后第一发取消父 ctx，剩余预算与候选一律不再发射。
func TestRunRace_NoRefillAfterCtxCancel(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2", "uri3", "uri4", "uri5", "uri6")
	defer testNodes.Reset()

	cfg := config.StaticProvider(raceTestConfig())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{}, 3)
	var launchCount int32

	run := func(ctx context.Context, uri string) (string, error) {
		started <- struct{}{}
		if atomic.AddInt32(&launchCount, 1) == 1 {
			cancel()
		}
		time.Sleep(5 * time.Millisecond)
		return "", fmt.Errorf("node failed")
	}

	go func() {
		_, _ = RunRace(ctx, testNodes, cfg, run)
	}()

	// 屏障：等待初始填充的三个候选全部真正进入执行函数（发射为同步填充，必然到达）。
	for i := 0; i < 3; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("initial fill candidates did not all enter run")
		}
	}

	// 给可能的越界补位留观察窗：取消后绝不出现第 4 发。
	time.Sleep(50 * time.Millisecond)
	if count := atomic.LoadInt32(&launchCount); count != 3 {
		t.Errorf("expected exactly 3 launches (initial fill only, no refill after cancel), got %d", count)
	}
}

// TestRunRace_AllAtOnce_LaunchesAllCandidates 验证初始填充一次拉满全部窗口槽位：
// 所有候选在释放屏障前均已进入执行函数。
//
// 说明：不使用返回后读取计数的方式断言（那依赖 goroutine 调度时序，成功者先返回时
// 其余候选可能尚未进入 run，造成假失败）。改用双阶段同步屏障：started 记录候选真正
// 进入执行函数，release 阻塞候选直至全部启动，验证的是"初始填充满窗"的目标语义。
func TestRunRace_AllAtOnce_LaunchesAllCandidates(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2", "uri3")
	defer testNodes.Reset()

	cfg := config.StaticProvider(raceTestConfig())
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
		_, err := RunRace(ctx, testNodes, cfg, run)
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

// TestRunRace_HangWithTimeout_NoResend 验证全体候选挂起时请求按父 ctx 时限收敛，
// 且发射数被初始填充封顶（预算=窗口=3），不产生任何超限补位。
func TestRunRace_HangWithTimeout_NoResend(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2", "uri3")
	defer testNodes.Reset()

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
	_, err := RunRace(ctx, testNodes, cfg, run)
	elapsed := time.Since(start)

	if elapsed > 3*time.Second {
		t.Errorf("RunRace took too long: %v", elapsed)
	}
	if err == nil {
		t.Error("expected error from RunRace, got nil")
	}

	count := atomic.LoadInt32(&launchCount)
	if count > 3 {
		t.Errorf("expected launchCount <= 3 (budget=window), got %d", count)
	}
}

// TestRunRace_SuccessAcrossWindowFill 验证恒满窗口下的并行竞速胜出：
// 慢速候选与快速候选同窗起跑，快速者立即胜出交付。
func TestRunRace_SuccessAcrossWindowFill(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2", "uri3")
	defer testNodes.Reset()

	cfg := config.StaticProvider(raceTestConfig())
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

	result, err := RunRace(ctx, testNodes, cfg, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "fast-success" && result != "slow-success" {
		t.Errorf("unexpected result: %s", result)
	}

	if count := atomic.LoadInt32(&callOrder); count < 2 {
		t.Errorf("expected at least 2 parallel candidates, got %d", count)
	}
}

// TestRunRace_CandidateTimeout_RefillsUntilBudget 验证窗口模型下的候选级超时语义：
// 父 ctx 存活时的 context 类候选失败照常补位、不改节点健康记账；
// 发射严格受 (MaxRetries+1)×W 封顶，耗尽后收敛为 context 错误。
func TestRunRace_CandidateTimeout_RefillsUntilBudget(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2", "uri3")
	defer testNodes.Reset()

	cfg := config.StaticProvider(config.AppConfig{
		ParallelPoolEnabled: true,
		ParallelPoolSize:    3,
		MaxRetries:          1, // 预算 = (1+1)*3 = 6
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var launchCount int32

	run := func(ctx context.Context, uri string) (string, error) {
		atomic.AddInt32(&launchCount, 1)
		// 模拟真实流式错误链：executeStreamingAttempt 以 %w 包装上游 context 超时。
		return "", NewContextError(fmt.Errorf("upstream request: %w", context.DeadlineExceeded))
	}

	_, err := RunRace(ctx, testNodes, cfg, run)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected errors.Is(err, DeadlineExceeded) == true, got %v", err)
	}

	// 发射数恰为预算上限：初始 3 发 + 补位 3 发。
	if count := atomic.LoadInt32(&launchCount); count != 6 {
		t.Errorf("expected exactly 6 launches ((1+1)*3 budget), got %d", count)
	}

	// context 类候选失败不记账：健康记录不得出现失败计数或冷却。
	for uri, h := range testNodes.LoadHealth() {
		if h.ConsecutiveFailures != 0 || h.FailCount != 0 || h.CooldownUntil != 0 {
			t.Errorf("node %s health polluted by context-class failure: %+v", uri, h)
		}
	}
}

// TestRunRace_CompleteChat_HardErrorDoesNotBlockSTOP 验证 CompleteChat 语义：
// 第一个候选返回不可重试硬错误（Terminal 类 403）时照常补位，第二个候选的 STOP 胜出。
func TestRunRace_CompleteChat_HardErrorDoesNotBlockSTOP(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2")
	defer testNodes.Reset()

	cfg := config.StaticProvider(raceTestConfigSize(2))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	run := func(ctx context.Context, uri string) (*transform.GeminiResponse, error) {
		if uri == "uri1" {
			// 第一个候选：不可重试硬错误（403 Terminal）。
			return nil, NewPermissionDeniedError("forbidden", nil)
		}
		select {
		case <-time.After(50 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &transform.GeminiResponse{
			Candidates: []*transform.Candidate{{FinishReason: "STOP"}},
		}, nil
	}

	result, err := RunRace(ctx, testNodes, cfg, run, WithWinningCheck(func(resp *transform.GeminiResponse) bool {
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
}

// TestRunRace_CompleteChat_PickBestNonSTOP 验证非流式多个非 STOP 结果收集后
// 使用 pickBestResult 规则：MAX_TOKENS 优先，然后按内容长度。
// 窗口=可用节点数（无富余预算），两候选各跑恰好一发后收敛择优。
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
		defer testNodes.Reset()

		cfg := config.StaticProvider(raceTestConfigSize(2))
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

		result, err := RunRace(ctx, testNodes, cfg, run, WithWinningCheck(func(resp *transform.GeminiResponse) bool {
			return candidateFinishTyped(resp) == "STOP"
		}), WithCollectedFinalizer(pickFinalizer))

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if finish := candidateFinishTyped(result); finish != "MAX_TOKENS" {
			t.Errorf("expected MAX_TOKENS to win, got %s", finish)
		}
		if count := atomic.LoadInt32(&callOrder); count != 2 {
			t.Errorf("expected both candidates to launch exactly once, got %d", count)
		}
	})

	t.Run("longer_text_when_same_finish_reason", func(t *testing.T) {
		setupRaceNodes(t, "uri1", "uri2")
		defer testNodes.Reset()

		cfg := config.StaticProvider(raceTestConfigSize(2))
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

		result, err := RunRace(ctx, testNodes, cfg, run, WithWinningCheck(func(resp *transform.GeminiResponse) bool {
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

// TestRunRace_CollectResult_TriggersRefill 验证非流式收集补位：
// 候选成功但非 STOP（收集）同样释放槽位并补位下一节点，
// 直至预算耗尽后统一择优（长文本胜出）。
func TestRunRace_CollectResult_TriggersRefill(t *testing.T) {
	setupRaceNodes(t, "a", "bbbb")
	defer testNodes.Reset()

	cfg := config.StaticProvider(config.AppConfig{
		ParallelPoolEnabled: true,
		ParallelPoolSize:    1,
		MaxRetries:          1, // 预算 = (1+1)*1 = 2
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var mu sync.Mutex
	seen := map[string]bool{}

	run := func(ctx context.Context, uri string) (*transform.GeminiResponse, error) {
		mu.Lock()
		seen[uri] = true
		mu.Unlock()
		return &transform.GeminiResponse{
			Candidates: []*transform.Candidate{{
				FinishReason: "MAX_TOKENS",
				Content:      &transform.Content{Role: "model", Parts: []transform.Part{{Text: strings.Repeat("x", len(uri))}}},
			}},
		}, nil
	}

	result, err := RunRace(ctx, testNodes, cfg, run,
		WithWinningCheck(func(resp *transform.GeminiResponse) bool {
			return candidateFinishTyped(resp) == "STOP"
		}),
		WithCollectedFinalizer(func(results []raceResult[*transform.GeminiResponse]) (*transform.GeminiResponse, error) {
			cr := make([]candidateResult, len(results))
			for i, r := range results {
				cr[i] = candidateResult{proxyURI: r.uri, resp: r.val, err: r.err}
			}
			return pickBestResult(cr, &transform.TextStrategy{})
		}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mu.Lock()
	distinct := len(seen)
	mu.Unlock()
	if distinct != 2 {
		t.Errorf("expected collect-refill to cover both nodes, saw %d distinct uris", distinct)
	}
	if got := responseContentLengthTyped(result); got != len("bbbb") {
		t.Errorf("expected longer collected text to win (len %d), got len %d", len("bbbb"), got)
	}
}

// TestRunRace_Streaming_PermissionDenied_NoFailFast 验证 403 不会触发 fail-fast：
// uri1 返回 403 PermissionDenied，uri2 延迟后交付有效首帧并胜出。
func TestRunRace_Streaming_PermissionDenied_NoFailFast(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2")
	defer testNodes.Reset()

	cfg := config.StaticProvider(raceTestConfigSize(2))
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

	result, err := RunRace(ctx, testNodes, cfg, run, WithPreserveRaceCtxOnWin[<-chan StreamChunk](), WithFailFastOnHardError[<-chan StreamChunk]())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	launched.Wait()
	first := <-result
	if first.Err != nil {
		t.Fatalf("unexpected error chunk: %v", first.Err)
	}
}

// TestRunRace_Streaming_GlobalHardError_FailFast 验证全局硬错误（invalid 400）触发 fail-fast：
// 双候选经释放屏障确保都已发射，首个硬错误结果到达即终止整个请求，绝不补位。
func TestRunRace_Streaming_GlobalHardError_FailFast(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2")
	defer testNodes.Reset()

	cfg := config.StaticProvider(raceTestConfigSize(2))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var launchCount int32

	run := func(ctx context.Context, uri string) (<-chan StreamChunk, error) {
		atomic.AddInt32(&launchCount, 1)
		started <- struct{}{}
		<-release
		return nil, NewInvalidArgumentError("invalid model name", nil)
	}

	done := make(chan error, 1)
	go func() {
		_, err := RunRace(ctx, testNodes, cfg, run, WithFailFastOnHardError[<-chan StreamChunk]())
		done <- err
	}()

	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("candidates did not all enter run")
		}
	}
	close(release)

	if err := <-done; err == nil {
		t.Error("expected error, got nil")
	}
	// 初始填充同步发射 2 发；fail-fast 在首个结果处理时终止，绝无第三发。
	if count := atomic.LoadInt32(&launchCount); count != 2 {
		t.Errorf("expected exactly 2 launches (full-window fill then fail-fast abort), got %d", count)
	}
}

// TestRunRace_Streaming_FailFastOnHardError 验证流式首帧的全局硬错误快速终止：
// 双候选经释放屏障确保都已发射，notfound 硬错误首达即终止，不消耗剩余预算补位。
func TestRunRace_Streaming_FailFastOnHardError(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2")
	defer testNodes.Reset()

	cfg := config.StaticProvider(raceTestConfigSize(2))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var launchCount int32

	run := func(ctx context.Context, uri string) (<-chan StreamChunk, error) {
		atomic.AddInt32(&launchCount, 1)
		started <- struct{}{}
		<-release
		return nil, NewNotFoundError("model not found", nil)
	}

	done := make(chan error, 1)
	go func() {
		_, err := RunRace(ctx, testNodes, cfg, run, WithFailFastOnHardError[<-chan StreamChunk]())
		done <- err
	}()

	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("candidates did not all enter run")
		}
	}
	close(release)

	if err := <-done; err == nil {
		t.Error("expected error, got nil")
	}
	if count := atomic.LoadInt32(&launchCount); count != 2 {
		t.Errorf("expected exactly 2 launches (full-window fill then fail-fast abort), got %d", count)
	}
}

// TestRunRace_AuthErrorDisablesNode 验证 auth 错误分支：
// RunRace 遇到 Kind == "auth" 的 VertexError 后，通过 BatchUpdateNodesDisabled
// 将该节点在节点池中标记为禁用；全员禁用后在飞归零即收敛（宽松通道也排除 Disabled）。
func TestRunRace_AuthErrorDisablesNode(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2")
	defer testNodes.Reset()

	cfg := config.StaticProvider(raceTestConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	run := func(ctx context.Context, uri string) (string, error) {
		return "", NewAuthenticationError("test auth failure", nil)
	}

	_, err := RunRace(ctx, testNodes, cfg, run)
	if err == nil {
		t.Error("expected error from RunRace, got nil")
	}

	ns := testNodes.LoadNodes()
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
	defer testNodes.Reset()

	cfg := config.StaticProvider(raceTestConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	run := func(ctx context.Context, uri string) (string, error) {
		if uri == "uri1" {
			panic("simulated node panic")
		}
		return "result-uri2", nil
	}

	result, err := RunRace(ctx, testNodes, cfg, run)
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
		health := testNodes.LoadHealth()
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
// （ErrStreamIdleTimeout）会触发节点的短时避让（RecordRateLimit），使僵死/慢节点被隔离。
// 宽松通道忽略冷却仅用于非常时期强行补位，冷却标记本身必须如实落账。
func TestRunRace_StreamIdleTimeout_TriggersRateLimitCooldown(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2")
	defer testNodes.Reset()

	cfg := config.StaticProvider(raceTestConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	run := func(ctx context.Context, uri string) (string, error) {
		return "", NewNetworkError(ErrStreamIdleTimeout)
	}

	if _, err := RunRace(ctx, testNodes, cfg, run); err == nil {
		t.Fatal("expected error, got nil")
	}

	now := time.Now().Unix()
	health := testNodes.LoadHealth()
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

// TestRunRace_InitialFill_FallsBackToRelaxedOnCooldown 验证初始填充的宽松兜底：
// 全员处于 429 冷却期时严格选点为空，引擎以宽松通道拉起窗口而非误降级直连；
// 冷却只是保护措施，非常时期按优先级照样开跑。
func TestRunRace_InitialFill_FallsBackToRelaxedOnCooldown(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2")
	defer testNodes.Reset()

	testNodes.RecordRateLimit("uri1", 60)
	testNodes.RecordRateLimit("uri2", 60)

	cfg := config.StaticProvider(raceTestConfigSize(2))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var launchCount int32
	run := func(ctx context.Context, uri string) (string, error) {
		atomic.AddInt32(&launchCount, 1)
		return "", NewRateLimitError("too many requests 429", 0, nil)
	}

	_, err := RunRace(ctx, testNodes, cfg, run)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if count := atomic.LoadInt32(&launchCount); count != 2 {
		t.Errorf("expected both cooling-down nodes to be launched via relaxed path, got %d", count)
	}
}

// TestRunRace_LaunchBudgetHardCap 验证发射硬预算：
// 任意失败序列下总发射数恰为 (MaxRetries+1)×W，绝不越界。
func TestRunRace_LaunchBudgetHardCap(t *testing.T) {
	uris := make([]string, 5)
	for i := range uris {
		uris[i] = fmt.Sprintf("uri%d", i+1)
	}
	setupRaceNodes(t, uris...)
	defer testNodes.Reset()

	cfg := config.StaticProvider(config.AppConfig{
		ParallelPoolEnabled: true,
		ParallelPoolSize:    3,
		MaxRetries:          1, // 预算 = 6
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var launched int32
	run := func(ctx context.Context, uri string) (string, error) {
		atomic.AddInt32(&launched, 1)
		// 错误文案避开自动禁用关键词（dial/refused/timeout/connection 等），
		// 保证节点保持可选、补位链条可以吃满预算。
		return "", NewNetworkError(fmt.Errorf("transient upstream blip"))
	}

	_, err := RunRace(ctx, testNodes, cfg, run)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if count := atomic.LoadInt32(&launched); count != 6 {
		t.Errorf("expected exactly 6 launches ((1+1)*3 hard budget), got %d", count)
	}
}

// TestRunRace_PerCandidateTimeout_AnchoredAtLaunch 验证每候选独立超时的启动锚定：
// 窗口=1、预算=2 时两个候选串行接力，各自获得完整 1s 个人时限（总耗时 ≈2s）。
// 若超时是"从请求起点共享"的口径，第二发会立即到期（总耗时 ≈1s），据此判定语义正确。
// 说明：秒级配置粒度决定本用例下限 ~1.5s，属超时语义验证的合理例外（规范上限 500ms）。
func TestRunRace_PerCandidateTimeout_AnchoredAtLaunch(t *testing.T) {
	setupRaceNodes(t, "cand1", "cand2")
	defer testNodes.Reset()

	cfg := config.StaticProvider(config.AppConfig{
		ParallelPoolEnabled:   true,
		ParallelPoolSize:      1,
		MaxRetries:            1,
		RequestTimeoutSeconds: 1,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var attempts int32
	run := func(ctx context.Context, uri string) (string, error) {
		atomic.AddInt32(&attempts, 1)
		<-ctx.Done()
		return "", ctx.Err()
	}

	start := time.Now()
	_, err := RunRace(ctx, testNodes, cfg, run)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded convergence, got %v", err)
	}
	if n := atomic.LoadInt32(&attempts); n != 2 {
		t.Fatalf("expected exactly 2 sequential attempts ((1+1)*1 budget), got %d", n)
	}
	if elapsed < 1500*time.Millisecond || elapsed > 3500*time.Millisecond {
		t.Errorf("expected ~2s wall clock (two fresh 1s candidate deadlines), got %v", elapsed)
	}
}

func TestRunRace_PickBestError_Priority(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2")
	defer testNodes.Reset()

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

		_, err := RunRace(ctx, testNodes, cfg, run)
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

		_, err := RunRace(ctx, testNodes, cfg, run)
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

		_, err := RunRace(ctx, testNodes, cfg, run)
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
// 预算 = 窗口 = 20：全员各自失败一发后立即收敛，无补位空间。
func TestRunRace_Convergence_PriorityLadder(t *testing.T) {
	uris := make([]string, 20)
	for i := range uris {
		uris[i] = fmt.Sprintf("uri%d", i+1)
	}
	setupRaceNodes(t, uris...)
	defer testNodes.Reset()

	cfg := config.StaticProvider(config.AppConfig{
		ParallelPoolEnabled: true,
		ParallelPoolSize:    20,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	newNetwork := func() *VertexError { return NewNetworkError(fmt.Errorf("boom")) }

	runRaceWith := func(one func(uri string) error) error {
		_, err := RunRace(ctx, testNodes, cfg, func(ctx context.Context, uri string) (map[string]any, error) {
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

// TestRunRace_ActiveURINeverRelaunched 是方案 §9 I1 的去重回归锚：
// 同一 URI 在飞期间（初始填充与补位两条路径）绝不重复发射；
// 结果消费释放占位后，允许跨轮复用（窗口模型核心语义）。
func TestRunRace_ActiveURINeverRelaunched(t *testing.T) {
	setupRaceNodes(t, "hang", "flaky")
	defer testNodes.Reset()

	cfg := config.StaticProvider(config.AppConfig{
		ParallelPoolEnabled: true,
		ParallelPoolSize:    2,
		MaxRetries:          5,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var hangLaunches, flakyLaunches int32
	hangStarted := make(chan struct{}, 8)
	release := make(chan struct{})

	run := func(ctx context.Context, uri string) (string, error) {
		if uri == "hang" {
			atomic.AddInt32(&hangLaunches, 1)
			hangStarted <- struct{}{}
			<-release
			return "ok-hang", nil
		}
		// 错误文案避开自动禁用关键词（dial/refused/timeout 等）。
		atomic.AddInt32(&flakyLaunches, 1)
		return "", NewNetworkError(fmt.Errorf("transient upstream blip"))
	}

	type raceOut struct {
		val string
		err error
	}
	done := make(chan raceOut, 1)
	go func() {
		v, err := RunRace(ctx, testNodes, cfg, run)
		done <- raceOut{v, err}
	}()

	// 等待初始填充完成：hang 一发 + flaky 一发。
	select {
	case <-hangStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("initial fill did not launch hang node")
	}
	fillDeadline := time.After(2 * time.Second)
	for atomic.LoadInt32(&flakyLaunches) == 0 {
		select {
		case <-fillDeadline:
			t.Fatal("initial fill did not launch flaky node")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if n := atomic.LoadInt32(&hangLaunches); n != 1 {
		t.Fatalf("expected exactly 1 initial launch of hang node, got %d", n)
	}

	// flaky 持续失败触发多轮补位；期间 hang 始终在飞，绝不可被重复发射。
	deadline := time.After(500 * time.Millisecond)
	for atomic.LoadInt32(&flakyLaunches) < 3 {
		select {
		case <-hangStarted:
			t.Fatal("active hang node was relaunched while in flight")
		case <-deadline:
			t.Fatalf("expected multiple flaky refill cycles, got %d", atomic.LoadInt32(&flakyLaunches))
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	if n := atomic.LoadInt32(&hangLaunches); n != 1 {
		t.Fatalf("hang node relaunched while in flight: %d launches", n)
	}

	close(release)
	out := <-done
	if out.err != nil {
		t.Fatalf("expected success after release, got error: %v", out.err)
	}
	if out.val != "ok-hang" {
		t.Errorf("expected ok-hang, got %q", out.val)
	}
	if n := atomic.LoadInt32(&hangLaunches); n != 1 {
		t.Errorf("hang node should never be relaunched in this scenario, got %d total launches", n)
	}
}

// TestApplyNodeFailure_EmptyURI_NoOp 验证空 URI（直连分支）的 ApplyNodeFailure 无副作用：
// 不产生健康记录、不 panic、不误塞 auth 判定。
func TestApplyNodeFailure_EmptyURI_NoOp(t *testing.T) {
	testNodes.Reset()
	defer testNodes.Reset()

	applyNodeFailure(testNodes, "", NewAuthenticationError("boom", nil))
	applyNodeFailure(testNodes, "", NewNetworkError(ErrStreamIdleTimeout))
	applyNodeFailure(testNodes, "", NewRateLimitError("too many", 429, nil))

	if len(testNodes.LoadHealth()) != 0 {
		t.Errorf("空 URI 不应产生任何健康记录，实际 %d 条", len(testNodes.LoadHealth()))
	}
}
