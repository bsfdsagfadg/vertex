package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/vertex"
)

// TestWithMetrics 验证 withMetrics 中间件行为：
// 设 X-Request-Id、注入 context、跳过 /health。
func TestWithMetrics(t *testing.T) {
	mw := &middleware{} //nolint:exhaustruct

	var seenReqID string
	ok := mw.withMetrics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenReqID = vertex.RequestIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	ok.ServeHTTP(rec, httptest.NewRequest("POST", "/v1beta/models/gemini-2.5-flash:generateContent", nil))
	if rec.Header().Get("X-Request-Id") == "" {
		t.Fatal("应设置 X-Request-Id 响应头")
	}
	if seenReqID == "" || seenReqID != rec.Header().Get("X-Request-Id") {
		t.Fatalf("context 里的 request-id 应与响应头一致，got ctx=%q header=%q", seenReqID, rec.Header().Get("X-Request-Id"))
	}

	// /health 应被跳过，不设 request-id。
	rec2 := httptest.NewRecorder()
	mw.withMetrics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec2, httptest.NewRequest("GET", "/health", nil))
	if rec2.Header().Get("X-Request-Id") != "" {
		t.Fatal("/health 不应设 X-Request-Id")
	}
}
