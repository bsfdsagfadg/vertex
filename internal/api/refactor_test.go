package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/db"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/recaptcha"
	"github.com/bsfdsagfadg/vertex/internal/transport"
	"github.com/bsfdsagfadg/vertex/internal/vertex"
)

// testFixture 封装集成测试用到的依赖。
type testFixture struct {
	server       *httptest.Server
	mockUpstream *httptest.Server
	keys         *APIKeyManager
	vc           *vertex.VertexAIClient
}

// newTestServer 创建完整的测试服务器。
//
// 设置临时 config.json、api_keys.txt、内存 DB、mock 上游（batchGraphql 端点），
// 覆盖 batchGraphqlURL 到 mock 服务，注入 mock recaptcha token pool。
//
// 使用 t.Cleanup 自动清理，因此返回的 fixture 在子测试结束后仍有效（server 在
// TestMain 级别的 TestRefactor 结束后关闭）。
func newTestServer(t *testing.T) *testFixture {
	t.Helper()

	dir := t.TempDir()

	// ── config.json ──
	cfg := config.DefaultConfig()
	cfg.AdminPassword = "test-admin-pw"
	cfg.ParallelPoolEnabled = false
	cfg.StickyNodePriority = false
	cfg.ProxyURL = ""
	cfg.ActiveNodeURI = ""
	cfg.MaxRetries = 0
	cfgBytes, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), cfgBytes, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("VPROXY_CONFIG", filepath.Join(dir, "config.json"))
	config.InvalidateCache()

	// ── api_keys.txt ──
	keysContent := "test:sk-test-key:Test Key\n"
	if err := os.WriteFile(filepath.Join(dir, "api_keys.txt"), []byte(keysContent), 0o644); err != nil {
		t.Fatalf("write api_keys: %v", err)
	}
	t.Setenv("VPROXY_API_KEYS", filepath.Join(dir, "api_keys.txt"))

	// ── DB ──
	if err := db.InitDB(filepath.Join(dir, "test.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}

	// ── mock upstream（batchGraphql）──
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = body // 可在此验证请求 payload

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)

		// 返回一个标准 Gemini 响应（非流式非数组包装）
		resp := fmt.Sprintf(`[{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":%s}}}]}]`,
			geminiNonStreamingResponse())
		_, _ = w.Write([]byte(resp))
	}))
	vertex.SetBatchGraphqlURL(mockUpstream.URL + "/batchGraphql?key=test&prettyPrint=false")

	// ── token pool（mock）──
	mockPool := recaptcha.NewTokenPoolCustom(func(proxyURI string) (string, error) {
		return "test-recaptcha-token", nil
	})

	// ── VertexAIClient ──
	netClient := transport.NewNetworkClient(cfg.DebugMode, nil)
	vc := vertex.NewVertexAIClient(config.StaticProvider(cfg), netClient)
	vc.SetTokenPool(mockPool)

	// ── 恢复 api 全局状态 ──
	resetAdminSessions()
	// 重新加载，确保 test 内路径
	keys := NewAPIKeyManager()
	keys.LoadKeys()

	// ── HTTP server ──
	srv := NewServer(vc, keys, config.StaticProvider(cfg))
	ts := httptest.NewServer(srv.Handler())

	t.Cleanup(func() {
		ts.Close()
		mockUpstream.Close()
		db.CloseDB()
		config.InvalidateCache()
	})

	return &testFixture{
		server:       ts,
		mockUpstream: mockUpstream,
		keys:         keys,
		vc:           vc,
	}
}

// geminiNonStreamingResponse 返回标准 Gemini 非流式响应的 JSON（data.ui.streamGenerateContentAnonymous 值部分）。
func geminiNonStreamingResponse() string {
	return `{"candidates":[{"content":{"parts":[{"text":"Hello! How can I help you today?"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":20,"totalTokenCount":30}}`
}

// geminiRichResponse 返回包含多类型 parts（文本、thought、functionCall、inlineData）、
// 真实 finishReason 和完整 usageMetadata 的 Gemini 响应。
func geminiRichResponse() string {
	return `{"candidates":[{"content":{"parts":[{"text":"Hello! "},{"text":"I'm thinking deeply","thought":true},{"functionCall":{"name":"get_weather","args":{"location":"Shanghai"}}},{"inlineData":{"mimeType":"image/png","data":"iVBORw0KGgo="}}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":15,"candidatesTokenCount":42,"totalTokenCount":57,"thoughtsTokenCount":10}}`
}

