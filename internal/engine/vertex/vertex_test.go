package vertex

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/engine/transform"
	"github.com/bsfdsagfadg/vertex/internal/infra/config"
)

func TestParseErrorResponse(t *testing.T) {
	e := parseErrorResponse(map[string]any{"error": map[string]any{
		"code": float64(404), "message": "not found", "status": "NOT_FOUND",
	}})
	if e == nil || e.Kind != "notfound" {
		t.Errorf("got %v", e)
	}
	// GraphQL errors 数组
	e2 := parseErrorResponse(map[string]any{"errors": []any{
		map[string]any{"message": "boom", "code": float64(500)},
	}})
	if e2 == nil {
		t.Error("errors 数组未解析")
	}
}

// TestParseErrorResponse_HTMLAndNonJSONResponse 验证上游返回 HTML / 纯文本（如 Cloudflare 502/504
// 网关页）时的兜底解析：不能静默返回 nil 导致下游误判为 EmptyResponseError，而应产出结构化的
// *VertexError，且 UpstreamResponse 保留原始响应体。
func TestParseErrorResponse_HTMLAndNonJSONResponse(t *testing.T) {
	html := `<html><head><title>502 Bad Gateway</title></head><body>502 Bad Gateway</body></html>`
	ve := parseErrorResponse(html)
	if ve == nil {
		t.Fatal("HTML 上游响应应解析出 *VertexError，而不是 nil")
	}
	if ve.Code != 502 {
		t.Errorf("Code=%d, want 502", ve.Code)
	}
	if ve.Kind != "network" && ve.Kind != "server" {
		t.Errorf("Kind=%s, want network/server", ve.Kind)
	}
	if !strings.Contains(ve.UpstreamResponse, "502 Bad Gateway") {
		t.Errorf("UpstreamResponse 应包含原始 HTML, got %q", ve.UpstreamResponse)
	}

	// 纯文本（非 JSON）也应兜底解析。
	text := "502 Bad Gateway (cloudflare)"
	tv := parseErrorResponse(text)
	if tv == nil {
		t.Fatal("纯文本上游错误也应解析出 *VertexError")
	}
	if tv.Code != 502 {
		t.Errorf("纯文本 Code=%d, want 502", tv.Code)
	}
}

func TestAuthError502(t *testing.T) {
	e := NewAuthenticationError("x", nil)
	if e.Code != 502 {
		t.Errorf("auth code=%d, want 502（红线：避免网关误判禁用渠道）", e.Code)
	}
	if e.ClassifyBatch() != Transient {
		t.Error("auth 应可重试")
	}
}

func TestRaiseForStatus(t *testing.T) {
	if raiseForStatus(429, "", "x", nil, "").Kind != "ratelimit" {
		t.Error("429 → ratelimit")
	}
	if raiseForStatus(401, "", "x", nil, "").Code != 502 {
		t.Error("401 → auth(502)")
	}
	if raiseForStatus(400, "", "x", nil, "").Kind != "invalid" {
		t.Error("400 → invalid")
	}
}

func TestBuildTypedRequestPayload(t *testing.T) {
	cfg := config.StaticProvider(config.DefaultConfig())
	req := &transform.GeminiRequest{
		Contents: []transform.Content{
			{Role: "user", Parts: []transform.Part{{Text: "hi"}}},
		},
	}
	body := buildTypedRequestPayload("gemini-3.1-flash", req, "TOKEN123", cfg)
	if body.QuerySignature != querySignature {
		t.Error("querySignature 不匹配")
	}
	if body.OperationName != "StreamGenerateContentAnonymous" {
		t.Error("operationName 不匹配")
	}
	vars, ok := body.Variables.(*transform.GeminiVariables)
	if !ok || vars == nil {
		t.Fatalf("Variables type mismatch: %T", body.Variables)
	}
	if vars.Region != "global" {
		t.Errorf("region=%v, want global", vars.Region)
	}
	if vars.RecaptchaToken != "TOKEN123" {
		t.Errorf("recaptchaToken=%v", vars.RecaptchaToken)
	}
	if vars.Model != "gemini-3.1-flash" {
		t.Errorf("model=%v", vars.Model)
	}
}

