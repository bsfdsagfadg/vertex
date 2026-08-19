package api

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/jsonx"
	"github.com/bsfdsagfadg/vertex/internal/transform"
	"github.com/bsfdsagfadg/vertex/internal/transport"
	"github.com/bsfdsagfadg/vertex/internal/vertex"
)

type handler struct {
	vc   *vertex.VertexAIClient
	keys *APIKeyManager
	cfg  config.ConfigProvider
}

// StreamCallback 是真流式回调：data 非 nil 表示一个有效 chunk，err 非 nil 表示错误。
// 返回 false 表示调用者要求停止流。
type StreamCallback func(chunk map[string]any, err *vertex.VertexError) bool

func (h *handler) dialer() transport.ProxyDialer {
	if h.vc != nil && h.vc.Net() != nil {
		return h.vc.Net().Dialer()
	}
	return nil
}

func (h *handler) decodeAdminBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Body == nil {
		writeJSON(w, http.StatusBadRequest, adminErr("请求体为空 (empty body)"))
		return false
	}
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeJSON(w, http.StatusBadRequest, adminErr("请求格式错误 (invalid JSON)"))
		return false
	}
	return true
}

func (h *handler) adminUnauthorized(w http.ResponseWriter) {
	writeJSON(w, http.StatusUnauthorized, adminErr("未登录或会话已过期 (unauthorized)"))
}

func (h *handler) adminMethodNotAllowed(w http.ResponseWriter) {
	writeJSON(w, http.StatusMethodNotAllowed, adminErr("方法不允许 (method not allowed)"))
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	if status < 100 || status > 599 {
		status = http.StatusInternalServerError
	}
	data, err := jsonx.Marshal(body)
	if err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"序列化失败 (internal error)","type":"server_error","code":500}}`))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

// writeGeminiError 在客户端未断开时将 VertexError 渲染为 Gemini JSON 错误。
// 若 clientCtx 命中 context.Canceled，直接跳过写入。
func writeGeminiError(w http.ResponseWriter, clientCtx context.Context, e *vertex.VertexError) {
	if clientCtx != nil && errors.Is(clientCtx.Err(), context.Canceled) {
		return
	}
	if e == nil {
		e = vertex.NewInternalError("internal server error", nil)
	}
	writeJSON(w, e.Code, vertexErrorToGemini(e))
}

func newSSEWriter(w http.ResponseWriter, contentType string) *sseWriter {
	flusher, _ := w.(http.Flusher)
	sw := &sseWriter{w: w, contentType: contentType}
	if flusher != nil {
		sw.flush = flusher.Flush
	}
	return sw
}

var (
	reqIDFallback atomic.Bool
	reqIDCounter  atomic.Uint64
)

// reqID24 生成 24 位十六进制 ID（高并发优先使用 crypto/rand 避免 GC 压力，
// 失败时降级为 UnixNano + 原子计数器组合）。
func reqID24() string {
	if !reqIDFallback.Load() {
		b := make([]byte, 12)
		if _, err := cryptorand.Read(b); err == nil {
			return hex.EncodeToString(b)
		}
		reqIDFallback.Store(true)
	}
	return fmt.Sprintf("%016x%08x", uint64(time.Now().UnixNano()), reqIDCounter.Add(1))
}

func toVertexError(err error) *vertex.VertexError {
	return vertex.NormalizeError(err)
}

func isSafetyBlock(e *vertex.VertexError) bool {
	if e == nil {
		return false
	}
	if e.Kind == "safety" {
		return true
	}
	// Status 可能承载两类内容：真实拦截原因（finishReason 枚举 / blockReason 枚举）
	// 或普通 gRPC status（如 INVALID_ARGUMENT/UNAVAILABLE）。排除法 IsBlockReason 不能
	// 直接用于 Status（"UNAVAILABLE" 等任意非空值都会被误判为拦截），此处限定为
	// blockReason 取值域：BLOCKED_REASON_* 前缀（非 UNSPECIFIED）、裸枚举 OTHER，
	// 以及由 SafetyFinishReasons 覆盖的裸枚举（SAFETY/PROHIBITED_CONTENT/IMAGE_SAFETY 等）。
	return transform.IsSafetyFinishReason(e.Status) ||
		(strings.HasPrefix(e.Status, "BLOCKED_REASON_") && e.Status != "BLOCKED_REASON_UNSPECIFIED") ||
		e.Status == "OTHER"
}

func resolveN(raw any, maxN int) (int, string) {
	if maxN <= 0 {
		maxN = 8
	}
	if raw == nil {
		return 1, ""
	}
	var n int
	switch v := raw.(type) {
	case float64:
		if v != float64(int(v)) {
			return 0, "请求参数有误: n 必须是整数 (n must be an integer)"
		}
		n = int(v)
	case int:
		n = v
	case *int:
		if v != nil {
			n = *v
		} else {
			return 1, ""
		}
	default:
		return 0, "请求参数有误: n 必须是整数 (n must be an integer)"
	}
	if n < 1 {
		return 0, "请求参数有误: n 必须 >= 1 (n must be >= 1)"
	}
	if n > maxN {
		return 0, "请求参数有误: n 超过上限 " + strconv.Itoa(maxN) + " (n exceeds maximum " + strconv.Itoa(maxN) + ")"
	}
	return n, ""
}

func adminErr(msg string) map[string]any {
	return map[string]any{"error": map[string]any{"message": msg}}
}

func maskKey(key string) string {
	if len(key) <= 4 {
		return "sk-····"
	}
	return "sk-····" + key[len(key)-4:]
}

func generateAPIKey() string {
	b := make([]byte, 24)
	_, _ = cryptorand.Read(b)
	return "sk-" + hex.EncodeToString(b)
}