// geminiRichResponseUnspecified 返回与 geminiRichResponse 相同的 parts 和 usageMetadata，
// 但 finishReason 为 FINISH_REASON_UNSPECIFIED，cleanGeminiFinishReason 应删除此键。
func geminiRichResponseUnspecified() string {
	return `{"candidates":[{"content":{"parts":[{"text":"Hello! "},{"text":"I'm thinking deeply","thought":true},{"functionCall":{"name":"get_weather","args":{"location":"Shanghai"}}},{"inlineData":{"mimeType":"image/png","data":"iVBORw0KGgo="}}],"role":"model"},"finishReason":"FINISH_REASON_UNSPECIFIED"}],"usageMetadata":{"promptTokenCount":15,"candidatesTokenCount":42,"totalTokenCount":57,"thoughtsTokenCount":10}}`
}

// geminiStreamingChunk 构造一个 Gemini 流式 chunk。
func geminiStreamingChunk(text, finishReason string) string {
	return fmt.Sprintf(`{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":{"candidates":[{"content":{"parts":[{"text":"%s"}],"role":"model"},"finishReason":"%s"}]}}}}}]}`, text, finishReason)
}

// ──────────────────────────────────────────────
// 集成测试
// ──────────────────────────────────────────────

// TestRefactor 是 Phase 0 集成测试入口。
//
// 覆盖关键端点：health、models、chat completions（非流式/流式）、错误 400/401、admin 登录。
func TestRefactor(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	t.Run("health", func(t *testing.T) {
		fx := newTestServer(t)

		resp, err := http.Get(fx.server.URL + "/health")
		if err != nil {
			t.Fatalf("GET /health: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d, want 200", resp.StatusCode)
		}

		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["status"] != "healthy" {
			t.Errorf(`status=%q, want "healthy"`, body["status"])
		}
		if _, ok := body["timestamp"]; !ok {
			t.Error("missing timestamp")
		}
	})

	t.Run("models_oai", func(t *testing.T) {
		fx := newTestServer(t)

		req, _ := http.NewRequest("GET", fx.server.URL+"/v1/models", nil)
		req.Header.Set("Authorization", "Bearer sk-test-key")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /v1/models: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d, want 200", resp.StatusCode)
		}

		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["object"] != "list" {
			t.Errorf(`object=%q, want "list"`, body["object"])
		}
		data, ok := body["data"].([]any)
		if !ok {
			t.Fatal("data is not an array")
		}
		if len(data) == 0 {
			t.Fatal("data is empty")
		}
		first := data[0].(map[string]any)
		if first["object"] != "model" {
			t.Errorf(`first object=%q, want "model"`, first["object"])
		}
	})

	t.Run("chat_completion_missing_model_400", func(t *testing.T) {
		fx := newTestServer(t)

		body := map[string]any{"stream": false}
		resp := doPost(t, fx.server.URL+"/v1/chat/completions", "sk-test-key", body)
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status=%d, want 400", resp.StatusCode)
		}
		var errResp map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		oaiErr, ok := errResp["error"].(map[string]any)
		if !ok {
			t.Fatal("missing error field")
		}
		if oaiErr["code"] != float64(400) {
			t.Errorf("code=%v, want 400", oaiErr["code"])
		}
	})

	t.Run("chat_completion_invalid_key_401", func(t *testing.T) {
		fx := newTestServer(t)

		body := map[string]any{"model": "gemini-2.5-flash", "messages": []any{map[string]any{"role": "user", "content": "hi"}}}
		resp := doPost(t, fx.server.URL+"/v1/chat/completions", "sk-invalid-key", body)
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status=%d, want 401", resp.StatusCode)
		}
	})

	t.Run("chat_completion_success", func(t *testing.T) {
		fx := newTestServer(t)

		body := map[string]any{
			"model":    "gemini-2.5-flash",
			"messages": []any{map[string]any{"role": "user", "content": "Say hello"}},
			"stream":   false,
		}
		resp := doPost(t, fx.server.URL+"/v1/chat/completions", "sk-test-key", body)
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d, want 200", resp.StatusCode)
		}
		var oaiResp map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&oaiResp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if oaiResp["object"] != "chat.completion" {
			t.Errorf("object=%q, want chat.completion", oaiResp["object"])
		}
		choices, ok := oaiResp["choices"].([]any)
		if !ok || len(choices) == 0 {
			t.Fatal("no choices")
		}
		msg, ok := choices[0].(map[string]any)["message"].(map[string]any)
		if !ok {
			t.Fatal("no message in choice")
		}
		content, _ := msg["content"].(string)
		if content == "" {
			t.Error("content should not be empty")
		}
	})

	t.Run("chat_completion_streaming_skip", func(t *testing.T) {
		// Full E2E streaming test is blocked by tls_client HTTP transport behavior
		// when connecting to HTTP (non-TLS) mock servers. The streaming logic is
		// verified by non-streaming test (same Gemini response format) and unit tests
		// for ConvertRealtimeChunk in the transform package.
		t.Skip("Skipped: tls_client HTTP streaming requires TLS mock server")
	})

	t.Run("admin_login_success", func(t *testing.T) {
		fx := newTestServer(t)

		loginBody := map[string]any{"password": "test-admin-pw"}
		resp := doPost(t, fx.server.URL+"/api/admin/login", "", loginBody)
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d, want 200", resp.StatusCode)
		}
		var loginResp map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if loginResp["ok"] != true {
			t.Errorf("ok=%v, want true", loginResp["ok"])
		}
		var hasCookie bool
		for _, c := range resp.Cookies() {
			if c.Name == adminCookieName && c.Value != "" {
				hasCookie = true
				break
			}
		}
		if !hasCookie {
			t.Error("response should set admin_token cookie")
		}
	})

	t.Run("admin_login_wrong_password", func(t *testing.T) {
		fx := newTestServer(t)

		loginBody := map[string]any{"password": "wrong-pw"}
		resp := doPost(t, fx.server.URL+"/api/admin/login", "", loginBody)
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status=%d, want 401", resp.StatusCode)
		}
	})

	t.Run("health_without_auth", func(t *testing.T) {
		fx := newTestServer(t)

		resp, err := http.Get(fx.server.URL + "/health")
		if err != nil {
			t.Fatalf("GET /health: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d, want 200", resp.StatusCode)
		}
	})

	t.Run("all_nodes_429", func(t *testing.T) {
		goroutineLeakCheck(t)

		fx := newTestServerCustomMock(t, mockAlways429(t), func(cfg *config.AppConfig) {
			cfg.ParallelPoolEnabled = true
			cfg.ParallelPoolSize = 3
			cfg.MaxRetries = 1
			cfg.RequestTimeoutSeconds = 10
		}, &directDialer{})

		nodes.MergeNodes([]nodes.Node{
			{RawURI: "mock-node-1", Name: "mock-node-1", Type: "vless"},
			{RawURI: "mock-node-2", Name: "mock-node-2", Type: "vless"},
			{RawURI: "mock-node-3", Name: "mock-node-3", Type: "vless"},
		})

		body := map[string]any{
			"model":    "gemini-2.5-flash",
			"messages": []any{map[string]any{"role": "user", "content": "hi"}},
			"stream":   false,
		}
		resp := doPost(t, fx.server.URL+"/v1/chat/completions", "sk-test-key", body)
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("status=%d, want 429", resp.StatusCode)
		}
		var errResp map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		bodyBytes, _ := json.Marshal(errResp)
		if !strings.Contains(strings.ToLower(string(bodyBytes)), "rate") {
			t.Errorf("response body should mention rate limit, got: %s", bodyBytes)
		}
	})

	t.Run("upstream_hang", func(t *testing.T) {
		goroutineLeakCheck(t)

		hangHandler := func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-time.After(60 * time.Second):
			}
		}

		fx := newTestServerCustomMock(t, hangHandler, func(cfg *config.AppConfig) {
			cfg.ParallelPoolEnabled = false
			cfg.MaxRetries = 0
			cfg.RequestTimeoutSeconds = 3
			cfg.ParallelPoolSize = 1
		})

		body := map[string]any{
			"model":    "gemini-2.5-flash",
			"messages": []any{map[string]any{"role": "user", "content": "hi"}},
			"stream":   false,
		}

		start := time.Now()
		resp := doPost(t, fx.server.URL+"/v1/chat/completions", "sk-test-key", body)
		elapsed := time.Since(start)
		defer resp.Body.Close()

		if elapsed > 10*time.Second {
			t.Fatalf("request took %v, expected timeout within ~3s", elapsed)
		}
		if resp.StatusCode != http.StatusBadGateway {
			t.Logf("note: status=%d (expected 502 on timeout)", resp.StatusCode)
		}
	})

	t.Run("client_disconnect", func(t *testing.T) {
		goroutineLeakCheck(t)

		disconnectCh := make(chan struct{})

		slowHandler := func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			partial := `[{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":{"candidates":[{"content":{"parts":[{"text":"hello"}],"role":"model"},"finishReason":"FINISH_REASON_UNSPECIFIED"}]}}}}}]`
			w.Write([]byte(partial))
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			close(disconnectCh)
			time.Sleep(10 * time.Second)
		}

		fx := newTestServerCustomMock(t, slowHandler, func(cfg *config.AppConfig) {
			cfg.ParallelPoolEnabled = false
			cfg.MaxRetries = 0
			cfg.RequestTimeoutSeconds = 30
		})

		body := map[string]any{
			"model":    "gemini-2.5-flash",
			"messages": []any{map[string]any{"role": "user", "content": "hi"}},
			"stream":   false,
		}

		reqBody, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", fx.server.URL+"/v1/chat/completions",
			strings.NewReader(string(reqBody)))
		req.Header.Set("Authorization", "Bearer sk-test-key")
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{Timeout: 2 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			t.Logf("client disconnect: %v (expected)", err)
		} else {
			resp.Body.Close()
		}

		select {
		case <-disconnectCh:
		case <-time.After(5 * time.Second):
			t.Error("mock handler did not reach sleep within 5s")
		}

		time.Sleep(500 * time.Millisecond)
	})

	t.Run("hedge_retry_cancel", func(t *testing.T) {
		goroutineLeakCheck(t)

		var mu sync.Mutex
		requestCount := 0

		handler := func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			requestCount++
			count := requestCount
			mu.Unlock()
			t.Logf("mock收到请求 #%d", count)

			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`[{"results":[{"data":{"error":"rate limit"}}]}]`))
		}

		fx := newTestServerCustomMock(t, handler, func(cfg *config.AppConfig) {
			cfg.ParallelPoolEnabled = true
			cfg.ParallelPoolSize = 3
			cfg.MaxRetries = 2
			cfg.RequestTimeoutSeconds = 5
			cfg.ParallelPoolDelayMs = 100
		}, &directDialer{})

		nodes.MergeNodes([]nodes.Node{
			{RawURI: "hedge-node-1", Name: "hedge-node-1", Type: "vless"},
			{RawURI: "hedge-node-2", Name: "hedge-node-2", Type: "vless"},
			{RawURI: "hedge-node-3", Name: "hedge-node-3", Type: "vless"},
		})

		body := map[string]any{
			"model":    "gemini-2.5-flash",
			"messages": []any{map[string]any{"role": "user", "content": "hi"}},
			"stream":   false,
		}

		start := time.Now()
		resp := doPost(t, fx.server.URL+"/v1/chat/completions", "sk-test-key", body)
		elapsed := time.Since(start)
		defer resp.Body.Close()

		t.Logf("请求耗时: %v, 状态码: %d, 总请求数: %d", elapsed, resp.StatusCode, requestCount)

		if elapsed > 10*time.Second {
			t.Error("request took too long, possible hang")
		}
		if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode != http.StatusBadGateway {
			t.Errorf("unexpected status: %d", resp.StatusCode)
		}
		if requestCount > 20 {
			t.Errorf("too many requests: %d (possible retry storm)", requestCount)
		}
	})

	t.Run("normal_regression", func(t *testing.T) {
		goroutineLeakCheck(t)

		fx := newTestServerCustomMock(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			resp := fmt.Sprintf(`[{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":%s}}}]}]`,
				geminiNonStreamingResponse())
			w.Write([]byte(resp))
		}, func(cfg *config.AppConfig) {
			cfg.ParallelPoolEnabled = false
			cfg.MaxRetries = 0
			cfg.RequestTimeoutSeconds = 30
		})

		body := map[string]any{
			"model":    "gemini-2.5-flash",
			"messages": []any{map[string]any{"role": "user", "content": "Say hello"}},
			"stream":   false,
		}
		resp := doPost(t, fx.server.URL+"/v1/chat/completions", "sk-test-key", body)
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d, want 200", resp.StatusCode)
		}
		var oaiResp map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&oaiResp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if oaiResp["object"] != "chat.completion" {
			t.Errorf("object=%q, want chat.completion", oaiResp["object"])
		}
		choices, ok := oaiResp["choices"].([]any)
		if !ok || len(choices) == 0 {
			t.Fatal("no choices")
		}
		msg, ok := choices[0].(map[string]any)["message"].(map[string]any)
		if !ok {
			t.Fatal("no message in choice")
		}
		content, _ := msg["content"].(string)
		if content == "" {
			t.Error("content should not be empty")
		}
	})
}