func TestNewNetworkError(t *testing.T) {
	originalErr := &net.DNSError{Err: "host not found", Name: "example.com", IsTimeout: false}
	e := NewNetworkError(originalErr)
	if e.Code != 502 {
		t.Errorf("Code=%d, want 502", e.Code)
	}
	if e.Kind != "network" {
		t.Errorf("Kind=%s, want network", e.Kind)
	}
	if e.ClassifyBatch() != Transient {
		t.Error("network error should be retryable")
	}
	if !errors.Is(e, originalErr) {
		t.Error("errors.Is should penetrate to original net.Error via cause")
	}
}

func TestNewSafetyError(t *testing.T) {
	e := NewSafetyError("Blocked by safety", "SAFETY", nil)
	if e.Code != 400 {
		t.Errorf("Code=%d, want 400", e.Code)
	}
	if e.Kind != "safety" {
		t.Errorf("Kind=%s, want safety", e.Kind)
	}
	if e.Status != "SAFETY" {
		t.Errorf("Status=%s, want SAFETY", e.Status)
	}
}

func TestWithCause(t *testing.T) {
	original := errors.New("root cause")
	e := NewInternalError("wrapper", nil).WithCause(original)
	if !errors.Is(e, original) {
		t.Error("WithCause should allow errors.Is to penetrate to original cause")
	}
}

func TestNetworkErrorTransientClassification(t *testing.T) {
	e := NewNetworkError(io.EOF)
	if e.ClassifyBatch() != Transient {
		t.Error("Kind==network should be retryable")
	}

	// context cancellation overrides retryability
	ctxErr := NewContextError(context.Canceled)
	if ctxErr.ClassifyBatch() == Transient {
		t.Error("context.Canceled should not be retryable")
	}
}

func TestIsGlobalHardError(t *testing.T) {
	tests := []struct {
		name string
		err  *VertexError
		want bool
	}{
		// 全局硬错误：应返回 true
		{"invalid 400", NewInvalidArgumentError("bad request", nil), true},
		{"notfound 404", NewNotFoundError("model not found", nil), true},
		{"safety", NewSafetyError("Blocked", "SAFETY", nil), true},
		// 节点级错误：应返回 false
		{"permission 403", NewPermissionDeniedError("forbidden", nil), false},
		{"auth 502", NewAuthenticationError("auth failed", nil), false},
		{"network", NewNetworkError(io.EOF), false},
		{"ratelimit 429", NewRateLimitError("too many", 0, nil), false},
		{"internal 500", NewInternalError("server error", nil), false},
		{"unavailable 503", NewUnavailableError("unavailable", nil), false},
		{"empty 502", NewEmptyResponseError("empty", nil), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.IsGlobalHardError(); got != tt.want {
				t.Errorf("IsGlobalHardError() = %v, want %v (Kind=%s)", got, tt.want, tt.err.Kind)
			}
		})
	}
}

func TestParseErrorResponseSafety(t *testing.T) {
	// Nested error with SAFETY message
	e := parseErrorResponse(map[string]any{
		"error": map[string]any{
			"code":    float64(400),
			"message": "SAFETY",
			"status":  "INVALID_ARGUMENT",
		},
	})
	if e == nil || e.Kind != "safety" {
		t.Errorf("expected safety error, got %v", e)
	}

	// Flat format with finishReason=SAFETY
	e2 := parseErrorResponse(map[string]any{
		"finishReason": "SAFETY",
		"message":      "Blocked",
	})
	if e2 == nil || e2.Kind != "safety" {
		t.Errorf("expected safety error from finishReason, got %v", e2)
	}

	// Flat format with promptFeedback.blockReason
	e3 := parseErrorResponse(map[string]any{
		"promptFeedback": map[string]any{
			"blockReason":        "SAFETY",
			"blockReasonMessage": "Content blocked",
		},
	})
	if e3 == nil || e3.Kind != "safety" {
		t.Errorf("expected safety error from promptFeedback, got %v", e3)
	}

	// Plain text containing "safety" should NOT produce safety error
	e4 := parseErrorResponse(map[string]any{
		"code":    float64(400),
		"message": "This is a safety test message",
		"status":  "INVALID_ARGUMENT",
	})
	if e4 != nil && e4.Kind == "safety" {
		t.Error("plain message containing 'safety' should not match as safety error")
	}
}

