package vertex

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/engine/recaptcha"
	"github.com/bsfdsagfadg/vertex/internal/engine/transform"
	"github.com/bsfdsagfadg/vertex/internal/infra/config"
	"github.com/bsfdsagfadg/vertex/internal/infra/transport"
)

// TestCompleteChat_AuthVerifyFail_RetrySucceeds 验证非流式路径同样具备 rT 第一发补偿：
// 首轮 "Failed to verify action" → 同 token 500ms 后第二发成功，GetTokenShared 仅 1 次。
func TestCompleteChat_AuthVerifyFail_RetrySucceeds(t *testing.T) {
	testNodes.Reset()
	defer testNodes.Reset()

	var requestCount int
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestCount++
		cur := requestCount
		mu.Unlock()
		if cur == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":{"message":"Failed to verify action"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}
		contentFrame := `{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":{"candidates":[{"content":{"parts":[{"text":"hello"}],"role":"model"},"finishReason":"FINISH_REASON_STOP"}]}}}}]}`
		_, _ = w.Write([]byte(contentFrame))
		flusher.Flush()
	}))
	defer server.Close()

	cfg := config.DefaultConfig()
	provider := config.StaticProvider(cfg)

	tokenFetchCount := 0
	var tokenMu sync.Mutex
	netClient := transport.NewNetworkClient(nil)
	vc := &VertexAIClient{
		net: netClient,
		pool: recaptcha.NewTokenPoolCustom(func(proxyURI string) (string, error) {
			tokenMu.Lock()
			tokenFetchCount++
			tokenMu.Unlock()
			return "test-token", nil
		}),
		cfg:      provider,
		batchURL: server.URL + "/batchGraphql",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := vc.CompleteChat(ctx, "test-model", &transform.GeminiRequest{}, &transform.TextStrategy{})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil ||
		len(resp.Candidates[0].Content.Parts) == 0 || resp.Candidates[0].Content.Parts[0].Text != "hello" {
		t.Fatalf("expected content 'hello', got %+v", resp)
	}
	if requestCount != 2 {
		t.Errorf("expected 2 upstream requests (1 fail + 1 retry), got %d", requestCount)
	}
	tokenMu.Lock()
	fetches := tokenFetchCount
	tokenMu.Unlock()
	if fetches != 1 {
		t.Errorf("expected single token fetch (same-token retry), got %d", fetches)
	}
}

// TestCompleteChat_AuthVerifyFail_RetryExhausted 验证非流式补偿耗尽路径：
// 首轮与同 token 重试均被拒时，如实透传 auth 错误。
func TestCompleteChat_AuthVerifyFail_RetryExhausted(t *testing.T) {
	testNodes.Reset()
	defer testNodes.Reset()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":{"message":"Failed to verify action"}}`))
	}))
	defer server.Close()

	cfg := config.DefaultConfig()
	provider := config.StaticProvider(cfg)

	netClient := transport.NewNetworkClient(nil)
	vc := &VertexAIClient{
		net: netClient,
		pool: recaptcha.NewTokenPoolCustom(func(proxyURI string) (string, error) {
			return "test-token", nil
		}),
		cfg:      provider,
		batchURL: server.URL + "/batchGraphql",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := vc.CompleteChat(ctx, "test-model", &transform.GeminiRequest{}, &transform.TextStrategy{})
	if err == nil {
		t.Fatal("expected auth error, got nil")
	}
	var ve *VertexError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *VertexError, got %T", err)
	}
	if ve.Kind != "auth" {
		t.Errorf("expected auth kind, got %q: %v", ve.Kind, err)
	}
	if !isAuthVerifyFail(ve) {
		t.Errorf("expected verify-fail auth error, got %v", err)
	}
}

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

// TestPickBestResult_SafetyFinishReasonNormalized 验证安全 finishReason 兜底原因经大小写
// 归一（与 blockReason 分支及 errors.go 各构造路径一致），非规范小写输入不退回默认 SAFETY。
func TestPickBestResult_SafetyFinishReasonNormalized(t *testing.T) {
	results := []candidateResult{
		{resp: &transform.GeminiResponse{
			Candidates: []*transform.Candidate{{FinishReason: "recitation"}},
		}},
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
	if ve.Status != "RECITATION" {
		t.Errorf("Status=%q, want 归一化后的 RECITATION", ve.Status)
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