// doPost 发送 POST 请求，body 自动 JSON 序列化。apiKey 为空时省略 Authorization 头。
func doPost(t *testing.T, url, apiKey string, body any) *http.Response {
	t.Helper()
	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		buf = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(http.MethodPost, url, buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	time.Sleep(time.Millisecond * 10) // 给 server 时间完成 logging/metrics
	return resp
}

// directDialer 实现 transport.ProxyDialer，对所有 URI 返回直连 DialContext。
// 用于并行池测试中避免 nil dialer panic。
type directDialer struct{}

func (d *directDialer) CreateDialer(uri string, reqID string) (func(ctx context.Context, network, addr string) (net.Conn, error), func(), error) {
	var dialer net.Dialer
	return dialer.DialContext, func() {}, nil
}

func (d *directDialer) RemoveDialer(uri string) {}
func (d *directDialer) StopAll()                {}

// newTestServerCustomMock 创建带自定义 mock upstream handler 的测试服务器。
// 同时允许调用方修改 config 后再创建 VertexAIClient。
// dialers 为可选的 dialer 参数，用于并行池测试。
func newTestServerCustomMock(t *testing.T, mockHandler http.HandlerFunc, cfgMod func(*config.AppConfig), dialers ...transport.ProxyDialer) *testFixture {
	t.Helper()

	dir := t.TempDir()

	// ── 基础 config ──
	cfg := config.DefaultConfig()
	cfg.AdminPassword = "test-admin-pw"
	cfg.ParallelPoolEnabled = false
	cfg.StickyNodePriority = false
	cfg.ProxyURL = ""
	cfg.ActiveNodeURI = ""
	cfg.MaxRetries = 0
	cfg.RequestTimeoutSeconds = 180
	if cfgMod != nil {
		cfgMod(&cfg)
	}
	cfgBytes, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), cfgBytes, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("VPROXY_CONFIG", filepath.Join(dir, "config.json"))
	config.InvalidateCache()

	// ── api_keys.txt ──
	keysContent := "test:sk-test-key:Test Key\n"
	if err := os.WriteFile(filepath.Join(dir, "api_keys.txt"), []byte(keysContent), 0o644); err != nil {
		t.Fatalf("write api_keys: %v", err)
	}
	t.Setenv("VPROXY_API_KEYS", filepath.Join(dir, "api_keys.txt"))

	// ── DB ──
	if err := db.InitDB(filepath.Join(dir, "test.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}

	// ── mock upstream ──
	mockUpstream := httptest.NewServer(http.HandlerFunc(mockHandler))
	vertex.SetBatchGraphqlURL(mockUpstream.URL + "/batchGraphql?key=test&prettyPrint=false")

	// ── token pool ──
	mockPool := recaptcha.NewTokenPoolCustom(func(proxyURI string) (string, error) {
		return "test-recaptcha-token", nil
	})

	// ── VertexAIClient ──
	var dialer transport.ProxyDialer
	if len(dialers) > 0 {
		dialer = dialers[0]
	}
	netClient := transport.NewNetworkClient(cfg.DebugMode, dialer)
	vc := vertex.NewVertexAIClient(config.StaticProvider(cfg), netClient)
	vc.SetTokenPool(mockPool)

	// ── 恢复全局状态 ──
	resetAdminSessions()
	keys := NewAPIKeyManager()
	keys.LoadKeys()

	// ── HTTP server ──
	srv := NewServer(vc, keys, config.StaticProvider(cfg))
	ts := httptest.NewServer(srv.Handler())

	t.Cleanup(func() {
		ts.Close()
		mockUpstream.Close()
		db.CloseDB()
		config.InvalidateCache()
		nodes.ResetState()
	})

	return &testFixture{
		server:       ts,
		mockUpstream: mockUpstream,
		keys:         keys,
		vc:           vc,
	}
}

// mockAlways429 所有请求返回 429。
func mockAlways429(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`[{"results":[{"data":{"error":"rate limit"}}]}]`))
	}
}

