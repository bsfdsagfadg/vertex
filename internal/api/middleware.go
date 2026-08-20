package api

import (
	"context"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/cli"
	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/strutil"
	"github.com/bsfdsagfadg/vertex/internal/transport"
	"github.com/bsfdsagfadg/vertex/internal/vertex"
)

type middleware struct {
	cfg  config.ConfigProvider
	keys *APIKeyManager
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.wroteHeader = true
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (m *middleware) withRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[Server] panic recovered: %v\n%s", rec, debug.Stack())
				oaiError(w, http.StatusInternalServerError, "服务内部错误，请联系开发者 (internal error)", "server_error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (m *middleware) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "*")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (m *middleware) withBodyLimit(next http.Handler) http.Handler {
	limit := int64(m.cfg.MaxRequestMB()) << 20
	if limit <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}

func (m *middleware) withConfigSnapshot(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := config.WithSnapshot(r.Context(), m.cfg)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *middleware) withMetrics(next http.Handler) http.Handler {
	skip := map[string]bool{"/": true, "/health": true, "/api/meta/build": true}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := strutil.ReqID()
		ctx := context.WithValue(r.Context(), vertex.RequestIDKey{}, reqID)
		ctx = transport.WithRequestID(ctx, reqID)
		if skip[r.URL.Path] || isAdminPath(r.URL.Path) {
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		w.Header().Set("X-Request-Id", reqID)
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		cli.StartReq(reqID)
		start := time.Now()
		next.ServeHTTP(sw, r.WithContext(ctx))
		elapsed := time.Since(start)
		cli.FinishReq(reqID)
		log.Printf("[Server] %s %s - %d (%.3fs) 请求ID=%s", r.Method, r.URL.Path, sw.status, elapsed.Seconds(), reqID)
	})
}

func (m *middleware) withAPIKey(next http.Handler) http.Handler {
	excluded := map[string]bool{"/": true, "/health": true, "/api/meta/build": true, "/favicon.ico": true}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if excluded[r.URL.Path] || isAdminPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		key := extractAPIKey(r)
		if key == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{
				"code": 401, "message": "缺少 API 密钥 (missing API key)", "status": "UNAUTHENTICATED",
			}})
			return
		}
		if key == "sk-your-key-here" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{
				"code": 401, "message": "示例密钥禁止调用，请新建密钥。", "status": "UNAUTHENTICATED",
			}})
			return
		}
		if !m.keys.ValidateKey(key) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{
				"code": 401, "message": "API 密钥无效 (invalid API key)", "status": "UNAUTHENTICATED",
			}})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isAdminPath(path string) bool {
	return path == "/admin" || strings.HasPrefix(path, "/admin/") || strings.HasPrefix(path, "/api/admin/") || strings.HasPrefix(path, "/assets/")
}
