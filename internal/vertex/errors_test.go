package vertex

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// ── ClassifyBatch 四态裁决 ──

func TestClassifyBatch_ContextCause_IsTerminal(t *testing.T) {
	err := NewContextError(fmt.Errorf("upstream request: %w", context.Canceled))
	if got := err.ClassifyBatch(); got != Terminal {
		t.Errorf("context.Canceled 应裁决为 Terminal，实际 %v", got)
	}
	if err.IsRetryable() {
		t.Error("context 类错误不应可重试")
	}

	err = NewContextError(fmt.Errorf("upstream request: %w", context.DeadlineExceeded))
	if got := err.ClassifyBatch(); got != Terminal {
		t.Errorf("context.DeadlineExceeded 应裁决为 Terminal，实际 %v", got)
	}
}

func TestClassifyBatch_Truncated_IsCommitted(t *testing.T) {
	err := NewNetworkError(ErrStreamIdleTimeout).WithTruncated()
	if got := err.ClassifyBatch(); got != Committed {
		t.Errorf("Truncated 应裁决为 Committed，实际 %v", got)
	}
	if err.IsRetryable() {
		t.Error("Committed 错误不应可重试")
	}
	if !err.Truncated {
		t.Error("WithTruncated 应置位 Truncated")
	}
}

func TestClassifyBatch_GlobalHardError_IsFailFast(t *testing.T) {
	cases := []struct {
		name string
		err  *VertexError
	}{
		{"invalid", NewInvalidArgumentError("bad request", nil)},
		{"notfound", NewNotFoundError("model not found", nil)},
		{"safety", NewSafetyError("blocked", "SAFETY", nil)},
		{"infra", NewRecaptchaUnavailableError("rT exhausted", nil)},
	}
	for _, c := range cases {
		if got := c.err.ClassifyBatch(); got != FailFast {
			t.Errorf("%s 应裁决为 FailFast，实际 %v", c.name, got)
		}
		if !c.err.IsGlobalHardError() {
			t.Errorf("%s 应命中 IsGlobalHardError", c.name)
		}
		if c.err.IsRetryable() {
			t.Errorf("%s 不应可重试", c.name)
		}
	}
}

func TestClassifyBatch_Permission_IsTerminal(t *testing.T) {
	err := NewPermissionDeniedError("forbidden", nil)
	if got := err.ClassifyBatch(); got != Terminal {
		t.Errorf("permission 应裁决为 Terminal，实际 %v", got)
	}
	if err.IsGlobalHardError() {
		t.Error("permission 不应命中 IsGlobalHardError")
	}
}

func TestClassifyBatch_Transient(t *testing.T) {
	cases := []struct {
		name string
		err  *VertexError
	}{
		{"network", NewNetworkError(fmt.Errorf("tcp reset"))},
		{"ratelimit", NewRateLimitError("too many", 429, nil)},
		{"auth", NewAuthenticationError("401", nil)},
		{"empty", NewEmptyResponseError("no content", nil)},
	}
	for _, c := range cases {
		if got := c.err.ClassifyBatch(); got != Transient {
			t.Errorf("%s 应裁决为 Transient，实际 %v", c.name, got)
		}
		if !c.err.IsRetryable() {
			t.Errorf("%s 应可重试", c.name)
		}
	}
}

func TestClassifyBatch_Default_IsTerminal(t *testing.T) {
	err := &VertexError{Kind: "unknown-kind", Code: 599}
	if got := err.ClassifyBatch(); got != Terminal {
		t.Errorf("未知 Kind 应裁决为 Terminal，实际 %v", got)
	}
}

// ── NormalizeError 内核兼容性 ──

func TestNormalizeError_NonVertexError_Defaults(t *testing.T) {
	err := NormalizeError(fmt.Errorf("plain error"))
	var ve *VertexError
	if !errors.As(err, &ve) {
		t.Fatalf("NormalizeError 应返回 *VertexError，实际 %T", err)
	}
	if ve.Kind != "internal" {
		t.Errorf("普通错误应归一为 internal，实际 %q", ve.Kind)
	}
	if ve.Code != 500 {
		t.Errorf("普通错误应归一为 500，实际 %d", ve.Code)
	}
	// 方案 line 64/409：internal(500) 属 Transient（CreateSession 失败等本机瞬时错误可重试）。
	if ve.ClassifyBatch() != Transient {
		t.Errorf("internal(500) 应裁决为 Transient，实际 %v", ve.ClassifyBatch())
	}
	if !ve.IsRetryable() {
		t.Error("internal(500) 应可重试")
	}
}

func TestNormalizeError_Nil(t *testing.T) {
	if err := NormalizeError(nil); err != nil {
		t.Errorf("nil 应透传 nil，实际 %v", err)
	}
}

// ── FriendlyErrorMessage 截断后缀 ──

func TestFriendlyErrorMessage_TruncatedSuffix(t *testing.T) {
	err := NewNetworkError(ErrStreamIdleTimeout).WithTruncated()
	msg := FriendlyErrorMessage(err)
	if msg == "" {
		t.Fatal("FriendlyErrorMessage 不应为空")
	}
	if !strings.Contains(msg, "（内容已截断）") {
		t.Errorf("截断错误应带后缀，实际 %q", msg)
	}

	plain := FriendlyErrorMessage(NewNetworkError(fmt.Errorf("tcp reset")))
	if strings.Contains(plain, "（内容已截断）") {
		t.Errorf("非截断错误不应带后缀，实际 %q", plain)
	}
}

// ── IsRetryable 薄别名 ──

func TestIsRetryable_Alias(t *testing.T) {
	if !NewNetworkError(fmt.Errorf("x")).IsRetryable() {
		t.Error("network 应可重试")
	}
	if NewInvalidArgumentError("x", nil).IsRetryable() {
		t.Error("invalid 不应可重试")
	}
}