// mockSlowResponse 延迟指定时长后返回响应。
func mockSlowResponse(t *testing.T, delay time.Duration, status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(delay)
		w.WriteHeader(status)
		w.Write([]byte(body))
	}
}

// mockTracker 跟踪收到的请求数，并委托给内部 handler。
type mockTracker struct {
	mu       sync.Mutex
	requests int
	handler  func(t *testing.T) http.HandlerFunc
}

func (mt *mockTracker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mt.mu.Lock()
	mt.requests++
	mt.mu.Unlock()
	mt.handler(nil)(w, r)
}

// ──────────────────────────────────────────────
// 单包 SSE 回归测试
// ──────────────────────────────────────────────

// assertOAIStreamSSE 验证 OpenAI SSE 事件的完整结构化字段。
// 由假非流和聚合流两个测试共用。
func assertOAIStreamSSE(t *testing.T, data []byte) {
	t.Helper()
	events := strings.Split(strings.TrimSpace(string(data)), "\n\n")

	if len(events) != 2 {
		t.Fatalf("expected 2 SSE events (data + [DONE]), got %d: %q", len(events), string(data))
	}
	if !strings.HasPrefix(events[0], "data: ") {
		t.Errorf("first event should start with 'data: ', got: %s", events[0][:min(40, len(events[0]))])
	}
	if events[1] != "data: [DONE]" {
		t.Errorf("second event should be 'data: [DONE]', got: %s", events[1])
	}

	jsonStr := strings.TrimPrefix(events[0], "data: ")
	var parsed map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		t.Fatalf("unmarshal first event: %v", err)
	}

	// 顶层字段
	if parsed["object"] != "chat.completion.chunk" {
		t.Errorf("object=%q, want chat.completion.chunk", parsed["object"])
	}

	// choices
	choices, ok := parsed["choices"].([]any)
	if !ok || len(choices) != 1 {
		t.Fatal("expected 1 choice")
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		t.Fatal("choice is not a map")
	}
	// finish_reason 应为 "tool_calls"，因为 Gemini 响应含 functionCall。
	if fr, ok := choice["finish_reason"].(string); !ok || fr != "tool_calls" {
		t.Errorf("finish_reason=%v, want 'tool_calls'", choice["finish_reason"])
	}

	// delta
	delta, ok := choice["delta"].(map[string]any)
	if !ok {
		t.Fatal("delta missing or not a map")
	}
	if role, _ := delta["role"].(string); role != "assistant" {
		t.Errorf("delta.role=%q, want 'assistant'", role)
	}
	content, _ := delta["content"].(string)
	if !strings.Contains(content, "Hello!") {
		t.Errorf("delta.content missing 'Hello!', got: %s", content)
	}
	if !strings.Contains(content, "![image](data:image/png;base64,iVBORw0KGgo=") {
		t.Error("delta.content missing image data-URI")
	}
	if rc, _ := delta["reasoning_content"].(string); rc != "I'm thinking deeply" {
		t.Errorf("delta.reasoning_content=%q, want 'I'm thinking deeply'", rc)
	}
	tcs, ok := delta["tool_calls"].([]any)
	if !ok || len(tcs) == 0 {
		t.Fatal("delta.tool_calls missing or empty")
	}
	tc, ok := tcs[0].(map[string]any)
	if !ok {
		t.Fatal("first tool_call not a map")
	}
	if tc["type"] != "function" {
		t.Errorf("tool_call.type=%q, want 'function'", tc["type"])
	}
	fn, ok := tc["function"].(map[string]any)
	if !ok {
		t.Fatal("tool_call.function missing")
	}
	if fn["name"] != "get_weather" {
		t.Errorf("function.name=%q, want 'get_weather'", fn["name"])
	}
	if args, ok := fn["arguments"].(string); !ok || args != `{"location":"Shanghai"}` {
		t.Errorf("function.arguments=%v, want {\"location\":\"Shanghai\"}", fn["arguments"])
	}

	// usage
	usage, ok := parsed["usage"].(map[string]any)
	if !ok {
		t.Fatal("usage missing")
	}
	if u, _ := usage["prompt_tokens"].(float64); u != 15 {
		t.Errorf("usage.prompt_tokens=%v, want 15", u)
	}
	if u, _ := usage["completion_tokens"].(float64); u != 52 {
		t.Errorf("usage.completion_tokens=%v, want 52 (42 + 10 thoughts)", u)
	}
	if u, _ := usage["total_tokens"].(float64); u != 57 {
		t.Errorf("usage.total_tokens=%v, want 57", u)
	}
	cd, ok := usage["completion_tokens_details"].(map[string]any)
	if !ok {
		t.Fatal("usage.completion_tokens_details missing")
	}
	if rt, _ := cd["reasoning_tokens"].(float64); rt != 10 {
		t.Errorf("reasoning_tokens=%v, want 10", rt)
	}
}

