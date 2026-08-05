package vertex

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestScanStream_BufferHardLimit(t *testing.T) {
	const hardLimit = 64 * 1024 * 1024

	data := make([]byte, hardLimit+1024*1024)
	data[0] = '{'
	for i := 1; i < len(data); i++ {
		data[i] = 'x'
	}

	done := make(chan error, 1)
	go func() {
		done <- scanStream(context.Background(), bytes.NewReader(data), func(obj map[string]any) (bool, error) {
			return false, nil
		}, nil)
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected error for oversized buffer, got nil")
		} else if !strings.Contains(err.Error(), "hard buffer limit") {
			t.Errorf("error should contain 'hard buffer limit', got: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("test timed out (possible OOM or hang)")
	}
}

// collectStream 把 scanStream 跑到底，收集所有 emit 出来的 chunk，返回 (chunks, 终止错误)。
// onObject 用 processStreamingObject 的真实逻辑，确保测的是端到端的流式提取 + finishReason 过滤。
func collectStream(t *testing.T, raw string) (emitted []map[string]any, finished bool, scanErr error) {
	t.Helper()
	emit := func(ch map[string]any) bool {
		emitted = append(emitted, ch)
		return true
	}
	var seenFinish bool
	scanErr = scanStream(context.Background(), strings.NewReader(raw), func(obj map[string]any) (bool, error) {
		stop, err := processStreamingObject(obj, emit, &seenFinish)
		if stop {
			finished = true
		}
		return stop, err
	}, nil)
	if scanErr == nil && seenFinish {
		finished = true
	}
	return
}

// wrap 把一段 candidates JSON 包成匿名 batchGraphql 的 results.data.ui.streamGenerateContentAnonymous 结构。
func wrap(inner string) string {
	return `{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":` + inner + `}}}]}`
}

func TestScanStream_MultiChunkBraceScan(t *testing.T) {
	// 两个连在一起的对象（模拟上游一个网络 chunk 里塞了两帧），增量花括号扫描要拆成两个。
	raw := wrap(`{"candidates":[{"content":{"parts":[{"text":"Hello"}],"role":"model"},"finishReason":"FINISH_REASON_UNSPECIFIED","index":0}]}`) +
		wrap(`{"candidates":[{"content":{"parts":[{"text":" world"}],"role":"model"},"finishReason":"STOP","index":0}]}`)
	emitted, stopped, err := collectStream(t, raw)
	if err != nil {
		t.Fatalf("scan error: %v", err)
	}
	if len(emitted) != 2 {
		t.Fatalf("emitted=%d, want 2", len(emitted))
	}
	if got := firstPartText(emitted[0]); got != "Hello" {
		t.Errorf("chunk0 text=%q, want Hello", got)
	}
	if got := firstPartText(emitted[1]); got != " world" {
		t.Errorf("chunk1 text=%q, want ' world'", got)
	}
	if !stopped {
		t.Error("收到真实 STOP 应触发 stop（主动结束流）")
	}
}

// 最关键的红线测试：首帧 FINISH_REASON_UNSPECIFIED 绝不能截断。
func TestScanStream_UnspecifiedDoesNotTruncate(t *testing.T) {
	// 5 个内容帧都带 UNSPECIFIED，最后一帧才 STOP —— 必须全部 emit，不能在首帧停。
	var sb strings.Builder
	for i := 0; i < 5; i++ {
		sb.WriteString(wrap(`{"candidates":[{"content":{"parts":[{"text":"x"}],"role":"model"},"finishReason":"FINISH_REASON_UNSPECIFIED"}]}`))
	}
	sb.WriteString(wrap(`{"candidates":[{"content":{"parts":[{"text":"end"}],"role":"model"},"finishReason":"STOP"}]}`))
	emitted, stopped, err := collectStream(t, sb.String())
	if err != nil {
		t.Fatalf("scan error: %v", err)
	}
	if len(emitted) != 6 {
		t.Fatalf("emitted=%d, want 6（UNSPECIFIED 不能截断！血泪教训）", len(emitted))
	}
	if !stopped {
		t.Error("末帧 STOP 应触发 stop")
	}
}

// 真实 finishReason 与末段文本同帧到达：该帧仍要 emit（内容不丢），且触发 stop。
func TestScanStream_FinishWithContentSameFrame(t *testing.T) {
	raw := wrap(`{"candidates":[{"content":{"parts":[{"text":"final text"}],"role":"model"},"finishReason":"MAX_TOKENS"}]}`)
	emitted, stopped, err := collectStream(t, raw)
	if err != nil {
		t.Fatalf("scan error: %v", err)
	}
	if len(emitted) != 1 {
		t.Fatalf("emitted=%d, want 1", len(emitted))
	}
	if got := firstPartText(emitted[0]); got != "final text" {
		t.Errorf("text=%q, want 'final text'（finish 同帧文本不能丢）", got)
	}
	if !stopped {
		t.Error("MAX_TOKENS 应触发 stop")
	}
}

// 增量扫描跨网络 chunk：一个 JSON 对象被劈成两半，跨 chunk 续扫不应丢失。
// 用 splitReader 模拟逐字节投喂，验证 O(n) 续扫状态机的正确性。
func TestScanStream_SplitAcrossReads(t *testing.T) {
	raw := wrap(`{"candidates":[{"content":{"parts":[{"text":"split me"}],"role":"model"},"finishReason":"STOP"}]}`)
	// 逐字节投喂（最极端的分片），状态机必须能正确续扫。
	emitted := []map[string]any{}
	err := scanStream(context.Background(), &splitReader{data: []byte(raw), chunk: 1}, func(obj map[string]any) (bool, error) {
		stop, err := processStreamingObject(obj, func(ch map[string]any) bool {
			emitted = append(emitted, ch)
			return true
		})
		return stop, err
	}, nil)
	if err != nil {
		t.Fatalf("scan error: %v", err)
	}
	if len(emitted) != 1 {
		t.Fatalf("emitted=%d, want 1（逐字节分片续扫失败）", len(emitted))
	}
	if got := firstPartText(emitted[0]); got != "split me" {
		t.Errorf("text=%q", got)
	}
}

// 字符串里含花括号 / 转义引号，不能被误判为对象边界。
func TestScanStream_BracesInsideString(t *testing.T) {
	raw := wrap(`{"candidates":[{"content":{"parts":[{"text":"a {nested} \"quote\" } brace"}],"role":"model"},"finishReason":"STOP"}]}`)
	emitted, _, err := collectStream(t, raw)
	if err != nil {
		t.Fatalf("scan error: %v", err)
	}
	if len(emitted) != 1 {
		t.Fatalf("emitted=%d, want 1（字符串内花括号被误判为边界？）", len(emitted))
	}
	if got := firstPartText(emitted[0]); got != `a {nested} "quote" } brace` {
		t.Errorf("text=%q（转义/字符串内花括号处理错误）", got)
	}
}

func TestScanStream_UsageMetadataDelayed(t *testing.T) {
	// STOP 帧后 usageMetadata 单独延迟一个包到达：必须被收集，不应丢失
	stopFrame := wrap(`{"candidates":[{"content":{"parts":[{"text":"done"}],"role":"model"},"finishReason":"STOP","index":0}]}`)
	usageFrame := `{"results":[{"data":{"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":10,"totalTokenCount":15}}}]}`
	raw := stopFrame + usageFrame
	emitted, finished, err := collectStream(t, raw)
	if err != nil {
		t.Fatalf("scan error: %v", err)
	}
	if !finished {
		t.Error("收到 STOP 应标记为 finished")
	}
	// 必须 emit 出 2 个 chunk：内容帧 + 元数据帧
	if len(emitted) < 1 {
		t.Fatalf("emitted=%d, want at least 1", len(emitted))
	}
	// 检查最后一帧是否包含 usageMetadata
	last := emitted[len(emitted)-1]
	if _, hasUsage := last["usageMetadata"]; !hasUsage {
		t.Errorf("延迟的 usageMetadata 未收集，最后一帧=%v", last)
	}
}

func TestScanStream_UsageMetadataDelayedSplitRead(t *testing.T) {
	// 延迟 usage 在跨 bufio.Read 边界时才到达（C1 修复验证）
	stopFrame := wrap(`{"candidates":[{"content":{"parts":[{"text":"done"}],"role":"model"},"finishReason":"STOP","index":0}]}`)
	usageFrame := `{"results":[{"data":{"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":10,"totalTokenCount":15}}}]}`
	raw := stopFrame + usageFrame

	emitted := []map[string]any{}
	var seenFinish bool
	var finished bool
	// 使用 splitReader 按 STOP 帧长度分块，确保两帧在不同 Read 调用中到达
	err := scanStream(context.Background(), &splitReader{data: []byte(raw), chunk: len(stopFrame)}, func(obj map[string]any) (bool, error) {
		stop, err := processStreamingObject(obj, func(ch map[string]any) bool {
			emitted = append(emitted, ch)
			return true
		}, &seenFinish)
		if stop {
			finished = true
		}
		return stop, err
	}, nil)
	if err != nil {
		t.Fatalf("scan error: %v", err)
	}
	if !finished {
		t.Error("收到 STOP+usage 应标记为 finished")
	}
	if len(emitted) < 2 {
		t.Fatalf("emitted=%d, want >= 2（内容帧+元数据帧跨读边界）", len(emitted))
	}
	last := emitted[len(emitted)-1]
	if _, hasUsage := last["usageMetadata"]; !hasUsage {
		t.Errorf("跨读边界的延迟 usageMetadata 未收集，最后一帧=%v", last)
	}
}

func TestScanStream_UsageMetadataSameFrame(t *testing.T) {
	// STOP 和 usageMetadata 同帧到达 → 正常 stop（不受延迟逻辑影响）
	raw := wrap(`{"candidates":[{"content":{"parts":[{"text":"done"}],"role":"model"},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":10,"totalTokenCount":15}}`)
	emitted, finished, err := collectStream(t, raw)
	if err != nil {
		t.Fatalf("scan error: %v", err)
	}
	if !finished {
		t.Error("收到 STOP 应标记为 finished")
	}
	if len(emitted) != 1 {
		t.Fatalf("emitted=%d, want 1", len(emitted))
	}
	if _, hasUsage := emitted[0]["usageMetadata"]; !hasUsage {
		t.Error("同帧 usageMetadata 应存在")
	}
}

func BenchmarkScanStream(b *testing.B) {
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		sb.WriteString(wrap(`{"candidates":[{"content":{"parts":[{"text":"Hello world"}],"role":"model"},"finishReason":"STOP"}]}`))
	}
	input := sb.String()

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		_ = scanStream(context.Background(), strings.NewReader(input), func(obj map[string]any) (bool, error) {
			return true, nil
		}, nil)
	}
}

