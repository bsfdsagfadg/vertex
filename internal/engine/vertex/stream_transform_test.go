package vertex

import (
	"strings"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/engine/transform"
)

// results 内的 "Failed to verify action" → AuthenticationError（触发同 token 重试）。
func TestProcessStreamingObject_VerifyFailError(t *testing.T) {
	raw := []byte(`{"results":[{"errors":[{"message":"Failed to verify action"}]}]}`)
	_, err := processStreamingObject(raw, func(*transform.GeminiChunk) bool { return true }, nil)
	if err == nil {
		t.Fatal("expected AuthenticationError")
	}
	if ve := asVertexError(err); ve == nil || ve.Kind != "auth" {
		t.Errorf("err=%v, want auth", err)
	}
}

// results 内真实错误（非 verify-fail）→ 结构化 VertexError。
func TestProcessStreamingObject_RealError(t *testing.T) {
	raw := []byte(`{"results":[{"errors":[{"message":"Resource exhausted","code":429}]}]}`)
	_, err := processStreamingObject(raw, func(*transform.GeminiChunk) bool { return true }, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if ve := asVertexError(err); ve == nil {
		t.Errorf("err=%v, want VertexError", err)
	}
}

// 畸形完整帧（JSON 语法非法）→ 可重试协议错误，不静默跳过。
func TestProcessStreamingObject_InvalidJSONFrame(t *testing.T) {
	_, err := processStreamingObject([]byte(`{"a":}`), func(*transform.GeminiChunk) bool { return true }, nil)
	if err == nil {
		t.Fatal("expected protocol error")
	}
	if !strings.Contains(err.Error(), "protocol error") {
		t.Errorf("err should contain 'protocol error', got: %v", err)
	}
	if len(err.Error()) > 400 {
		t.Errorf("错误不应泄漏完整超长 payload, len=%d", len(err.Error()))
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

func TestIsValidContentChunk_ThoughtBool(t *testing.T) {
	// thought 为 bool(true) 时也应识别为有效内容（漏洞1类型断言）
	chunk := &transform.GeminiChunk{
		Candidates: []*transform.Candidate{{
			Content: &transform.Content{Role: "model", Parts: []transform.Part{{Thought: true}}},
		}},
	}
	if !isValidContentChunkTyped(chunk) {
		t.Error("thought:true 的 chunk 应 valid")
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

// chunkFinishReasonTyped 正确取 candidates[0].finishReason，缺省返回空串。
func TestChunkFinishReasonTyped(t *testing.T) {
	if got := chunkFinishReasonTyped(&transform.GeminiChunk{Candidates: []*transform.Candidate{{FinishReason: "STOP"}}}); got != "STOP" {
		t.Errorf("got %q, want STOP", got)
	}
	if got := chunkFinishReasonTyped(&transform.GeminiChunk{Candidates: []*transform.Candidate{}}); got != "" {
		t.Errorf("空 candidates 应返回空串, got %q", got)
	}
	if got := chunkFinishReasonTyped(&transform.GeminiChunk{}); got != "" {
		t.Errorf("无 candidates 应返回空串, got %q", got)
	}
}

// ── isValidContentChunkTyped ──

func TestIsValidContentChunk_TextContent(t *testing.T) {
	chunk := &transform.GeminiChunk{
		Candidates: []*transform.Candidate{{
			Content: &transform.Content{Role: "model", Parts: []transform.Part{{Text: "hello"}}},
		}},
	}
	if !isValidContentChunkTyped(chunk) {
		t.Error("text content chunk should be valid")
	}
}

func TestIsValidContentChunk_ThoughtContent(t *testing.T) {
	chunk := &transform.GeminiChunk{
		Candidates: []*transform.Candidate{{
			Content: &transform.Content{Role: "model", Parts: []transform.Part{{Thought: true, Text: "hello"}}},
		}},
	}
	if !isValidContentChunkTyped(chunk) {
		t.Error("thought content chunk should be valid")
	}
}

func TestIsValidContentChunk_FunctionCall(t *testing.T) {
	chunk := &transform.GeminiChunk{
		Candidates: []*transform.Candidate{{
			Content: &transform.Content{Role: "model", Parts: []transform.Part{{FunctionCall: &transform.FunctionCall{Name: "get_weather"}}}},
		}},
	}
	if !isValidContentChunkTyped(chunk) {
		t.Error("functionCall chunk should be valid")
	}
}

func TestIsValidContentChunk_FinishReasonStopWithoutContent(t *testing.T) {
	chunk := &transform.GeminiChunk{
		Candidates: []*transform.Candidate{{FinishReason: "STOP"}},
	}
	if isValidContentChunkTyped(chunk) {
		t.Error("STOP finishReason without content should NOT be valid (causes silent interruption)")
	}
}

func TestIsValidContentChunk_FinishReasonSafety(t *testing.T) {
	chunk := &transform.GeminiChunk{
		Candidates: []*transform.Candidate{{FinishReason: "SAFETY"}},
	}
	if !isValidContentChunkTyped(chunk) {
		t.Error("SAFETY finishReason chunk should be valid")
	}
}

func TestIsValidContentChunk_UnspecifiedFinishReason(t *testing.T) {
	chunk := &transform.GeminiChunk{
		Candidates: []*transform.Candidate{{FinishReason: "FINISH_REASON_UNSPECIFIED"}},
	}
	if isValidContentChunkTyped(chunk) {
		t.Error("UNSPECIFIED finishReason should NOT be valid")
	}
}

func TestIsValidContentChunk_BlockReason(t *testing.T) {
	chunk := &transform.GeminiChunk{PromptFeedback: &transform.PromptFeedback{BlockReason: "SAFETY"}}
	if !isValidContentChunkTyped(chunk) {
		t.Error("blockReason chunk should be valid")
	}
}

func TestIsValidContentChunk_BlockReasonUnspecified(t *testing.T) {
	chunk := &transform.GeminiChunk{PromptFeedback: &transform.PromptFeedback{BlockReason: "BLOCKED_REASON_UNSPECIFIED"}}
	if isValidContentChunkTyped(chunk) {
		t.Error("UNSPECIFIED blockReason should NOT be valid")
	}
}

func TestIsValidContentChunk_EmptyStopFrame(t *testing.T) {
	chunk := &transform.GeminiChunk{
		Candidates: []*transform.Candidate{{
			FinishReason: "STOP",
			Content:      &transform.Content{Role: "model", Parts: []transform.Part{{Text: ""}}},
		}},
		PromptFeedback: &transform.PromptFeedback{BlockReason: "BLOCKED_REASON_UNSPECIFIED"},
	}
	if isValidContentChunkTyped(chunk) {
		t.Error("空 STOP 帧（无真实内容）不应判为有效，否则导致节点误胜出与客户端静默中断")
	}
}

// MAX_TOKENS 无内容不应判为有效（与 STOP 同理，防止空响应误胜出）
func TestIsValidContentChunk_FinishReasonMaxTokensWithoutContent(t *testing.T) {
	chunk := &transform.GeminiChunk{
		Candidates: []*transform.Candidate{{FinishReason: "MAX_TOKENS"}},
	}
	if isValidContentChunkTyped(chunk) {
		t.Error("MAX_TOKENS finishReason without content should NOT be valid")
	}
}

// STOP + 有真实 content 应判为有效（正常流式响应末帧场景）
func TestIsValidContentChunk_StopWithContent(t *testing.T) {
	chunk := &transform.GeminiChunk{
		Candidates: []*transform.Candidate{{
			FinishReason: "STOP",
			Content:      &transform.Content{Role: "model", Parts: []transform.Part{{Text: "hello"}}},
		}},
	}
	if !isValidContentChunkTyped(chunk) {
		t.Error("STOP frame with real content should be valid")
	}
}

func TestIsValidContentChunk_ExecutableCode(t *testing.T) {
	chunk := &transform.GeminiChunk{
		Candidates: []*transform.Candidate{{
			Content: &transform.Content{Role: "model", Parts: []transform.Part{{ExecutableCode: &transform.ExecutableCode{Code: "print('hello')"}}}},
		}},
	}
	if !isValidContentChunkTyped(chunk) {
		t.Error("executableCode chunk should be valid")
	}
}

func TestIsValidContentChunk_CodeExecutionResult(t *testing.T) {
	chunk := &transform.GeminiChunk{
		Candidates: []*transform.Candidate{{
			Content: &transform.Content{Role: "model", Parts: []transform.Part{{CodeExecutionResult: &transform.CodeExecutionResult{Output: "hello"}}}},
		}},
	}
	if !isValidContentChunkTyped(chunk) {
		t.Error("codeExecutionResult chunk should be valid")
	}
}

func TestIsValidContentChunk_InlineData(t *testing.T) {
	chunk := &transform.GeminiChunk{
		Candidates: []*transform.Candidate{{
			Content: &transform.Content{Role: "model", Parts: []transform.Part{{InlineData: &transform.InlineData{Data: "base64...", MimeType: "image/png"}}}},
		}},
	}
	if !isValidContentChunkTyped(chunk) {
		t.Error("inlineData chunk should be valid")
	}
}

func TestIsValidContentChunk_FileData(t *testing.T) {
	chunk := &transform.GeminiChunk{
		Candidates: []*transform.Candidate{{
			Content: &transform.Content{Role: "model", Parts: []transform.Part{{FileData: &transform.FileData{FileURI: "gs://bucket/file", MimeType: "image/png"}}}},
		}},
	}
	if !isValidContentChunkTyped(chunk) {
		t.Error("fileData chunk should be valid")
	}
}

func TestIsValidContentChunk_MetadataOnly(t *testing.T) {
	chunk := &transform.GeminiChunk{UsageMetadata: &transform.UsageMetadata{TotalTokenCount: 5}}
	if isValidContentChunkTyped(chunk) {
		t.Error("metadata-only chunk should NOT be valid")
	}
}

func TestIsValidContentChunk_EmptyCandidates(t *testing.T) {
	chunk := &transform.GeminiChunk{Candidates: []*transform.Candidate{}}
	if isValidContentChunkTyped(chunk) {
		t.Error("empty candidates chunk should NOT be valid")
	}
}

// emitAndCheckFinish: UNSPECIFIED 不结束流；真实 finish 结束。
func TestEmitAndCheckFinish(t *testing.T) {
	noop := func(*transform.GeminiChunk) bool { return true }

	// UNSPECIFIED → 不 done。
	done := emitAndCheckFinish(&transform.GeminiChunk{Candidates: []*transform.Candidate{{FinishReason: "FINISH_REASON_UNSPECIFIED"}}}, noop)
	if done {
		t.Error("UNSPECIFIED 不应结束流（红线⑤）")
	}

	// 空 finishReason → 不 done。
	done = emitAndCheckFinish(&transform.GeminiChunk{Candidates: []*transform.Candidate{{}}}, noop)
	if done {
		t.Error("空 finishReason 不应结束流")
	}

	// STOP → done。
	done = emitAndCheckFinish(&transform.GeminiChunk{Candidates: []*transform.Candidate{{FinishReason: "STOP"}}}, noop)
	if !done {
		t.Error("STOP 应结束流")
	}
}

// ── Task A：typed 直解链单元测试 ──