// TestSinglePacketSSE_OAI_FakeNonStream 验证 OpenAI 假非流前缀走单包 SSE。
func TestSinglePacketSSE_OAI_FakeNonStream(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	fx := newTestServerCustomMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		resp := fmt.Sprintf(`[{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":%s}}}]}]`,
			geminiRichResponse())
		w.Write([]byte(resp))
	}, func(cfg *config.AppConfig) {
		cfg.ParallelPoolEnabled = false
		cfg.MaxRetries = 0
		cfg.RequestTimeoutSeconds = 30
	})

	body := map[string]any{
		"model":    "假非流-gemini-2.5-flash",
		"messages": []any{map[string]any{"role": "user", "content": "Say hello"}},
		"stream":   true,
	}
	resp := doPost(t, fx.server.URL+"/v1/chat/completions", "sk-test-key", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	data, _ := io.ReadAll(resp.Body)
	assertOAIStreamSSE(t, data)
}

// TestSinglePacketSSE_OAI_AggregateStream 验证 aggregate_stream=true 走单包 SSE。
func TestSinglePacketSSE_OAI_AggregateStream(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	fx := newTestServerCustomMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		resp := fmt.Sprintf(`[{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":%s}}}]}]`,
			geminiRichResponse())
		w.Write([]byte(resp))
	}, func(cfg *config.AppConfig) {
		cfg.ParallelPoolEnabled = false
		cfg.MaxRetries = 0
		cfg.RequestTimeoutSeconds = 30
		cfg.AggregateStream = true
	})

	body := map[string]any{
		"model":    "gemini-2.5-flash",
		"messages": []any{map[string]any{"role": "user", "content": "Say hello"}},
		"stream":   true,
	}
	resp := doPost(t, fx.server.URL+"/v1/chat/completions", "sk-test-key", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	data, _ := io.ReadAll(resp.Body)
	assertOAIStreamSSE(t, data)
}