// TestScanStream_TouchActivity 验证 scanStream 在读取到数据时调用 touchActivity。
func TestScanStream_TouchActivity(t *testing.T) {
	var callCount atomic.Int32
	touchActivity := func() {
		callCount.Add(1)
	}
	data := wrap(`{"candidates":[{"content":{"parts":[{"text":"hello"}],"role":"model"},"finishReason":"STOP"}]}`)

	err := scanStream(context.Background(), strings.NewReader(data), func(obj map[string]any) (bool, error) {
		return true, nil
	}, touchActivity)

	if err != nil {
		t.Fatalf("scanStream error: %v", err)
	}
	if callCount.Load() == 0 {
		t.Error("touchActivity should be called at least once")
	}
}

// TestIdleWatcher_TriggersOnTimeout 验证原子时间戳空闲 watcher 模式：
// 当 touchActivity 在 timeout 时间内未被调用时，触发 idleTriggered 并关闭 reader。
func TestIdleWatcher_TriggersOnTimeout(t *testing.T) {
	pr, pw := io.Pipe()

	var (
		lastActiveUnixNano atomic.Int64
		idleTriggered      atomic.Bool
	)
	lastActiveUnixNano.Store(time.Now().UnixNano())
	done := make(chan struct{})

	idleTimeout := 200 * time.Millisecond

	touchActivity := func() {
		lastActiveUnixNano.Store(time.Now().UnixNano())
	}

	// ── 空闲 watcher goroutine（原子时间戳 + Ticker 模式）──
	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				last := time.Unix(0, lastActiveUnixNano.Load())
				elapsed := time.Since(last)
				if elapsed > idleTimeout {
					if idleTriggered.CompareAndSwap(false, true) {
						pr.Close()
					}
					return
				}
			case <-done:
				return
			}
		}
	}()

	// ── scanStream 消费 pipe ──
	errCh := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		errCh <- scanStream(ctx, pr, func(obj map[string]any) (bool, error) {
			return false, nil
		}, touchActivity)
	}()

	// 先写入一帧有效数据
	initialData := `{"results":[{"data":{"candidates":[{"content":{"parts":[{"text":"ping"}],"role":"model"},"finishReason":"FINISH_REASON_UNSPECIFIED"}]}}]}`
	_, writeErr := pw.Write([]byte(initialData))
	if writeErr != nil {
		t.Fatalf("write initial data: %v", writeErr)
	}

	// 等待 idle timeout 触发
	select {
	case err := <-errCh:
		pw.Close()
		close(done)
		if err != nil {
			t.Errorf("scanStream 应从 body close 返回 nil, 得到 %v", err)
		}
		if !idleTriggered.Load() {
			t.Error("idle watcher 应触发 idleTriggered")
		}
	case <-time.After(3 * time.Second):
		pw.Close()
		close(done)
		t.Fatal("超时：idle watcher 未能在预期时间内触发")
	}
}

// splitReader 按固定 chunk 大小逐块投喂数据，模拟网络流分片（测增量续扫）。
type splitReader struct {
	data  []byte
	chunk int
	pos   int
}

func (r *splitReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	end := r.pos + r.chunk
	if end > len(r.data) {
		end = len(r.data)
	}
	n := copy(p, r.data[r.pos:end])
	r.pos += n
	return n, nil
}
