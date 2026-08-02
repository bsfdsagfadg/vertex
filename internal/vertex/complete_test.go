package vertex

import (
	"errors"
	"testing"
)

// TestCompleteChat_AllCandidatesSafetyBlocked_ReturnsSafetyError 验证所有候选都被安全审查
// 拦截（finishReason=SAFETY 且无有效内容）时，pickBestResult 返回 *VertexError.Kind=="safety"，
// 而非退化为 500 内部错误。修复前：无有效响应直接返回 NewInternalError，被网关误判为服务内部错误。
func TestCompleteChat_AllCandidatesSafetyBlocked_ReturnsSafetyError(t *testing.T) {
	safetyResult := func() map[string]any {
		return map[string]any{"candidates": []any{map[string]any{"finishReason": "SAFETY"}}}
	}

	results := []candidateResult{
		{proxyURI: "uri1", resp: safetyResult()},
		{proxyURI: "uri2", resp: safetyResult()},
	}

	_, err := pickBestResult(results)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var ve *VertexError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *VertexError, got %T", err)
	}
	if ve.Kind != "safety" {
		t.Errorf("expected Kind=safety, got %q", ve.Kind)
	}
	if !ve.IsGlobalHardError() {
		t.Error("safety 应为全局硬错误")
	}
}

// TestPickBestResult_MixedSafetyAndViable 验证混合场景：部分候选安全拦截、部分含有效内容时，
// 返回首个有效内容，不产生 safety 错误。
func TestPickBestResult_MixedSafetyAndViable(t *testing.T) {
	viable := map[string]any{"candidates": []any{
		map[string]any{
			"finishReason": "STOP",
			"content":      map[string]any{"parts": []any{map[string]any{"text": "ok"}}, "role": "model"},
		},
	}}
	results := []candidateResult{
		{proxyURI: "uri1", resp: map[string]any{"candidates": []any{map[string]any{"finishReason": "SAFETY"}}}},
		{proxyURI: "uri2", resp: viable},
	}

	resp, err := pickBestResult(results)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if candidateFinish(resp) != "STOP" {
		t.Errorf("expected STOP viable result to win, got %v", resp)
	}
}

// TestPickBestResult_AllNoViableNonSafety 验证全无有效内容且无 SAFETY 时仍保持
// 内部错误语义（不误报 safety）。
func TestPickBestResult_AllNoViableNonSafety(t *testing.T) {
	empty := map[string]any{"candidates": []any{}}
	results := []candidateResult{
		{resp: map[string]any{}},
		{resp: empty},
	}
	_, err := pickBestResult(results)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var ve *VertexError
	if errors.As(err, &ve) && ve.Kind == "safety" {
		t.Errorf("无 SAFETY 时不应误报 safety, got %v", ve)
	}
}