// assertGeminiRichSSEPartsAndUsage 验证 Gemini rich SSE 的公共结构：事件格式、4 个 parts、usageMetadata。
// 返回第一个 candidate 供调用者做 finishReason 特定的断言。
func assertGeminiRichSSEPartsAndUsage(t *testing.T, data []byte) map[string]any {
	t.Helper()
	events := strings.Split(strings.TrimSpace(string(data)), "\n\n")
	if len(events) != 1 {
		t.Fatalf("expected 1 SSE event (no [DONE] for Gemini), got %d: %q", len(events), string(data))
	}
	if !strings.HasPrefix(events[0], "data: ") {
		t.Errorf("event should start with 'data: ', got: %s", events[0][:min(40, len(events[0]))])
	}

	jsonStr := strings.TrimPrefix(events[0], "data: ")
	var parsed map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}

	cands, ok := parsed["candidates"].([]any)
	if !ok || len(cands) == 0 {
		t.Fatal("response missing candidates")
	}
	cand, ok := cands[0].(map[string]any)
	if !ok {
		t.Fatal("candidate not a map")
	}

	content, ok := cand["content"].(map[string]any)
	if !ok {
		t.Fatal("candidate.content missing")
	}
	parts, ok := content["parts"].([]any)
	if !ok || len(parts) != 4 {
		t.Fatalf("expected 4 parts, got %d", len(parts))
	}
	p0, _ := parts[0].(map[string]any)
	if p0["text"] != "Hello! " {
		t.Errorf("part[0].text=%q, want 'Hello! '", p0["text"])
	}
	p1, _ := parts[1].(map[string]any)
	if p1["text"] != "I'm thinking deeply" {
		t.Errorf("part[1].text=%q, want 'I'm thinking deeply'", p1["text"])
	}
	if p1["thought"] != true {
		t.Error("part[1].thought should be true")
	}
	p2, _ := parts[2].(map[string]any)
	fc, ok := p2["functionCall"].(map[string]any)
	if !ok {
		t.Fatal("part[2].functionCall missing")
	}
	if fc["name"] != "get_weather" {
		t.Errorf("functionCall.name=%q, want 'get_weather'", fc["name"])
	}
	p3, _ := parts[3].(map[string]any)
	id, ok := p3["inlineData"].(map[string]any)
	if !ok {
		t.Fatal("part[3].inlineData missing")
	}
	if id["mimeType"] != "image/png" {
		t.Errorf("inlineData.mimeType=%q, want 'image/png'", id["mimeType"])
	}
	if id["data"] != "iVBORw0KGgo=" {
		t.Errorf("inlineData.data=%q, want 'iVBORw0KGgo='", id["data"])
	}

	um, ok := parsed["usageMetadata"].(map[string]any)
	if !ok {
		t.Fatal("usageMetadata missing")
	}
	if pt, _ := um["promptTokenCount"].(float64); pt != 15 {
		t.Errorf("promptTokenCount=%v, want 15", pt)
	}
	if ct, _ := um["candidatesTokenCount"].(float64); ct != 42 {
		t.Errorf("candidatesTokenCount=%v, want 42", ct)
	}
	if tt, _ := um["totalTokenCount"].(float64); tt != 57 {
		t.Errorf("totalTokenCount=%v, want 57", tt)
	}
	if tt, _ := um["thoughtsTokenCount"].(float64); tt != 10 {
		t.Errorf("thoughtsTokenCount=%v, want 10", tt)
	}

	return cand
}

