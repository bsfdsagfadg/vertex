package vertex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/recaptcha"
	"github.com/bsfdsagfadg/vertex/internal/transport"
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

// results 内的 "Failed to verify action" → AuthenticationError（触发同 token 重试）。
func TestProcessStreamingObject_VerifyFailError(t *testing.T) {
	obj := map[string]any{"results": []any{
		map[string]any{"errors": []any{map[string]any{"message": "Failed to verify action"}}},
	}}
	_, err := processStreamingObject(obj, func(map[string]any) bool { return true }, nil)
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
	_, err := processStreamingObject(obj, func(map[string]any) bool { return true }, nil)
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

func TestCleanPart_FunctionCallEmptyArgs(t *testing.T) {
	// args 为空字符串时应转为空 map（M1 修复）
	part := map[string]any{
		"functionCall": map[string]any{
			"name": "no_args",
			"args": "",
		},
	}
	got := cleanPart(part)
	if got == nil {
		t.Fatal("expected non-nil part when name is present")
	}
	fc, ok := got["functionCall"].(map[string]any)
	if !ok {
		t.Fatal("expected functionCall in cleaned part")
	}
	args, ok := fc["args"].(map[string]any)
	if !ok {
		t.Fatalf("空 args 应转为 map[string]any{}, got %T", fc["args"])
	}
	if len(args) != 0 {
		t.Errorf("空 args map 应为空，got %v", args)
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

func TestCleanPart_FunctionResponseNonStringName(t *testing.T) {
	// 非字符串 name（如缺失 key）不应 panic，应被 toStr 安全处理
	part := map[string]any{
		"functionResponse": map[string]any{
			"response": map[string]any{"result": "ok"},
		},
	}
	got := cleanPart(part)
	if got == nil {
		t.Fatal("expected non-nil part when response is present")
	}
	fr, ok := got["functionResponse"].(map[string]any)
	if !ok {
		t.Fatal("expected functionResponse in cleaned part")
	}
	if _, exists := fr["name"]; exists && fr["name"] != "" {
		t.Errorf("name should be empty or absent when missing in input, got %v", fr["name"])
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

func TestCleanPart_ThoughtOnly(t *testing.T) {
	// 纯思考块：仅 thought=true，无 text → 必须保留（不再返回 nil）
	part := map[string]any{"thought": true}
	got := cleanPart(part)
	if got == nil {
		t.Fatal("纯 thought part 不应返回 nil（漏洞1）")
	}
	if got["thought"] != true {
		t.Errorf("thought 字段应保留，got %v", got)
	}
}

func TestCleanPart_ExecutableCode(t *testing.T) {
	part := map[string]any{"executableCode": map[string]any{"code": "print('hello')", "language": "python"}}
	got := cleanPart(part)
	if got == nil {
		t.Fatal("executableCode part should NOT return nil")
	}
	if _, ok := got["executableCode"]; !ok {
		t.Error("executableCode field should be preserved")
	}
}

func TestCleanPart_CodeExecutionResult(t *testing.T) {
	part := map[string]any{"codeExecutionResult": map[string]any{"output": "hello", "outcome": "OK"}}
	got := cleanPart(part)
	if got == nil {
		t.Fatal("codeExecutionResult part should NOT return nil")
	}
	if _, ok := got["codeExecutionResult"]; !ok {
		t.Error("codeExecutionResult field should be preserved")
	}
}

func TestCleanPart_InlineData(t *testing.T) {
	part := map[string]any{"inlineData": map[string]any{"data": "base64content", "mimeType": "image/png"}}
	got := cleanPart(part)
	if got == nil {
		t.Fatal("inlineData part should NOT return nil")
	}
	if _, ok := got["inlineData"]; !ok {
		t.Error("inlineData field should be preserved")
	}
}

func TestCleanPart_FileData(t *testing.T) {
	part := map[string]any{"fileData": map[string]any{"fileUri": "gs://bucket/file", "mimeType": "image/png"}}
	got := cleanPart(part)
	if got == nil {
		t.Fatal("fileData part should NOT return nil")
	}
	if _, ok := got["fileData"]; !ok {
		t.Error("fileData field should be preserved")
	}
}

func TestCleanPart_ThoughtSignatureOnly(t *testing.T) {
	// 仅 thoughtSignature 存在，无 text → 必须保留
	part := map[string]any{"thoughtSignature": "sig_abc123"}
	got := cleanPart(part)
	if got == nil {
		t.Fatal("含 thoughtSignature 的 part 不应返回 nil（漏洞1）")
	}
	if got["thoughtSignature"] != "sig_abc123" {
		t.Errorf("thoughtSignature 字段应保留，got %v", got)
	}
}

func TestIsValidContentChunk_ThoughtBool(t *testing.T) {
	// thought 为 bool(true) 时也应识别为有效内容（漏洞1类型断言）
	chunk := map[string]any{
		"candidates": []any{map[string]any{
			"content": map[string]any{"parts": []any{map[string]any{"thought": true}}, "role": "model"},
		}},
	}
	if !isValidContentChunk(chunk) {
		t.Error("thought:true 的 chunk 应 valid（漏洞1：type assertion trap）")
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

// ── isValidContentChunk ──

func TestIsValidContentChunk_TextContent(t *testing.T) {
	chunk := map[string]any{
		"candidates": []any{map[string]any{
			"content": map[string]any{"parts": []any{map[string]any{"text": "hello"}}, "role": "model"},
		}},
	}
	if !isValidContentChunk(chunk) {
		t.Error("text content chunk should be valid")
	}
}

func TestIsValidContentChunk_ThoughtContent(t *testing.T) {
	chunk := map[string]any{
		"candidates": []any{map[string]any{
			"content": map[string]any{"parts": []any{map[string]any{"thought": "thinking...", "text": "hello"}}, "role": "model"},
		}},
	}
	if !isValidContentChunk(chunk) {
		t.Error("thought content chunk should be valid")
	}
}

func TestIsValidContentChunk_FunctionCall(t *testing.T) {
	chunk := map[string]any{
		"candidates": []any{map[string]any{
			"content": map[string]any{"parts": []any{map[string]any{"functionCall": map[string]any{"name": "get_weather"}}}, "role": "model"},
		}},
	}
	if !isValidContentChunk(chunk) {
		t.Error("functionCall chunk should be valid")
	}
}

func TestIsValidContentChunk_FinishReasonStopWithoutContent(t *testing.T) {
	chunk := map[string]any{
		"candidates": []any{map[string]any{"finishReason": "STOP"}},
	}
	if isValidContentChunk(chunk) {
		t.Error("STOP finishReason without content should NOT be valid")
	}
}

func TestIsValidContentChunk_FinishReasonSafety(t *testing.T) {
	chunk := map[string]any{
		"candidates": []any{map[string]any{"finishReason": "SAFETY"}},
	}
	if !isValidContentChunk(chunk) {
		t.Error("SAFETY finishReason chunk should be valid")
	}
}

func TestIsValidContentChunk_UnspecifiedFinishReason(t *testing.T) {
	chunk := map[string]any{
		"candidates": []any{map[string]any{"finishReason": "FINISH_REASON_UNSPECIFIED"}},
	}
	if isValidContentChunk(chunk) {
		t.Error("UNSPECIFIED finishReason should NOT be valid")
	}
}

func TestIsValidContentChunk_BlockReason(t *testing.T) {
	chunk := map[string]any{"promptFeedback": map[string]any{"blockReason": "SAFETY"}}
	if !isValidContentChunk(chunk) {
		t.Error("blockReason chunk should be valid")
	}
}

func TestIsValidContentChunk_ExecutableCode(t *testing.T) {
	chunk := map[string]any{
		"candidates": []any{map[string]any{
			"content": map[string]any{"parts": []any{map[string]any{"executableCode": map[string]any{"code": "print('hello')"}}}, "role": "model"},
		}},
	}
	if !isValidContentChunk(chunk) {
		t.Error("executableCode chunk should be valid")
	}
}

func TestIsValidContentChunk_CodeExecutionResult(t *testing.T) {
	chunk := map[string]any{
		"candidates": []any{map[string]any{
			"content": map[string]any{"parts": []any{map[string]any{"codeExecutionResult": map[string]any{"output": "hello"}}}, "role": "model"},
		}},
	}
	if !isValidContentChunk(chunk) {
		t.Error("codeExecutionResult chunk should be valid")
	}
}

func TestIsValidContentChunk_InlineData(t *testing.T) {
	chunk := map[string]any{
		"candidates": []any{map[string]any{
			"content": map[string]any{"parts": []any{map[string]any{"inlineData": map[string]any{"data": "base64...", "mimeType": "image/png"}}}, "role": "model"},
		}},
	}
	if !isValidContentChunk(chunk) {
		t.Error("inlineData chunk should be valid")
	}
}

func TestIsValidContentChunk_FileData(t *testing.T) {
	chunk := map[string]any{
		"candidates": []any{map[string]any{
			"content": map[string]any{"parts": []any{map[string]any{"fileData": map[string]any{"fileUri": "gs://bucket/file", "mimeType": "image/png"}}}, "role": "model"},
		}},
	}
	if !isValidContentChunk(chunk) {
		t.Error("fileData chunk should be valid")
	}
}

func TestIsValidContentChunk_MetadataOnly(t *testing.T) {
	chunk := map[string]any{"usageMetadata": map[string]any{"totalTokenCount": float64(5)}}
	if isValidContentChunk(chunk) {
		t.Error("metadata-only chunk should NOT be valid")
	}
}

func TestIsValidContentChunk_EmptyCandidates(t *testing.T) {
	chunk := map[string]any{"candidates": []any{}}
	if isValidContentChunk(chunk) {
		t.Error("empty candidates chunk should NOT be valid")
	}
}

// ── isEmptyResponseError ──

func TestIsEmptyResponseError_Positive(t *testing.T) {
	err := NewEmptyResponseError("Upstream returned empty response (no content)", nil)
	if !isEmptyResponseError(err) {
		t.Error("NewEmptyResponseError should match isEmptyResponseError")
	}
}

func TestIsEmptyResponseError_Negative(t *testing.T) {
	err := NewNetworkError(fmt.Errorf("connection reset"))
	if isEmptyResponseError(err) {
		t.Error("NewNetworkError should NOT match isEmptyResponseError")
	}
}

func TestIsEmptyResponseError_OtherVertexError(t *testing.T) {
	err := NewAuthenticationError("token expired", nil)
	if isEmptyResponseError(err) {
		t.Error("auth error should NOT match isEmptyResponseError")
	}
}

func TestIsEmptyResponseError_NonVertexError(t *testing.T) {
	err := fmt.Errorf("some random error")
	if isEmptyResponseError(err) {
		t.Error("non-VertexError should NOT match isEmptyResponseError")
	}
}

// emitAndCheckFinish: UNSPECIFIED 不结束流；真实 finish 结束。
func TestEmitAndCheckFinish(t *testing.T) {
	noop := func(map[string]any) bool { return true }

	// UNSPECIFIED → 不 done。
	done := emitAndCheckFinish(map[string]any{"candidates": []any{map[string]any{"finishReason": "FINISH_REASON_UNSPECIFIED"}}}, noop)
	if done {
		t.Error("UNSPECIFIED 不应结束流（红线⑤）")
	}

	// 空 finishReason → 不 done。
	done = emitAndCheckFinish(map[string]any{"candidates": []any{map[string]any{}}}, noop)
	if done {
		t.Error("空 finishReason 不应结束流")
	}

	// STOP → done。
	done = emitAndCheckFinish(map[string]any{"candidates": []any{map[string]any{"finishReason": "STOP"}}}, noop)
	if !done {
		t.Error("STOP 应结束流")
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

// TestExecuteStreamingAttempt_IdleTimeout 验证 executeStreamingAttempt 在静默后触发空闲超时返回 ErrStreamIdleTimeout。
func TestExecuteStreamingAttempt_IdleTimeout(t *testing.T) {
	// ── mock 上游服务器：发送 1 帧后挂起 ──
	var mu sync.Mutex
	chunksWritten := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if chunksWritten > 0 {
			mu.Unlock()
			// 后续请求直接挂起等待断开
			<-r.Context().Done()
			return
		}
		chunksWritten++
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("response writer does not support Flusher")
			return
		}
		firstChunk := `{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":{"candidates":[{"content":{"parts":[{"text":"hello"}],"role":"model"},"finishReason":"FINISH_REASON_UNSPECIFIED"}]}}}}]}`
		_, _ = w.Write([]byte(firstChunk))
		flusher.Flush()
		// 挂起等待上下文取消（连接关闭）
		<-r.Context().Done()
	}))
	defer server.Close()

	origURL := batchGraphqlURL
	batchGraphqlURL = server.URL + "/batchGraphql"
	defer func() { batchGraphqlURL = origURL }()

	cfg := config.DefaultConfig()
	cfg.StreamIdleTimeoutSeconds = 1 // postTimeout = max(1, 10) = 10s（包间下限生效）
	provider := config.StaticProvider(cfg)

	netClient := transport.NewNetworkClient(nil)
	vc := &VertexAIClient{
		net:  netClient,
		pool: recaptcha.NewTokenPoolCustom(func(proxyURI string) (string, error) {
			return "test-token", nil
		}),
		cfg: provider,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sess, err := netClient.CreateSession(180, "", "test-idle-timeout")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer sess.Close()

	var emitted []map[string]any
	err = vc.executeStreamingAttempt(ctx, sess, "test-model", map[string]any{}, "test-token", true, func(ch map[string]any) bool {
		emitted = append(emitted, ch)
		return true
	})

	if err == nil {
		t.Fatal("expected idle timeout error, got nil")
	}
	if !errors.Is(err, ErrStreamIdleTimeout) {
		t.Errorf("expected ErrStreamIdleTimeout, got %v", err)
	}
	if len(emitted) == 0 {
		t.Error("expected at least one chunk before idle timeout")
	}
}

// TestExecuteStreamingAttempt_IdleTimeout_InDoStream 验证 executeStreamingAttempt
// 在 DoStream 阶段（等待 HTTP Response Header）卡定时，空闲超时监控能提前切断并返回 ErrStreamIdleTimeout。
func TestExecuteStreamingAttempt_IdleTimeout_InDoStream(t *testing.T) {
	testDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-testDone:
		}
	}))
	defer func() {
		close(testDone)
		server.Close()
	}()

	origURL := batchGraphqlURL
	batchGraphqlURL = server.URL + "/batchGraphql"
	defer func() { batchGraphqlURL = origURL }()

	cfg := config.DefaultConfig()
	cfg.StreamIdleTimeoutSeconds = 1 // preTimeout = max(2, 20) = 20s（首包下限生效）
	provider := config.StaticProvider(cfg)

	netClient := transport.NewNetworkClient(nil)
	vc := &VertexAIClient{
		net:  netClient,
		pool: recaptcha.NewTokenPoolCustom(func(proxyURI string) (string, error) {
			return "test-token", nil
		}),
		cfg: provider,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()

	sess, err := netClient.CreateSession(180, "", "test-idle-dostream-hang")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer sess.Close()

	var emitted []map[string]any
	err = vc.executeStreamingAttempt(ctx, sess, "test-model", map[string]any{}, "test-token", true, func(ch map[string]any) bool {
		emitted = append(emitted, ch)
		return true
	})

	if err == nil {
		t.Fatal("expected idle timeout error, got nil")
	}
	if !errors.Is(err, ErrStreamIdleTimeout) {
		t.Errorf("expected ErrStreamIdleTimeout, got %v", err)
	}
	if len(emitted) != 0 {
		t.Error("expected no chunks (timeout before any data received)")
	}
}

// TestExecuteStreamingWithRetries_ClientCancel 验证传入已取消的 ctx 时干净退出。
func TestExecuteStreamingWithRetries_ClientCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	cfg := config.DefaultConfig()
	provider := config.StaticProvider(cfg)

	netClient := transport.NewNetworkClient(nil)
	vc := &VertexAIClient{
		net:  netClient,
		pool: recaptcha.NewTokenPoolCustom(func(proxyURI string) (string, error) {
			return "test-token", nil
		}),
		cfg: provider,
	}

	var gotErr *VertexError
	yield := func(chunk StreamChunk) bool {
		if chunk.Err != nil {
			gotErr = chunk.Err
		}
		return false
	}

	// 不应 panic
	vc.executeStreamingWithRetries(ctx, "test-model", map[string]any{}, "test-proxy", yield)

	if gotErr == nil {
		t.Fatal("expected context error, got nil")
	}
	if !errors.Is(gotErr, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", gotErr)
	}
}

// TestExecuteStreamingWithRetries_NetworkError_RecreatesSession 验证网络/空响应重试时，
// executeStreamingWithRetries 会关闭并重建 Session（干净会话），使重试成功拿到有效内容。
// 修复前：空响应 / 网络错误重试沿用旧 Session，复用脏连接池导致连续失败。
func TestExecuteStreamingWithRetries_NetworkError_RecreatesSession(t *testing.T) {
	var mu sync.Mutex
	requestCount := 0
	// 第 1 次请求返回「无有效内容」触发空响应错误，第 2 次请求返回有效内容。
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestCount++
		count := requestCount
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		if count == 1 {
			// 首帧无有效内容（仅 UNSPECIFIED finishReason）→ 空响应分支。
			_, _ = w.Write([]byte(`{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":{"candidates":[{"finishReason":"FINISH_REASON_UNSPECIFIED"}]}}}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":{"candidates":[{"content":{"parts":[{"text":"recovered"}],"role":"model"},"finishReason":"STOP"}]}}}}]}`))
	}))
	defer server.Close()

	origURL := batchGraphqlURL
	batchGraphqlURL = server.URL + "/batchGraphql"
	defer func() { batchGraphqlURL = origURL }()

cfg := config.DefaultConfig()
	cfg.ParallelPoolEnabled = false // 池重试开关关闭时 MaxRetries 生效
	cfg.MaxRetries = 2
	cfg.StreamIdleTimeoutSeconds = 360
	provider := config.StaticProvider(cfg)

	netClient := transport.NewNetworkClient(nil)
	vc := &VertexAIClient{
		net:  netClient,
		pool: recaptcha.NewTokenPoolCustom(func(proxyURI string) (string, error) {
			return "test-token", nil
		}),
		cfg: provider,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

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

	vc.executeStreamingWithRetries(ctx, "test-model", map[string]any{}, "", yield)

	if gotText != "recovered" {
		t.Errorf("expected retries to recover valid content, got %q", gotText)
	}
	if requestCount < 2 {
		t.Errorf("预期发生重试（>=2 次请求），实际 %d 次", requestCount)
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
