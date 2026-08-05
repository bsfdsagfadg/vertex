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

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/recaptcha"
	"github.com/bsfdsagfadg/vertex/internal/transport"
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
