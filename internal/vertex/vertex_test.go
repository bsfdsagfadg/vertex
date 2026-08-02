package vertex

import (
	"context"
	"errors"
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

func TestVertexErrorPreservesCauseAndClassifiesNetwork(t *testing.T) {
	ctxErr := NewContextError(context.DeadlineExceeded)
	if !errors.Is(ctxErr, context.DeadlineExceeded) {
		t.Fatal("context cause 应可通过 errors.Is 穿透")
	}
	if ctxErr.IsRetryable() {
		t.Fatal("调用方 context 超时不应重试")
	}

	networkCause := errors.New("connection reset")
	networkErr := NewNetworkError(networkCause)
	if !networkErr.IsRetryable() || !errors.Is(networkErr, networkCause) {
		t.Fatal("网络错误应可重试并保留底层 cause")
	}
}

func TestParseErrorResponseSafetyAndNonJSON(t *testing.T) {
	safety := parseErrorResponse(map[string]any{"promptFeedback": map[string]any{"blockReason": "SAFETY"}})
	if safety == nil || safety.Kind != "safety" || !safety.IsGlobalHardError() {
		t.Fatalf("安全拦截分类错误: %+v", safety)
	}
	nonJSON := parseErrorResponse("<html>bad gateway</html>")
	if nonJSON == nil || nonJSON.Code != 502 || nonJSON.UpstreamResponse == "" {
		t.Fatalf("非 JSON 上游错误未保留: %+v", nonJSON)
	}
}

func TestClassifyUpstreamHTTPErrorKeepsStatusForPlainText(t *testing.T) {
	err := classifyUpstreamHTTPError(400, "plain invalid argument")
	if err.Code != 400 || err.Kind != "invalid" {
		t.Fatalf("纯文本 HTTP 错误应保留状态码: %+v", err)
	}
}

func TestAuthError502(t *testing.T) {
	e := NewAuthenticationError("x")
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

func TestCollectChunksToParseResultPreservesMultipleCandidates(t *testing.T) {
	chunks := []map[string]any{
		{"candidates": []any{
			map[string]any{"index": 0, "content": map[string]any{"parts": []any{map[string]any{"text": "zero-a"}}}},
			map[string]any{"index": 1, "content": map[string]any{"parts": []any{map[string]any{"text": "one-a"}}}},
		}},
		{"candidates": []any{
			map[string]any{"index": 0, "content": map[string]any{"parts": []any{map[string]any{"text": "-zero-b"}}}, "finishReason": "STOP"},
			map[string]any{"index": 1, "content": map[string]any{"parts": []any{map[string]any{"text": "-one-b"}}}, "finishReason": "MAX_TOKENS"},
		}, "usageMetadata": map[string]any{"totalTokenCount": float64(10)}},
	}

	result := collectChunksToParseResult(chunks)
	if len(result.Candidates) != 2 {
		t.Fatalf("candidates=%#v, want 2", result.Candidates)
	}
	for index, wantText := range []string{"zero-a-zero-b", "one-a-one-b"} {
		candidate := result.Candidates[index]
		content := candidate["content"].(map[string]any)
		parts := content["parts"].([]any)
		if got := parts[0].(map[string]any)["text"]; got != wantText {
			t.Fatalf("candidate %d text=%#v, want %q", index, got, wantText)
		}
	}
	if result.Parts[0]["text"] != "zero-a-zero-b" {
		t.Fatalf("first-candidate compatibility fields=%#v", result.Parts)
	}
	if result.UsageMetadata["totalTokenCount"] != float64(10) {
		t.Fatalf("usage metadata=%#v", result.UsageMetadata)
	}

	client := &VertexAIClient{}
	response, err := client.buildCompleteResponse(result)
	if err != nil {
		t.Fatalf("buildCompleteResponse: %v", err)
	}
	if candidates := response["candidates"].([]any); len(candidates) != 2 {
		t.Fatalf("response candidates=%#v, want 2", candidates)
	}
}