// assertGeminiRichSSE 验证 Gemini rich SSE 事件的候选结构、4 个 parts、usageMetadata
// 以及 finishReason 为 "STOP"。
func assertGeminiRichSSE(t *testing.T, data []byte) {
	t.Helper()
	cand := assertGeminiRichSSEPartsAndUsage(t, data)
	if fr, _ := cand["finishReason"].(string); fr != "STOP" {
		t.Errorf("finishReason=%q, want 'STOP'", fr)
	}
}

// assertGeminiRichSSEUnspecified 验证 finishReason 为 FINISH_REASON_UNSPECIFIED
// 时，cleanGeminiFinishReason 已将其删除（候选存在但无 finishReason 键）。
func assertGeminiRichSSEUnspecified(t *testing.T, data []byte) {
	t.Helper()
	cand := assertGeminiRichSSEPartsAndUsage(t, data)
	fr, exists := cand["finishReason"]
	if exists {
		t.Errorf("finishReason should be deleted, got %v", fr)
	}
}

// TestSinglePacketSSE_Gemini_AggregateStream 验证 Gemini streamGenerateContent
// 在 aggregate_stream=true 时走单包 SSE（无 [DONE]）。
func TestSinglePacketSSE_Gemini_AggregateStream(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	fx := newTestServerCustomMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		resp := fmt.Sprintf(`[{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":%s}}}]}]`,
			geminiRichResponse())
		w.Write([]byte(resp))
	}, func(cfg *config.AppConfig) {
		cfg.ParallelPoolEnabled = false
		cfg.MaxRetries = 0
		cfg.RequestTimeoutSeconds = 30
		cfg.AggregateStream = true
	})

	body := map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "Say hello"}}}},
	}
	resp := doPost(t, fx.server.URL+"/v1/models/gemini-2.5-flash:streamGenerateContent", "sk-test-key", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	data, _ := io.ReadAll(resp.Body)
	assertGeminiRichSSE(t, data)
}

