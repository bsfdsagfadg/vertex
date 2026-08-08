package vertex

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// collectStream 把 scanStream 跑到底，收集所有 emit 出来的 chunk，返回 (chunks, 终止错误)。
// onObject 用 processStreamingObject 的真实逻辑，确保测的是端到端的流式提取 + finishReason 过滤。
func collectStream(t *testing.T, raw string) (emitted []map[string]any, stopped bool, scanErr error) {
	t.Helper()
	emit := func(ch map[string]any) bool {
		emitted = append(emitted, ch)
		return true
	}
	scanErr = scanStream(strings.NewReader(raw), func(obj map[string]any) (bool, error) {
		stop, err := processStreamingObject(obj, emit)
		if stop {
			stopped = true
		}
		return stop, err
	})
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
	err := scanStream(&splitReader{data: []byte(raw), chunk: 1}, func(obj map[string]any) (bool, error) {
		stop, err := processStreamingObject(obj, func(ch map[string]any) bool {
			emitted = append(emitted, ch)
			return true
		})
		return stop, err
	})
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

func TestScanStream_PropagatesReadErrorAfterCompleteChunk(t *testing.T) {
	wantErr := errors.New("connection reset")
	raw := wrap(`{"candidates":[{"content":{"parts":[{"text":"partial answer"}],"role":"model"},"finishReason":"FINISH_REASON_UNSPECIFIED"}]}`)
	emitted := 0
	err := scanStream(&terminalErrorReader{data: []byte(raw), err: wantErr}, func(obj map[string]any) (bool, error) {
		return processStreamingObject(obj, func(map[string]any) bool {
			emitted++
			return true
		})
	})
	if emitted != 1 {
		t.Fatalf("emitted=%d, want 1", emitted)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("异常断流应向上传播，got %v", err)
	}
}

func TestScanStream_PropagatesReadErrorWithIncompleteObject(t *testing.T) {
	wantErr := io.ErrUnexpectedEOF
	err := scanStream(&terminalErrorReader{data: []byte(`{"results":[{"data":`), err: wantErr}, func(map[string]any) (bool, error) {
		t.Fatal("不完整对象不应触发回调")
		return false, nil
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("不完整对象后的异常断流应向上传播，got %v", err)
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

// results 内的 "Failed to verify action" → AuthenticationError（触发同 token 重试）。
func TestProcessStreamingObject_VerifyFailError(t *testing.T) {
	obj := map[string]any{"results": []any{
		map[string]any{"errors": []any{map[string]any{"message": "Failed to verify action"}}},
	}}
	_, err := processStreamingObject(obj, func(map[string]any) bool { return true })
	if err == nil {
		t.Fatal("expected AuthenticationError")
	}
	if ve := asVertexError(err); ve == nil || ve.Kind != "auth" {
		t.Errorf("err=%v, want auth", err)
	}
}

// results 内真实错误（非 verify-fail）→ 结构化 VertexError。
func TestProcessStreamingObject_RealError(t *testing.T) {
	obj := map[string]any{"results": []any{
		map[string]any{"errors": []any{map[string]any{"message": "Resource exhausted", "code": float64(429)}}},
	}}
	_, err := processStreamingObject(obj, func(map[string]any) bool { return true })
	if err == nil {
		t.Fatal("expected error")
	}
	if ve := asVertexError(err); ve == nil {
		t.Errorf("err=%v, want VertexError", err)
	}
}

// _extract_chunk: 无 candidates 但有 metadata → 保留 metadata（对齐 Python：空 candidates 帧传递元数据）。
func TestExtractChunk_NoCandidates(t *testing.T) {
	chunk := extractChunk(map[string]any{"usageMetadata": map[string]any{"totalTokenCount": float64(5)}})
	if chunk == nil {
		t.Fatal("有 usageMetadata 应返回 chunk，不应为 nil")
	}
	if _, ok := chunk["usageMetadata"]; !ok {
		t.Error("usageMetadata 应保留")
	}
	if _, ok := chunk["candidates"]; ok {
		t.Error("不应有 candidates key")
	}
}

// _extract_chunk: candidates 为空列表 → 保留空列表（对齐 Python）。
func TestExtractChunk_EmptyCandidatesList(t *testing.T) {
	chunk := extractChunk(map[string]any{"candidates": []any{}})
	if chunk == nil {
		t.Fatal("空 candidates 列表应返回 chunk，不应为 nil")
	}
	cands, ok := chunk["candidates"].([]any)
	if !ok || len(cands) != 0 {
		t.Errorf("candidates=%v, want empty list", chunk["candidates"])
	}
}

// _extract_chunk: 完全空帧 → nil。
func TestExtractChunk_CompletelyEmpty(t *testing.T) {
	if chunk := extractChunk(map[string]any{}); chunk != nil {
		t.Errorf("空帧应返回 nil, got %v", chunk)
	}
}

func TestIsValidContentChunkRejectsDefaultBlockReasonAndEmptyStop(t *testing.T) {
	chunk := map[string]any{
		"candidates": []any{map[string]any{
			"finishReason": "STOP",
			"content": map[string]any{
				"role":  "model",
				"parts": []any{map[string]any{"text": ""}},
			},
		}},
		"promptFeedback": map[string]any{"blockReason": blockReasonUnspecified},
	}
	if isValidContentChunk(chunk) {
		t.Fatal("empty STOP frame with the default block reason must not count as content")
	}
}

func TestIsValidContentChunkKeepsRealSafetyBlock(t *testing.T) {
	if !isValidContentChunk(map[string]any{"promptFeedback": map[string]any{"blockReason": "SAFETY"}}) {
		t.Fatal("real safety block must be emitted")
	}
}

// _extract_chunk 附带元数据：usageMetadata/modelVersion 等非空时带上。
func TestExtractChunk_AttachesMetadata(t *testing.T) {
	data := map[string]any{
		"candidates":    []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": "hi"}}}}},
		"usageMetadata": map[string]any{"totalTokenCount": float64(3)},
		"modelVersion":  "gemini-3.1-flash",
	}
	chunk := extractChunk(data)
	if chunk == nil {
		t.Fatal("chunk 不应为 nil")
	}
	if _, ok := chunk["usageMetadata"]; !ok {
		t.Error("usageMetadata 未附带")
	}
	if chunk["modelVersion"] != "gemini-3.1-flash" {
		t.Errorf("modelVersion=%v", chunk["modelVersion"])
	}
}

// _clean_parts: 畸形嵌套 text（list/dict）递归展开为纯字符串。
func TestCleanStreamParts_MalformedNestedText(t *testing.T) {
	parts := []any{
		map[string]any{"text": []any{map[string]any{"text": "nested"}, map[string]any{"text": " text"}}},
	}
	cleaned := cleanStreamParts(parts)
	if len(cleaned) != 1 {
		t.Fatalf("cleaned len=%d, want 1", len(cleaned))
	}
	p := cleaned[0].(map[string]any)
	if p["text"] != "nested text" {
		t.Errorf("text=%q, want 'nested text'", p["text"])
	}
}

// 正常字符串 text 原样保留。
func TestCleanStreamParts_NormalText(t *testing.T) {
	parts := []any{map[string]any{"text": "plain"}}
	cleaned := cleanStreamParts(parts)
	if len(cleaned) != 1 || cleaned[0].(map[string]any)["text"] != "plain" {
		t.Errorf("normal text 被改动: %v", cleaned)
	}
}

func TestCleanPart_EmptyDefaults(t *testing.T) {
	part := map[string]any{
		"data":             "text",
		"fileData":         map[string]any{},
		"functionCall":     map[string]any{},
		"functionResponse": map[string]any{},
		"inlineData":       map[string]any{},
	}
	if got := cleanPart(part); got != nil {
		t.Errorf("empty defaults should return nil, got %v", got)
	}
}

func TestCleanPart_FunctionCallStringArgs(t *testing.T) {
	part := map[string]any{
		"functionCall": map[string]any{
			"name": "search",
			"args": `{"q":"hello"}`,
		},
	}
	got := cleanPart(part)
	if got == nil {
		t.Fatal("expected non-nil part")
	}
	fc, ok := got["functionCall"].(map[string]any)
	if !ok {
		t.Fatal("expected functionCall in cleaned part")
	}
	if fc["name"] != "search" {
		t.Errorf("name=%v, want search", fc["name"])
	}
	args, ok := fc["args"].(map[string]any)
	if !ok {
		t.Fatalf("args should be map after normalization, got %T", fc["args"])
	}
	if args["q"] != "hello" {
		t.Errorf("args.q=%v, want hello", args["q"])
	}
}

func TestCleanPart_FunctionResponseStringResponse(t *testing.T) {
	part := map[string]any{
		"functionResponse": map[string]any{
			"name":     "search",
			"response": "result text",
		},
	}
	got := cleanPart(part)
	if got == nil {
		t.Fatal("expected non-nil part")
	}
	fr, ok := got["functionResponse"].(map[string]any)
	if !ok {
		t.Fatal("expected functionResponse in cleaned part")
	}
	if fr["name"] != "search" {
		t.Errorf("name=%v, want search", fr["name"])
	}
	resp, ok := fr["response"].(map[string]any)
	if !ok {
		t.Fatalf("response should be map after normalization, got %T", fr["response"])
	}
	if resp["result"] != "result text" {
		t.Errorf("response.result=%v, want 'result text'", resp["result"])
	}
}

func TestCleanStreamParts_SkipsEmpty(t *testing.T) {
	parts := []any{
		map[string]any{"data": "text", "fileData": map[string]any{}, "text": "hi"},
		map[string]any{"data": "text", "fileData": map[string]any{}, "functionCall": map[string]any{}, "functionResponse": map[string]any{}},
	}
	cleaned := cleanStreamParts(parts)
	if len(cleaned) != 1 {
		t.Fatalf("cleaned len=%d, want 1 (only first part should survive)", len(cleaned))
	}
	p := cleaned[0].(map[string]any)
	if p["text"] != "hi" {
		t.Errorf("text=%q, want 'hi'", p["text"])
	}
}

// extractTextRecursive 递归提取嵌套 text，并防无限递归（depth>20 截断）。
func TestExtractTextRecursive_DepthGuard(t *testing.T) {
	// 正向：嵌套 text 能逐层递归提取到底。
	if got := extractTextRecursive(map[string]any{"text": map[string]any{"text": "deep"}}, 0); got != "deep" {
		t.Errorf("嵌套 text 提取失败：got %q，want deep", got)
	}
	// 数组：各 text 拼接。
	if got := extractTextRecursive([]any{map[string]any{"text": "a"}, map[string]any{"text": "b"}}, 0); got != "ab" {
		t.Errorf("数组 text 拼接失败：got %q，want ab", got)
	}
	// depth guard：25 层嵌套必须能返回（不无限递归/不栈溢出），完成本身即证明守护生效。
	var deep any = "x"
	for i := 0; i < 25; i++ {
		deep = map[string]any{"text": deep}
	}
	_ = extractTextRecursive(deep, 0)
}

// chunkFinishReason 正确取 candidates[0].finishReason，缺省返回空串。
func TestChunkFinishReason(t *testing.T) {
	if got := chunkFinishReason(map[string]any{"candidates": []any{map[string]any{"finishReason": "STOP"}}}); got != "STOP" {
		t.Errorf("got %q, want STOP", got)
	}
	if got := chunkFinishReason(map[string]any{"candidates": []any{}}); got != "" {
		t.Errorf("空 candidates 应返回空串, got %q", got)
	}
	if got := chunkFinishReason(map[string]any{}); got != "" {
		t.Errorf("无 candidates 应返回空串, got %q", got)
	}
}

// emitAndCheckFinish: UNSPECIFIED 不结束流；真实 finish 结束。
func TestEmitAndCheckFinish(t *testing.T) {
	noop := func(map[string]any) bool { return true }

	// UNSPECIFIED → 不 done。
	_, done := emitAndCheckFinish(map[string]any{"candidates": []any{map[string]any{"finishReason": "FINISH_REASON_UNSPECIFIED"}}}, noop)
	if done {
		t.Error("UNSPECIFIED 不应结束流（红线⑤）")
	}

	// 空 finishReason → 不 done。
	_, done = emitAndCheckFinish(map[string]any{"candidates": []any{map[string]any{}}}, noop)
	if done {
		t.Error("空 finishReason 不应结束流")
	}

	// STOP → done。
	_, done = emitAndCheckFinish(map[string]any{"candidates": []any{map[string]any{"finishReason": "STOP"}}}, noop)
	if !done {
		t.Error("STOP 应结束流")
	}

	// 客户端断开（emit 返回 false）→ stopByClient + done。
	reject := func(map[string]any) bool { return false }
	stopByClient, done := emitAndCheckFinish(map[string]any{"candidates": []any{map[string]any{"finishReason": "FINISH_REASON_UNSPECIFIED"}}}, reject)
	if !stopByClient || !done {
		t.Error("客户端断开应 stopByClient=true done=true")
	}
}

func TestStreamCompletionStateWaitsForEveryCandidateAndUsage(t *testing.T) {
	state := newStreamCompletionState()
	emit := func(map[string]any) bool { return true }

	_, done := emitAndCheckFinish(map[string]any{"candidates": []any{
		map[string]any{"index": 0, "finishReason": "STOP"},
		map[string]any{"index": 1, "finishReason": finishReasonUnspecified},
	}}, emit, state)
	if done {
		t.Fatal("stream stopped before all candidates finished and usage arrived")
	}
	_, done = emitAndCheckFinish(map[string]any{"usageMetadata": map[string]any{"totalTokenCount": float64(5)}}, emit, state)
	if done {
		t.Fatal("stream stopped while candidate 1 was unfinished")
	}
	_, done = emitAndCheckFinish(map[string]any{"candidates": []any{
		map[string]any{"index": 1, "finishReason": "MAX_TOKENS"},
	}}, emit, state)
	if !done {
		t.Fatal("stream did not stop after all candidates finished and usage was observed")
	}
}

func TestProcessStreamingObjectDoesNotStopInsideParallelCandidateList(t *testing.T) {
	state := newStreamCompletionState()
	emitted := 0
	emit := func(map[string]any) bool {
		emitted++
		return true
	}
	obj := map[string]any{"results": []any{map[string]any{"data": map[string]any{
		"usageMetadata": map[string]any{"totalTokenCount": float64(5)},
		"ui": map[string]any{"streamGenerateContentAnonymous": []any{
			map[string]any{"candidates": []any{map[string]any{"index": 0, "finishReason": "STOP"}}},
			map[string]any{"candidates": []any{map[string]any{"index": 1, "finishReason": finishReasonUnspecified}}},
		}},
	}}}}

	stop, err := processStreamingObject(obj, emit, state)
	if err != nil || stop {
		t.Fatalf("processStreamingObject stop=%v err=%v", stop, err)
	}
	if emitted != 2 {
		t.Fatalf("emitted=%d, want both parallel candidates", emitted)
	}
}

// ---- 测试小工具 ----

func firstPartText(chunk map[string]any) string {
	cands, _ := chunk["candidates"].([]any)
	if len(cands) == 0 {
		return ""
	}
	c, _ := cands[0].(map[string]any)
	content, _ := c["content"].(map[string]any)
	parts, _ := content["parts"].([]any)
	if len(parts) == 0 {
		return ""
	}
	p, _ := parts[0].(map[string]any)
	if s, ok := p["text"].(string); ok {
		return s
	}
	return ""
}

// splitReader 按固定 chunk 大小逐块投喂数据，模拟网络流分片（测增量续扫）。
type splitReader struct {
	data  []byte
	chunk int
	pos   int
}

type terminalErrorReader struct {
	data []byte
	err  error
	done bool
}

func (r *terminalErrorReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, r.err
	}
	r.done = true
	return copy(p, r.data), r.err
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
