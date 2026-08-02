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
)

// stuckRun 模拟"网络延迟一直连不上"的卡死节点：阻塞直到 ctx 取消，返回 ctx.Err()。
// 与真实 run（会话层有 180s RequestTimeout 兜底）的区别：这里在 RaceTimeout 到点就返回。
func stuckRun(ctx context.Context, _ string) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

func raceTestConfig(raceTimeout int) config.ConfigProvider {
	return config.StaticProvider(config.AppConfig{ //nolint:exhaustruct
		ParallelPoolEnabled: true,
		ParallelPoolSize:    3,
		ParallelNodeTopK:    80,
		StickyNodePriority:  false,
		RaceTimeout:         raceTimeout,
	})
}

func raceTestNodes(t *testing.T) {
	t.Helper()
	nodes.MergeNodes([]nodes.Node{
		{Type: "http", Name: "n1", RawURI: "http://node1:8080"},
		{Type: "http", Name: "n2", RawURI: "http://node2:8080"},
		{Type: "http", Name: "n3", RawURI: "http://node3:8080"},
	})
	t.Cleanup(func() {
		nodes.DeleteNode("http://node1:8080")
		nodes.DeleteNode("http://node2:8080")
		nodes.DeleteNode("http://node3:8080")
	})
}

// TestRunRace_StuckNodeEliminatedByTimeout 验证核心修复：
// 全部节点都"卡死"时，RaceTimeout 到点后每个节点被单独淘汰（返回 503），
// 竞速正常结束，而不是被卡死节点拖住挂到 180s。
func TestRunRace_StuckNodeEliminatedByTimeout(t *testing.T) {
	raceTestNodes(t)

	start := time.Now()
	_, err := RunRace(context.Background(), raceTestConfig(1), stuckRun)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("全节点卡死时应在超时后返回错误")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("竞速被卡死节点拖住: 耗时 %v，应约 1 秒后全部淘汰", elapsed)
	}
	ve, ok := err.(*VertexError)
	if !ok || ve.Kind != "unavailable" {
		t.Fatalf("应返回 503 超时错误（可重试），got: %v", err)
	}
}

// TestRunRace_FastNodeWinsDespiteStuck 验证不影响正常节点：
// 一个节点立即胜出时，其余卡死节点不应拖慢胜出（RaceTimeout 不误伤快节点）。
func TestRunRace_FastNodeWinsDespiteStuck(t *testing.T) {
	raceTestNodes(t)

	var winner atomic.Bool
	run := func(ctx context.Context, uri string) (string, error) {
		if uri == "http://node1:8080" {
			winner.Store(true)
			return "ok", nil
		}
		return stuckRun(ctx, uri)
	}

	start := time.Now()
	val, err := RunRace(context.Background(), raceTestConfig(1), run)
	if err != nil || val != "ok" {
		t.Fatalf("快节点应立即胜出, val=%v err=%v", val, err)
	}
	if !winner.Load() {
		t.Fatal("node1 应被选中并胜出")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("胜出不应被卡死节点拖慢, 耗时 %v", elapsed)
	}
}

func TestRunRaceUsesConfiguredHedgeDelay(t *testing.T) {
	raceTestNodes(t)
	cfg := config.StaticProvider(config.AppConfig{ //nolint:exhaustruct
		ParallelPoolEnabled:      true,
		ParallelPoolSize:         3,
		ParallelNodeTopK:         80,
		StickyNodePriority:       false,
		ParallelPoolDelayDynamic: false,
		ParallelPoolDelayMs:      150,
	})

	started := make(chan time.Time, 3)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := RunRace(ctx, cfg, func(candidateCtx context.Context, _ string) (string, error) {
		started <- time.Now()
		<-candidateCtx.Done()
		return "", candidateCtx.Err()
	})
	if err == nil {
		t.Fatal("父上下文超时后应返回错误")
	}

	first := <-started
	second := <-started
	if elapsed := second.Sub(first); elapsed < 100*time.Millisecond {
		t.Fatalf("第二候选启动过早，固定 hedge 延迟未生效: %v", elapsed)
	}
}