func TestClassifyNetworkError(t *testing.T) {
	// Already a VertexError → passthrough
	orig := NewAuthenticationError("test", nil)
	got := classifyNetworkError(orig)
	if got != orig {
		t.Error("classifyNetworkError should return same pointer for existing VertexError")
	}

	// context canceled → NewContextError
	ctxErr := classifyNetworkError(context.Canceled)
	if ctxErr == nil || ctxErr.Kind != "internal" {
		t.Error("context.Canceled should become internal Kind")
	}

	// net.Error timeout → network error
	timeoutErr := &net.DNSError{Err: "timeout", Name: "test", IsTimeout: true}
	netErr := classifyNetworkError(timeoutErr)
	if netErr == nil || netErr.Kind != "network" {
		t.Errorf("net.Error should become network error, got Kind=%s", netErr.Kind)
	}

	// plain error → internal with cause
	plainErr := errors.New("something went wrong")
	internalErr := classifyNetworkError(plainErr)
	if internalErr == nil || internalErr.Kind != "internal" {
		t.Errorf("plain error should become internal, got Kind=%s", internalErr.Kind)
	}
	if !errors.Is(internalErr, plainErr) {
		t.Error("classifyNetworkError should preserve cause for plain errors")
	}
}

func TestBuildCompleteResponseTyped_Empty(t *testing.T) {
	c := &VertexAIClient{}
	// 无 candidates、无 promptFeedback → EmptyResponseError
	_, err := c.buildCompleteResponseTyped(&ParseResultTyped{})
	if err == nil {
		t.Error("空响应应返回 EmptyResponseError")
	}
	if ve := asVertexError(err); ve == nil || ve.Kind != "network" {
		t.Errorf("err=%v, want empty", err)
	}
}

func TestBuildCompleteResponseTyped_MultiCandidate(t *testing.T) {
	c := &VertexAIClient{}
	// 构造包含两个候选（index 0 和 1）的 ParseResultTyped
	r := &ParseResultTyped{
		Candidates: []*transform.Candidate{
			{
				Index:        0,
				Content:      &transform.Content{Role: "model", Parts: []transform.Part{{Text: "hello from 0"}}},
				FinishReason: "STOP",
			},
			{
				Index:        1,
				Content:      &transform.Content{Role: "model", Parts: []transform.Part{{Text: "hello from 1"}}},
				FinishReason: "STOP",
			},
		},
	}
	resp, err := c.buildCompleteResponseTyped(r)
	if err != nil {
		t.Fatalf("buildCompleteResponseTyped error: %v", err)
	}
	if len(resp.Candidates) != 2 {
		t.Fatalf("len(candidates)=%d, want 2", len(resp.Candidates))
	}
	// 验证第一个候选
	c0 := resp.Candidates[0]
	if c0.Index != 0 {
		t.Errorf("candidate[0].index=%d, want 0", c0.Index)
	}
	if len(c0.Content.Parts) != 1 || c0.Content.Parts[0].Text != "hello from 0" {
		t.Errorf("candidate[0] parts mismatch: %+v", c0.Content.Parts)
	}
	// 验证第二个候选
	c1 := resp.Candidates[1]
	if c1.Index != 1 {
		t.Errorf("candidate[1].index=%d, want 1", c1.Index)
	}
	if c1.Content == nil || c1.Content.Parts[0].Text != "hello from 1" {
		t.Errorf("candidate[1] parts mismatch: %+v", c1.Content)
	}
}

