package vertex

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/engine/transform"
	"github.com/bsfdsagfadg/vertex/internal/infra/config"
)

// TestStreamParallel_FailoverOnEmptyResponse 验证 StreamParallel 在节点 A 返回空流
// （channel 无数据直接关闭）时能故障转移到节点 B。
func TestStreamParallel_FailoverOnEmptyResponse(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2")
	defer testNodes.Reset()

	cfg := config.StaticProvider(raceTestConfigSize(2))
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
				// Node A：channel 直接关闭（空流），触发 wrappedOp 的 EmptyResponseError 分支。
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

	StreamParallel(ctx, testNodes, cfg, "gemini-2.5-flash", op, yield, nil)
	launched.Wait()

	if len(gotChunks) == 0 {
		t.Fatal("expected at least one chunk from node B")
	}
	if gotChunks[0].Err != nil {
		t.Fatalf("expected success chunk, got error: %v", gotChunks[0].Err)
	}
}

// TestStreamParallel_Direct_SequentialRefill 验证直连降级模式（池关闭，窗口=1）
// 的顺序接力语义：第 1 发 Transient 失败后无退避立即第 2 发并成功交付。
func TestStreamParallel_Direct_SequentialRefill(t *testing.T) {
	start := time.Now()
	defer func() {
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("直连接力应无退避短促完成，实际 %v", elapsed)
		}
	}()

	testNodes.Reset()
	defer testNodes.Reset()

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

	StreamParallel(ctx, testNodes, cfg, "test-model", op, yield, nil)

	if gotData != "recovered" {
		t.Errorf("expected sequential relay to recover content, got %q", gotData)
	}
	if n := atomic.LoadInt32(&attempts); n != 2 {
		t.Errorf("expected exactly 2 attempts ((1+1)*1 budget), got %d", n)
	}
}

// TestStreamParallel_Window_RefillRecover 验证多候选场景下失败即补位：
// 两节点首发均瞬态失败，补位发次成功交付内容。
func TestStreamParallel_Window_RefillRecover(t *testing.T) {
	setupRaceNodes(t, "uri1", "uri2")
	defer testNodes.Reset()

	cfg := config.StaticProvider(config.AppConfig{
		ParallelPoolEnabled: true,
		ParallelPoolSize:    2,
		MaxRetries:          1, // 预算 = 4
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
				// 注意：错误文案避开 dial/refused/timeout/connection 等关键词，
				// 防止触发节点池网络类故障自动禁用（本用例要验证的是补位而非禁用）。
				ch <- StreamChunk{Err: NewNetworkError(fmt.Errorf("upstream 503 blip"))}
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

	StreamParallel(ctx, testNodes, cfg, "test-model", op, yield, nil)

	if !strings.HasPrefix(gotText, "ok-") {
		t.Errorf("expected refill to deliver content, got %q", gotText)
	}
	if n := atomic.LoadInt32(&attempts); n < 3 {
		t.Errorf("expected >= 3 attempts (refill beyond initial fill), got %d", n)
	}
}

// TestStreamParallel_Direct_Truncated_BudgetSurfacesCommitted 验证直连模式下
// 候选级 Truncated 错误（帧未达客户端、缓冲即弃）按普通失败顺序接力至预算耗尽，
// 最终以最高优先级透传 Committed(Truncated) 错误。
func TestStreamParallel_Direct_Truncated_BudgetSurfacesCommitted(t *testing.T) {
	testNodes.Reset()
	defer testNodes.Reset()

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
	StreamParallel(ctx, testNodes, cfg, "test-model", op, func(chunk StreamChunk) bool {
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
	if n := atomic.LoadInt32(&attempts); n != 4 {
		t.Errorf("expected exactly (3+1)*1 sequential attempts, got %d", n)
	}
}

// TestStreamParallel_ClientCancel_Silent 验证客户端取消后 StreamParallel 静默返回：
// 不产错误帧、不产数据帧。
func TestStreamParallel_ClientCancel_Silent(t *testing.T) {
	testNodes.Reset()
	defer testNodes.Reset()

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
	StreamParallel(ctx, testNodes, cfg, "test-model", op, func(chunk StreamChunk) bool {
		got = append(got, chunk)
		return true
	}, nil)

	if len(got) != 0 {
		t.Errorf("expected silent return on client cancel, got %d chunks", len(got))
	}
}

// TestStreamParallel_FailFast_NoRetry 验证 FailFast（invalid 等全局硬错误）首达即终止：
// attempts==1，错误帧如实透传，即使直连预算尚余也不接力。
func TestStreamParallel_FailFast_NoRetry(t *testing.T) {
	testNodes.Reset()
	defer testNodes.Reset()

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
	StreamParallel(ctx, testNodes, cfg, "test-model", op, func(chunk StreamChunk) bool {
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
		t.Errorf("expected single attempt for FailFast error, got %d attempts", n)
	}
}

// TestStreamParallel_Direct_Terminal_RelayUntilBudget 验证直连模式下的 Terminal 错误：
// 与主路径口径一致——除 FailFast 外照常接力直至预算耗尽（(3+1)*1=4 发），最终透传 Terminal。
func TestStreamParallel_Direct_Terminal_RelayUntilBudget(t *testing.T) {
	testNodes.Reset()
	defer testNodes.Reset()

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
	StreamParallel(ctx, testNodes, cfg, "test-model", op, func(chunk StreamChunk) bool {
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
	if n := atomic.LoadInt32(&attempts); n != 4 {
		t.Errorf("expected exactly 4 sequential relay attempts ((3+1)*1 budget), got %d", n)
	}
}