// TestRunRace_NoTimeoutKeepsLegacyBehavior 验证 RaceTimeout=0（默认）时行为不变：
// 卡死节点不会提前被淘汰（保留原有等待语义，由上层 ctx 控制）。
func TestRunRace_NoTimeoutKeepsLegacyBehavior(t *testing.T) {
	raceTestNodes(t)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := RunRace(ctx, raceTestConfig(0), stuckRun)
	elapsed := time.Since(start)

	// 无 RaceTimeout：等 ctx（300ms）取消后整体退出，返回 ctx.Err()。
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("应返回 ctx 超时/取消错误, got: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("应随 ctx 在 ~300ms 退出, 实际 %v", elapsed)
	}
}

// TestRunRace_RoundRelaySwitchesToFreshNodes 验证关单节点重试时的轮次换批：
// 并发 2、重试 1（roundBudget=1）：第一轮 2 个节点失败后，换一批从未用过的 2 个节点再试，
// 且每个节点最多被尝试一次（不重复使用节点）。
func TestRunRace_RoundRelaySwitchesToFreshNodes(t *testing.T) {
	nodes.MergeNodes([]nodes.Node{
		{Type: "http", Name: "n1", RawURI: "http://node1:8080"},
		{Type: "http", Name: "n2", RawURI: "http://node2:8080"},
		{Type: "http", Name: "n3", RawURI: "http://node3:8080"},
		{Type: "http", Name: "n4", RawURI: "http://node4:8080"},
	})
	t.Cleanup(func() {
		for i := 1; i <= 4; i++ {
			nodes.DeleteNode(fmt.Sprintf("http://node%d:8080", i))
		}
	})

	cfg := config.StaticProvider(config.AppConfig{ //nolint:exhaustruct
		ParallelPoolEnabled:      true,
		ParallelPoolSize:         2,
		ParallelNodeTopK:         80,
		StickyNodePriority:       false,
		ParallelPoolRetryEnabled: false, // 关单节点重试 → 竞速轮次换批
		MaxRetries:               1,     // 换 1 批新节点
	})

	var mu sync.Mutex
	attempted := map[string]int{}
	run := func(_ context.Context, uri string) (string, error) {
		mu.Lock()
		attempted[uri]++
		total := len(attempted)
		count := attempted[uri]
		mu.Unlock()
		if count > 1 {
			return "", errors.New("node reused") // 同一节点不应被再次尝试
		}
		// 前两个尝试的节点（即第一轮）全部失败；后续轮次成功。
		// 这不依赖于节点随机选择的顺序。
		if total <= 2 {
			return "", NewRateLimitError("quota", 0)
		}
		return "ok", nil
	}

	val, err := RunRace(context.Background(), cfg, run)
	if err != nil || val != "ok" {
		t.Fatalf("第二轮应胜出, val=%v err=%v", val, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(attempted) < 3 {
		t.Fatalf("应至少尝试 3 个不同节点(跨两批), got %v", attempted)
	}
	for uri, c := range attempted {
		if c != 1 {
			t.Fatalf("节点 %s 被尝试 %d 次, 应只 1 次", uri, c)
		}
	}
}

func TestStreamParallelWinnerContinuesBeyondRaceTimeout(t *testing.T) {
	raceTestNodes(t)

	op := func(ctx context.Context, _ string) <-chan StreamChunk {
		ch := make(chan StreamChunk, 2)
		go func() {
			defer close(ch)
			select {
			case ch <- StreamChunk{Data: map[string]any{"text": "first"}}:
			case <-ctx.Done():
				return
			}
			timer := time.NewTimer(1200 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				return
			}
			select {
			case ch <- StreamChunk{Data: map[string]any{"text": "after-timeout"}}:
			case <-ctx.Done():
			}
		}()
		return ch
	}

	var got []string
	StreamParallel(context.Background(), raceTestConfig(1), op, func(chunk StreamChunk) bool {
		if chunk.Err != nil {
			t.Fatalf("胜出流不应被 race_timeout 截断: %v", chunk.Err)
		}
		if text, _ := chunk.Data["text"].(string); text != "" {
			got = append(got, text)
		}
		return true
	})
	if fmt.Sprint(got) != "[first after-timeout]" {
		t.Fatalf("跨过 race_timeout 后仍应收到完整流, got %v", got)
	}
}

func TestStreamParallelYieldStopCancelsUpstream(t *testing.T) {
	raceTestNodes(t)

	var canceled atomic.Int32
	op := func(ctx context.Context, _ string) <-chan StreamChunk {
		ch := make(chan StreamChunk, 1)
		go func() {
			defer close(ch)
			select {
			case ch <- StreamChunk{Data: map[string]any{"text": "first"}}:
			case <-ctx.Done():
			}
			<-ctx.Done()
			canceled.Add(1)
		}()
		return ch
	}

	StreamParallel(context.Background(), raceTestConfig(0), op, func(StreamChunk) bool { return false })
	deadline := time.Now().Add(2 * time.Second)
	for canceled.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if canceled.Load() != 3 {
		t.Fatalf("客户端停止消费应取消所有上游候选, canceled=%d", canceled.Load())
	}
}

func TestStreamParallelCanceledLosersAreNotRecordedAsEmptyFailures(t *testing.T) {
	raceTestNodes(t)
	var losersCanceled atomic.Int32
	op := func(ctx context.Context, uri string) <-chan StreamChunk {
		ch := make(chan StreamChunk, 1)
		go func() {
			defer close(ch)
			if strings.Contains(uri, "node1") {
				ch <- StreamChunk{Data: map[string]any{"text": "winner"}}
				return
			}
			<-ctx.Done()
			losersCanceled.Add(1)
		}()
		return ch
	}
	StreamParallel(context.Background(), raceTestConfig(0), op, func(StreamChunk) bool { return true })

	deadline := time.Now().Add(2 * time.Second)
	for losersCanceled.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if losersCanceled.Load() != 2 {
		t.Fatalf("落败候选未全部取消: %d", losersCanceled.Load())
	}
	time.Sleep(50 * time.Millisecond)
	health := nodes.LoadHealth()
	for _, uri := range []string{"http://node2:8080", "http://node3:8080"} {
		if h := health[uri]; h != nil && h.FailCount > 0 {
			t.Fatalf("被取消的 loser 不应记录为空流失败: %+v", health)
		}
	}
}

func TestRunRaceParentCancellationPropagatesToCandidates(t *testing.T) {
	raceTestNodes(t)
	ctx, cancel := context.WithCancel(context.Background())
	var started atomic.Int32
	var canceled atomic.Int32
	done := make(chan error, 1)
	go func() {
		_, err := RunRace(ctx, raceTestConfig(0), func(candidateCtx context.Context, _ string) (string, error) {
			started.Add(1)
			<-candidateCtx.Done()
			canceled.Add(1)
			return "", candidateCtx.Err()
		})
		done <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for started.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("父请求取消应原样返回 context.Canceled, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("父请求取消后竞速未及时退出")
	}
	deadline = time.Now().Add(2 * time.Second)
	for canceled.Load() < started.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if canceled.Load() != started.Load() {
		t.Fatalf("父请求取消未传播到所有已启动候选: started=%d canceled=%d", started.Load(), canceled.Load())
	}
}

func TestRunRaceRecoversCandidatePanic(t *testing.T) {
	raceTestNodes(t)
	_, err := RunRace(context.Background(), raceTestConfig(0), func(context.Context, string) (string, error) {
		panic("boom")
	})
	if err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("候选 panic 应转换为可诊断错误, got %v", err)
	}
}

func TestRunRaceReturnsGlobalRequestError(t *testing.T) {
	raceTestNodes(t)
	_, err := RunRace(context.Background(), raceTestConfig(0), func(_ context.Context, uri string) (string, error) {
		switch {
		case strings.Contains(uri, "node1"):
			return "", NewInvalidArgumentError("bad request")
		case strings.Contains(uri, "node2"):
			return "", NewRateLimitError("quota", 0)
		default:
			return "", NewPermissionDeniedError("permission")
		}
	})
	var ve *VertexError
	if !errors.As(err, &ve) || ve.Kind != "invalid" {
		t.Fatalf("请求级参数错误应优先返回且不被节点限流掩盖, got %v", err)
	}
}

func TestStreamParallelReportsEmptyResponse(t *testing.T) {
	raceTestNodes(t)
	var gotErr *VertexError
	StreamParallel(context.Background(), raceTestConfig(0), func(context.Context, string) <-chan StreamChunk {
		ch := make(chan StreamChunk)
		close(ch)
		return ch
	}, func(chunk StreamChunk) bool {
		gotErr = chunk.Err
		return true
	})
	if gotErr == nil || gotErr.Kind != "empty" {
		t.Fatalf("所有候选空流应返回 empty 错误, got %+v", gotErr)
	}
}