func TestCollectChunksToParseResultTyped_MultiCandidate(t *testing.T) {
	chunks := []*transform.GeminiChunk{
		{
			Candidates: []*transform.Candidate{
				{
					Index:        0,
					Content:      &transform.Content{Role: "model", Parts: []transform.Part{{Text: "part0-a"}}},
					FinishReason: "FINISH_REASON_UNSPECIFIED",
				},
				{
					Index:        1,
					Content:      &transform.Content{Role: "model", Parts: []transform.Part{{Text: "part1-a"}}},
					FinishReason: "FINISH_REASON_UNSPECIFIED",
				},
			},
		},
		{
			Candidates: []*transform.Candidate{
				{
					Index:        0,
					Content:      &transform.Content{Role: "model", Parts: []transform.Part{{Text: " part0-b"}}},
					FinishReason: "STOP",
				},
				{
					Index:        1,
					Content:      &transform.Content{Role: "model", Parts: []transform.Part{{Text: " part1-b"}}},
					FinishReason: "STOP",
				},
			},
			UsageMetadata: &transform.UsageMetadata{TotalTokenCount: 10},
		},
	}

	result := collectChunksToParseResultTyped(chunks)
	if result == nil {
		t.Fatal("collectChunksToParseResultTyped returned nil")
	}

	// 验证 UsageMetadata 传播
	if result.UsageMetadata == nil || result.UsageMetadata.TotalTokenCount != 10 {
		t.Error("usageMetadata should be propagated")
	}

	// 验证完整 Candidates（按 index 升序）
	if len(result.Candidates) != 2 {
		t.Fatalf("result.Candidates len=%d, want 2", len(result.Candidates))
	}
	// index=0 候选：同角色文本跨帧合并
	c0 := result.Candidates[0]
	if c0.Index != 0 {
		t.Errorf("candidates[0].index=%d, want 0", c0.Index)
	}
	if len(c0.Content.Parts) != 1 || c0.Content.Parts[0].Text != "part0-a part0-b" {
		t.Errorf("candidates[0] text=%q, want 'part0-a part0-b'", c0.Content.Parts[0].Text)
	}
	if c0.FinishReason != "STOP" {
		t.Errorf("candidates[0].finishReason=%q, want STOP", c0.FinishReason)
	}
	// index=1 候选
	c1 := result.Candidates[1]
	if c1.Index != 1 {
		t.Errorf("candidates[1].index=%d, want 1", c1.Index)
	}
	if len(c1.Content.Parts) != 1 || c1.Content.Parts[0].Text != "part1-a part1-b" {
		t.Errorf("candidates[1] text=%q, want 'part1-a part1-b'", c1.Content.Parts[0].Text)
	}
	if c1.FinishReason != "STOP" {
		t.Errorf("candidates[1].finishReason=%q, want STOP", c1.FinishReason)
	}
}

func TestCollectChunksToParseResultTyped_SingleCandidate(t *testing.T) {
	// 确保单候选场景行为不变
	chunks := []*transform.GeminiChunk{
		{
			Candidates: []*transform.Candidate{
				{
					Index:   0,
					Content: &transform.Content{Role: "model", Parts: []transform.Part{{Text: "hello"}}},
				},
			},
		},
	}
	result := collectChunksToParseResultTyped(chunks)
	if result == nil {
		t.Fatal("collectChunksToParseResultTyped returned nil")
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("Candidates len=%d, want 1", len(result.Candidates))
	}
	if result.Candidates[0].Index != 0 {
		t.Errorf("candidates[0].index=%d, want 0", result.Candidates[0].Index)
	}
	if len(result.Candidates[0].Content.Parts) != 1 || result.Candidates[0].Content.Parts[0].Text != "hello" {
		t.Errorf("single candidate parts mismatch: %+v", result.Candidates[0].Content.Parts)
	}
}
