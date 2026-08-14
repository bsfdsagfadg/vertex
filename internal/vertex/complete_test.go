package vertex

import (
	"errors"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/transform"
)

// TestCompleteChat_AllCandidatesSafetyBlocked_ReturnsSafetyError 验证所有候选都被安全审查拦截
func TestCompleteChat_AllCandidatesSafetyBlocked_ReturnsSafetyError(t *testing.T) {
	safetyResult := func() *transform.GeminiResponse {
		return &transform.GeminiResponse{
			Candidates: []*transform.Candidate{
				{FinishReason: "SAFETY"},
			},
		}
	}

	results := []candidateResult{
		{proxyURI: "uri1", resp: safetyResult()},
		{proxyURI: "uri2", resp: safetyResult()},
	}

	_, err := pickBestResult(results, &transform.TextStrategy{})
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

// TestPickBestResult_MixedSafetyAndViable 验证混合场景：部分候选安全拦截、部分含有效内容时，返回首个有效内容。
func TestPickBestResult_MixedSafetyAndViable(t *testing.T) {
	viable := &transform.GeminiResponse{
		Candidates: []*transform.Candidate{
			{
				FinishReason: "STOP",
				Content:      &transform.Content{Role: "model", Parts: []transform.Part{{Text: "ok"}}},
			},
		},
	}
	results := []candidateResult{
		{proxyURI: "uri1", resp: &transform.GeminiResponse{Candidates: []*transform.Candidate{{FinishReason: "SAFETY"}}}},
		{proxyURI: "uri2", resp: viable},
	}

	resp, err := pickBestResult(results, &transform.TextStrategy{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if candidateFinishTyped(resp) != "STOP" {
		t.Errorf("expected STOP viable result to win, got %v", resp)
	}
}

// TestPickBestResult_AllNoViableNonSafety 验证全无有效内容且无 SAFETY 时返回 502 空响应错误。
func TestPickBestResult_AllNoViableNonSafety(t *testing.T) {
	empty := &transform.GeminiResponse{Candidates: []*transform.Candidate{}}
	results := []candidateResult{
		{resp: &transform.GeminiResponse{}},
		{resp: empty},
	}
	_, err := pickBestResult(results, &transform.TextStrategy{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var ve *VertexError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *VertexError, got %T", err)
	}
	if ve.Kind == "safety" {
		t.Errorf("无 SAFETY 时不应误报 safety, got %v", ve)
	}
	if ve.Code != 502 {
		t.Errorf("expected Code=502 for empty candidates, got %d", ve.Code)
	}
}

// TestPickBestResult_StopWithoutContent_TreatedAsInvalid 验证虽有 STOP 但无 content/parts 的空包不被选为有效结果。
func TestPickBestResult_StopWithoutContent_TreatedAsInvalid(t *testing.T) {
	emptyStop := &transform.GeminiResponse{
		Candidates: []*transform.Candidate{
			{FinishReason: "STOP", Content: &transform.Content{Parts: []transform.Part{}}},
		},
	}
	validStop := &transform.GeminiResponse{
		Candidates: []*transform.Candidate{
			{FinishReason: "STOP", Content: &transform.Content{Parts: []transform.Part{{Text: "real answer"}}}},
		},
	}
	results := []candidateResult{
		{proxyURI: "uri1", resp: emptyStop},
		{proxyURI: "uri2", resp: validStop},
	}

	resp, err := pickBestResult(results, &transform.TextStrategy{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Candidates[0].Content.Parts) == 0 {
		t.Fatal("emptyStop should not have won")
	}
	if resp.Candidates[0].Content.Parts[0].Text != "real answer" {
		t.Errorf("expected validStop content, got %v", resp)
	}
}
