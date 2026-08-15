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
	"testing"

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
// 用例结束后关闭）。
func newTestServer(t *testing.T) *testFixture {
	t.Helper()

	dir := t.TempDir()

	// ── config.json ──
	cfg := config.DefaultConfig()
	cfg.AdminPassword = "test-admin-pw"
	cfg.ParallelPoolEnabled = false
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
		_, _ = io.ReadAll(r.Body)

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
	netClient := transport.NewNetworkClient(nil)
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

	// ── mock upstream 使用可取消 BaseContext ──
	// 当 handler 阻塞（如 upstream_hang 测试）时，cleanup 先取消 baseCtx，
	// 使所有活跃 handler 的 r.Context().Done() 立即触发，避免 httptest.Server.Close()
	// 因等待 handler 返回而长时间挂起。
	mockUpstreamCtx, mockUpstreamCancel := context.WithCancel(context.Background())
	mockUpstream := httptest.NewUnstartedServer(http.HandlerFunc(mockHandler))
	mockUpstream.Config.BaseContext = func(net.Listener) context.Context {
		return mockUpstreamCtx
	}
	mockUpstream.Start()
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
	netClient := transport.NewNetworkClient(dialer)
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
		mockUpstreamCancel()
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
	return resp
}

// directDialer 实现 transport.ProxyDialer，对所有 URI 返回直连 DialContext。
// 用于并行池测试中避免 nil dialer panic。
type directDialer struct{}

func (d *directDialer) CreateDialer(uri string, reqID string) (func(ctx context.Context, network, addr string) (net.Conn, error), func(), error) {
	var dialer net.Dialer
	return dialer.DialContext, func() {}, nil
}

func (d *directDialer) StopAll()                      {}
func (d *directDialer) GetNextEntrySocksAddr() string { return "" }
func (d *directDialer) SyncEntryPool() error          { return nil }
func (d *directDialer) TestEntryProxy(uri string) (func(ctx context.Context, network, addr string) (net.Conn, error), func(), error) {
	return d.CreateDialer(uri, "test")
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

// goroutineLeakCheck 在 cleanup 中检查 goroutine 是否回到基线。
func goroutineLeakCheck(t *testing.T) {
	t.Helper()
	baseline := runtime.NumGoroutine()
	t.Cleanup(func() {
		if leaked := runtime.NumGoroutine() - baseline; leaked > 30 {
			t.Errorf("possible goroutine leak: +%d goroutines (baseline=%d, current=%d)",
				leaked, baseline, runtime.NumGoroutine())
		}
	})
}