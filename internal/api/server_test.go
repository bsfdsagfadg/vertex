package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/vertex"
)

func TestResolveN(t *testing.T) {
	cases := []struct { //nolint:govet
		name    string
		raw     any
		maxN    int
		wantN   int
		wantErr bool
	}{
		{"nil 缺省 1", nil, 8, 1, false},
		{"float 整数", float64(3), 8, 3, false},
		{"int", 4, 8, 4, false},
		{"非整数 float", 2.5, 8, 0, true},
		{"字符串非法", "x", 8, 0, true},
		{"小于 1", float64(0), 8, 0, true},
		{"超上限", float64(20), 8, 0, true},
		{"等于上限 OK", float64(8), 8, 8, false},
		{"maxN<=0 用默认 8", float64(8), 0, 8, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n, errMsg := resolveN(c.raw, c.maxN)
			if c.wantErr {
				if errMsg == "" {
					t.Errorf("want error, got n=%d", n)
				}
				return
			}
			if errMsg != "" {
				t.Errorf("unexpected error: %s", errMsg)
			}
			if n != c.wantN {
				t.Errorf("n=%d, want %d", n, c.wantN)
			}
		})
	}
}

func TestIsSafetyBlock_PlainTextSafety(t *testing.T) {
	e := vertex.NewInvalidArgumentError("This is a safety test message", nil)
	if isSafetyBlock(e) {
		t.Error("普通 400 错误包含单词 'safety' 不应被判定为 safety block")
	}
}

func TestIsSafetyBlock_KindSafety(t *testing.T) {
	e := vertex.NewSafetyError("Blocked", "SAFETY", nil)
	if !isSafetyBlock(e) {
		t.Error("Kind==safety 的错误应被判定为 safety block")
	}
}

func TestIsSafetyBlock_StatusSafety(t *testing.T) {
	e := vertex.NewInvalidArgumentError("Blocked by content filter", nil)
	e.Status = "SAFETY"
	if !isSafetyBlock(e) {
		t.Error("Status==SAFETY 的错误应被判定为 safety block")
	}
}

func TestIsSafetyBlock_StatusBlockedReasonSafety(t *testing.T) {
	e := vertex.NewInvalidArgumentError("Blocked", nil)
	e.Status = "BLOCKED_REASON_SAFETY"
	if !isSafetyBlock(e) {
		t.Error("Status==BLOCKED_REASON_SAFETY 应被判定为 safety block")
	}
}

func TestIsSafetyBlock_Nil(t *testing.T) {
	if isSafetyBlock(nil) {
		t.Error("nil 不应被判定为 safety block")
	}
}

// TestStatusWriterFlush 验证 statusWriter 透传 Flush（保 SSE 流式不被破坏）。
func TestStatusWriterFlush(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec, status: http.StatusOK} //nolint:exhaustruct
	// httptest.ResponseRecorder 实现 http.Flusher；断言能命中且不 panic。
	if _, ok := interface{}(sw).(http.Flusher); !ok {
		t.Fatal("statusWriter 应实现 http.Flusher")
	}
	sw.Flush()
	if !rec.Flushed {
		t.Fatal("Flush 应透传到底层 ResponseRecorder")
	}
}

// TestHealth 验证 /health 端点返回 healthy 状态与时间戳。
func TestHealth(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

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
}

// TestWithMetrics_PanicDoesNotLeakReq 验证 handler panic 时 TUI 请求表项必清理：
// withMetrics 在 StartReq 后 defer FinishReq，panic 展开期 defer 先于 withRecover 的
// recover 执行，测试不挂起且正确返回 500（服务端可观测行为）。
func TestWithMetrics_PanicDoesNotLeakReq(t *testing.T) {
	mw := &middleware{} //nolint:exhaustruct

	rec := httptest.NewRecorder()
	mw.withRecover(mw.withMetrics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))).ServeHTTP(
		rec,
		httptest.NewRequest("POST", "/v1/chat/completions", nil),
	)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500", rec.Code)
	}
	if rec.Header().Get("X-Request-Id") == "" {
		t.Error("panic 路径也应设置 X-Request-Id")
	}
}

// TestHealth_WithoutAuth 验证 health 端点无需 API key 认证。
func TestHealth_WithoutAuth(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	fx := newTestServer(t)

	resp, err := http.Get(fx.server.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
}
