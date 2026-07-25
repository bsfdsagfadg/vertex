package vertex

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/config"
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

func TestAuthError502(t *testing.T) {
	e := NewAuthenticationError("x", nil)
	if e.Code != 502 {
		t.Errorf("auth code=%d, want 502（红线：避免网关误判禁用渠道）", e.Code)
	}
	if !e.IsRetryable() {
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

func TestBuildRequestPayload(t *testing.T) {
	cfg := config.StaticProvider(config.DefaultConfig())
	payload := map[string]any{"contents": []any{
		map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}},
	}}
	body := buildRequestPayload("gemini-3.1-flash", payload, "TOKEN123", cfg)
	if body["querySignature"] != querySignature {
		t.Error("querySignature 不匹配")
	}
	if body["operationName"] != "StreamGenerateContentAnonymous" {
		t.Error("operationName 不匹配")
	}
	vars := body["variables"].(map[string]any)
	if vars["region"] != "global" {
		t.Errorf("region=%v, want global", vars["region"])
	}
	if vars["recaptchaToken"] != "TOKEN123" {
		t.Errorf("recaptchaToken=%v", vars["recaptchaToken"])
	}
	if vars["model"] != "gemini-3.1-flash" {
		t.Errorf("model=%v", vars["model"])
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
	if !e.IsRetryable() {
		t.Error("network error should be retryable")
	}
	if !errors.Is(e, originalErr) {
		t.Error("errors.Is should penetrate to original net.Error via cause")
	}
}

func TestNewNetworkError_IsRetryable(t *testing.T) {
	e := NewNetworkError(io.EOF)
	if !e.IsRetryable() {
		t.Error("network error IsRetryable should return true")
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

func TestIsRetryableNetwork(t *testing.T) {
	e := NewNetworkError(io.EOF)
	if !e.IsRetryable() {
		t.Error("Kind==network should be retryable")
	}

	// context cancellation overrides retryability
	ctxErr := NewContextError(context.Canceled)
	if ctxErr.IsRetryable() {
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

func TestBuildCompleteResponse_Empty(t *testing.T) {
	c := &VertexAIClient{}
	// 无 parts、无 error、无 promptFeedback → EmptyResponseError
	_, err := c.buildCompleteResponse(&ParseResult{PromptFeedback: map[string]any{}})
	if err == nil {
		t.Error("空响应应返回 EmptyResponseError")
	}
	if ve := asVertexError(err); ve == nil || ve.Kind != "empty" {
		t.Errorf("err=%v, want empty", err)
	}
}