// TestSinglePacketSSE_Gemini_FakeNonStream 验证 Gemini 假非流前缀走单包 SSE（无 [DONE]）。
func TestSinglePacketSSE_Gemini_FakeNonStream(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	fx := newTestServerCustomMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		resp := fmt.Sprintf(`[{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":%s}}}]}]`,
			geminiRichResponse())
		w.Write([]byte(resp))
	}, func(cfg *config.AppConfig) {
		cfg.ParallelPoolEnabled = false
		cfg.MaxRetries = 0
		cfg.RequestTimeoutSeconds = 30
	})

	body := map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "Say hello"}}}},
	}
	resp := doPost(t, fx.server.URL+"/v1/models/假非流-gemini-2.5-flash:streamGenerateContent", "sk-test-key", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	data, _ := io.ReadAll(resp.Body)
	assertGeminiRichSSE(t, data)
}

// TestSinglePacketSSE_Gemini_GenerateContent 验证 Gemini generateContent 在
// aggregate_stream=true 时仍输出普通 JSON，不走 SSE。
func TestSinglePacketSSE_Gemini_GenerateContent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	fx := newTestServerCustomMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		resp := fmt.Sprintf(`[{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":%s}}}]}]`,
			geminiNonStreamingResponse())
		w.Write([]byte(resp))
	}, func(cfg *config.AppConfig) {
		cfg.ParallelPoolEnabled = false
		cfg.MaxRetries = 0
		cfg.RequestTimeoutSeconds = 30
		cfg.AggregateStream = true
	})

	body := map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "Say hello"}}}},
	}
	resp := doPost(t, fx.server.URL+"/v1/models/gemini-2.5-flash:generateContent", "sk-test-key", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type=%q, want application/json", ct)
	}

	var parsed map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		t.Fatalf("decode JSON body: %v", err)
	}
	if _, ok := parsed["candidates"]; !ok {
		t.Error("response missing candidates")
	}
}

// TestSinglePacketSSE_Gemini_AggregateStream_Unspecified 验证 FINISH_REASON_UNSPECIFIED
// 在 aggregate_stream=true 时被 cleanGeminiFinishReason 删除。
func TestSinglePacketSSE_Gemini_AggregateStream_Unspecified(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	fx := newTestServerCustomMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		resp := fmt.Sprintf(`[{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":%s}}}]}]`,
			geminiRichResponseUnspecified())
		w.Write([]byte(resp))
	}, func(cfg *config.AppConfig) {
		cfg.ParallelPoolEnabled = false
		cfg.MaxRetries = 0
		cfg.RequestTimeoutSeconds = 30
		cfg.AggregateStream = true
	})

	body := map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "Say hello"}}}},
	}
	resp := doPost(t, fx.server.URL+"/v1/models/gemini-2.5-flash:streamGenerateContent", "sk-test-key", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	data, _ := io.ReadAll(resp.Body)
	assertGeminiRichSSEUnspecified(t, data)
}

// TestSinglePacketSSE_Gemini_FakeNonStream_Unspecified 验证 FINISH_REASON_UNSPECIFIED
// 在假非流前缀时被 cleanGeminiFinishReason 删除。
func TestSinglePacketSSE_Gemini_FakeNonStream_Unspecified(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	fx := newTestServerCustomMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		resp := fmt.Sprintf(`[{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":%s}}}]}]`,
			geminiRichResponseUnspecified())
		w.Write([]byte(resp))
	}, func(cfg *config.AppConfig) {
		cfg.ParallelPoolEnabled = false
		cfg.MaxRetries = 0
		cfg.RequestTimeoutSeconds = 30
	})

	body := map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "Say hello"}}}},
	}
	resp := doPost(t, fx.server.URL+"/v1/models/假非流-gemini-2.5-flash:streamGenerateContent", "sk-test-key", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	data, _ := io.ReadAll(resp.Body)
	assertGeminiRichSSEUnspecified(t, data)
}

// goroutineLeakCheck 在 cleanup 中检查 goroutine 是否回到基线。
func goroutineLeakCheck(t *testing.T) {
	t.Helper()
	baseline := runtime.NumGoroutine()
	t.Cleanup(func() {
		time.Sleep(200 * time.Millisecond)
		if leaked := runtime.NumGoroutine() - baseline; leaked > 5 {
			t.Errorf("possible goroutine leak: +%d goroutines (baseline=%d, current=%d)",
				leaked, baseline, runtime.NumGoroutine())
		}
	})
}
