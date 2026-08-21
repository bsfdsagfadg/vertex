package vertex

import (
	"context"
	"errors"
	"fmt"
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

// ── isAuthVerifyFail（rT 时序补偿判定） ──

func TestIsAuthVerifyFail(t *testing.T) {
	cases := []struct {
		name string
		ve   *VertexError
		want bool
	}{
		{"verify action", NewAuthenticationError("Authentication/Recaptcha failed: ... Failed to verify action ...", nil), true},
		{"permission", NewAuthenticationError("The caller does not have permission", nil), true},
		{"plain auth", NewAuthenticationError("token expired", nil), false},
		{"network", NewNetworkError(fmt.Errorf("connection reset")), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAuthVerifyFail(tc.ve); got != tc.want {
				t.Errorf("isAuthVerifyFail(%v) = %v, want %v", tc.ve, got, tc.want)
			}
		})
	}
}

// TestStreamChatOp_AuthVerifyFail_RetrySucceeds 验证 rT 时序补偿机制：
// 首轮请求被上游 "Failed to verify action" 拒绝后，同一 token 静置 500ms 重试一次即成功，
// 且不重新获取 token（GetTokenShared 仅调用 1 次）。
func TestStreamChatOp_AuthVerifyFail_RetrySucceeds(t *testing.T) {
	testNodes.Reset()
	defer testNodes.Reset()

	// mock 上游：第 1 次请求返回 401 verify fail，第 2 次返回正常内容帧。
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
		contentFrame := `{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":{"candidates":[{"content":{"parts":[{"text":"hello"}],"role":"model"},"finishReason":"FINISH_REASON_UNSPECIFIED"}]}}}}]}`
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

	var gotData bool
	var gotErr *VertexError
	vc.StreamChat(ctx, "test-model", &transform.GeminiRequest{}, func(chunk StreamChunk) bool {
		if chunk.Err != nil && gotErr == nil {
			gotErr = chunk.Err
			return false
		}
		if chunk.Data != nil && firstPartText(chunk.Data) == "hello" {
			gotData = true
		}
		return true
	}, &transform.TextStrategy{})

	if gotErr != nil {
		t.Fatalf("expected success, got error: %+v", gotErr)
	}
	if !gotData {
		t.Fatal("expected content chunk after rT timing retry")
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

// TestStreamChatOp_AuthVerifyFail_RetryExhausted 验证时序补偿耗尽路径：
// 首轮与同 token 重试均被 "Failed to verify action" 拒绝时，如实透传 auth 错误（不换 token、不误报）。
func TestStreamChatOp_AuthVerifyFail_RetryExhausted(t *testing.T) {
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

	var gotErr *VertexError
	vc.StreamChat(ctx, "test-model", &transform.GeminiRequest{}, func(chunk StreamChunk) bool {
		if chunk.Err != nil && gotErr == nil {
			gotErr = chunk.Err
		}
		return true
	}, &transform.TextStrategy{})

	if gotErr == nil {
		t.Fatal("expected auth error chunk, got nil")
	}
	if gotErr.Kind != "auth" {
		t.Errorf("expected auth kind, got %q: %+v", gotErr.Kind, gotErr)
	}
	if !isAuthVerifyFail(gotErr) {
		t.Errorf("expected verify-fail auth error, got %+v", gotErr)
	}
}

// TestExecuteStreamingAttempt_MalformedFrame_NetworkError 补充方案：HTTP 流返回畸形完整帧时，
// 必须报 network 类 VertexError（而非空响应错误），按 MaxRetries=0 立即失败。
func TestExecuteStreamingAttempt_MalformedFrame_NetworkError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"a":}`))
	}))
	defer server.Close()

	cfg := config.DefaultConfig()
	cfg.MaxRetries = 0
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

	sess, err := netClient.CreateSession(180, "", "test-malformed-frame")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer sess.Close()

	err = vc.executeStreamingAttempt(ctx, sess, "test-model", &transform.GeminiRequest{}, "test-token", func(ch *transform.GeminiChunk) bool {
		return true
	}, &transform.TextStrategy{})

	if err == nil {
		t.Fatal("expected error")
	}
	if isEmptyResponseError(err) {
		t.Fatalf("畸形帧不应被误报为 empty response: %v", err)
	}
	ve := asVertexError(err)
	if ve == nil {
		t.Fatalf("expected VertexError, got %T: %v", err, err)
	}
	if ve.Kind != "network" {
		t.Errorf("expected network kind, got %q: %v", ve.Kind, err)
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

	cfg := config.DefaultConfig()
	cfg.StreamIdleTimeoutSeconds = 1 // postTimeout = max(1, 10) = 10s（包间下限生效）
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

	sess, err := netClient.CreateSession(180, "", "test-idle-timeout")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer sess.Close()

	var emitted []transform.GeminiChunk
	err = vc.executeStreamingAttempt(ctx, sess, "test-model", &transform.GeminiRequest{}, "test-token", func(ch *transform.GeminiChunk) bool {
		if ch != nil {
			emitted = append(emitted, *ch)
		}
		return true
	}, &transform.TextStrategy{})

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

	cfg := config.DefaultConfig()
	cfg.StreamIdleTimeoutSeconds = 1 // preTimeout = max(2, 20) = 20s（首包下限生效）
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

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()

	sess, err := netClient.CreateSession(180, "", "test-idle-dostream-hang")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer sess.Close()

	var emitted []transform.GeminiChunk
	err = vc.executeStreamingAttempt(ctx, sess, "test-model", &transform.GeminiRequest{}, "test-token", func(ch *transform.GeminiChunk) bool {
		if ch != nil {
			emitted = append(emitted, *ch)
		}
		return true
	}, &transform.TextStrategy{})

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

// TestStreamChatOp_TokenFailure_RecaptchaUnavailable 验证 op 闭包在 GetTokenShared 失败时
// 返回 NewRecaptchaUnavailableError（Kind=infra → FailFast，不重试、不误塞 auth）。
func TestStreamChatOp_TokenFailure_RecaptchaUnavailable(t *testing.T) {
	testNodes.Reset()
	defer testNodes.Reset()

	cfg := config.DefaultConfig()
	provider := config.StaticProvider(cfg)

	netClient := transport.NewNetworkClient(nil)
	vc := &VertexAIClient{
		net: netClient,
		pool: recaptcha.NewTokenPoolCustom(func(proxyURI string) (string, error) {
			return "", fmt.Errorf("rT exhausted")
		}),
		cfg: provider,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var gotErr *VertexError
	vc.StreamChat(ctx, "test-model", &transform.GeminiRequest{}, func(chunk StreamChunk) bool {
		if chunk.Err != nil && gotErr == nil {
			gotErr = chunk.Err
		}
		return true
	}, &transform.TextStrategy{})

	if gotErr == nil {
		t.Fatal("expected error chunk, got nil")
	}
	if gotErr.Kind != "infra" || gotErr.Code != 502 {
		t.Errorf("expected infra/502, got Kind=%s Code=%d", gotErr.Kind, gotErr.Code)
	}
	if gotErr.ClassifyBatch() != FailFast {
		t.Errorf("expected FailFast disposition, got %v", gotErr.ClassifyBatch())
	}
}

// TestStreamChatOp_TruncatedAfterContent 验证 op 闭包在首帧已交付后断流时，
// 对错误标注 Truncated（Committed 语义：绝不重试），并如实透传真实原因。
func TestStreamChatOp_TruncatedAfterContent(t *testing.T) {
	testNodes.Reset()
	defer testNodes.Reset()

	// mock 上游：先发有效内容帧，随后发畸形数据触发网络扫描错误（首帧后断流）。
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}
		contentFrame := `{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":{"candidates":[{"content":{"parts":[{"text":"hello"}],"role":"model"},"finishReason":"FINISH_REASON_UNSPECIFIED"}]}}}}]}`
		_, _ = w.Write([]byte(contentFrame))
		flusher.Flush()
		_, _ = w.Write([]byte(`{"a":}`))
		flusher.Flush()
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var gotData bool
	var gotErr *VertexError
	vc.StreamChat(ctx, "test-model", &transform.GeminiRequest{}, func(chunk StreamChunk) bool {
		if chunk.Err != nil && gotErr == nil {
			gotErr = chunk.Err
			return false
		}
		if chunk.Data != nil {
			gotData = true
		}
		return true
	}, &transform.TextStrategy{})

	if !gotData {
		t.Fatal("expected content chunk before truncation")
	}
	if gotErr == nil {
		t.Fatal("expected truncated error chunk, got nil")
	}
	if !gotErr.Truncated {
		t.Errorf("expected Truncated flag set, got %+v", gotErr)
	}
	if gotErr.ClassifyBatch() != Committed {
		t.Errorf("expected Committed disposition, got %v", gotErr.ClassifyBatch())
	}
	if gotErr.Kind != "network" {
		t.Errorf("expected network kind (真实原因), got %q", gotErr.Kind)
	}
}

func firstPartText(chunk *transform.GeminiChunk) string {
	if chunk == nil || len(chunk.Candidates) == 0 || chunk.Candidates[0].Content == nil {
		return ""
	}
	if len(chunk.Candidates[0].Content.Parts) > 0 {
		return chunk.Candidates[0].Content.Parts[0].Text
	}
	return ""
}
