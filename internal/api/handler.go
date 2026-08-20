package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/jsonx"
	"github.com/bsfdsagfadg/vertex/internal/repository"
	runtimeroute "github.com/bsfdsagfadg/vertex/internal/runtime/route"
	"github.com/bsfdsagfadg/vertex/internal/scheduler"
	"github.com/bsfdsagfadg/vertex/internal/strutil"
	"github.com/bsfdsagfadg/vertex/internal/toolstate"
	"github.com/bsfdsagfadg/vertex/internal/transport"
	"github.com/bsfdsagfadg/vertex/internal/vertex"
)

type handler struct {
	vc           *vertex.VertexAIClient
	keys         *APIKeyManager
	cfg          config.ConfigProvider
	toolStates   *toolstate.Service
	repository   *repository.SQLite
	platform     *PlatformHandler
	proxyManager *transport.ProxyManager
	nodePool     *runtimeroute.NodePool
	routePlanner *scheduler.RoutePlanner
}

func (h *handler) writeSettings(values map[string]any) error {
	if writer, ok := h.cfg.(config.ConfigWriter); ok && writer != nil {
		return writer.WriteSettings(values)
	}
	// Compatibility for tests and legacy callers that construct a handler with
	// an immutable provider. V2 Normal injects dynamicConfig, which implements
	// ConfigWriter and therefore does not take this branch.
	return config.WriteSettings(values)
}

func (h *handler) removeProxy(uri string) {
	if h.proxyManager != nil {
		h.proxyManager.RemoveProxy(uri)
		return
	}
	transport.RemoveProxy(uri)
}

func (h *handler) requestConfig(r *http.Request) config.ConfigProvider {
	return config.FromContext(r.Context(), h.cfg)
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

func oaiError(w http.ResponseWriter, status int, msg, errType string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{
		"message": msg, "type": errType, "code": status,
	}})
}

func sseEvent(obj map[string]any) string {
	data, err := jsonx.Marshal(obj)
	if err != nil {
		return "data: {}\n\n"
	}
	return "data: " + string(data) + "\n\n"
}

func streamChunkBase(model, requestID string) map[string]any {
	return streamChunkBaseAt(model, requestID, time.Now().Unix())
}

func streamChunkBaseAt(model, requestID string, created int64) map[string]any {
	return map[string]any{
		"id":      "chatcmpl-" + requestID,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
	}
}

func newSSEWriter(w http.ResponseWriter, contentType string) *sseWriter {
	flusher, _ := w.(http.Flusher)
	sw := &sseWriter{
		w:           w,
		contentType: contentType,
	}
	if flusher != nil {
		sw.flush = flusher.Flush
	}
	return sw
}

func vertexErrorToOAI(e *vertex.VertexError) map[string]any {
	var errType string
	switch e.Kind {
	case "invalid":
		errType = "invalid_request_error"
	case "ratelimit":
		errType = "rate_limit_error"
	case "auth":
		errType = "server_error"
	case "notfound", "permission":
		errType = "invalid_request_error"
	default:
		errType = "server_error"
	}
	message := e.PublicText
	if message == "" {
		message = vertex.FriendlyErrorMessage(e)
	}
	code := any(e.Code)
	if e.StableCode != "" {
		code = e.StableCode
	}
	return map[string]any{"error": map[string]any{
		"message": message,
		"type":    errType,
		"param":   nilIfEmpty(e.Param),
		"code":    code,
	}}
}

func nilIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func withUpstreamDetail(friendly string, e *vertex.VertexError) string {
	detail := strings.TrimSpace(e.Message)
	if detail == "" {
		detail = strings.TrimSpace(e.UpstreamResponse)
	}
	if detail == "" || strings.Contains(friendly, detail) {
		return friendly
	}
	if r := []rune(detail); len(r) > 400 {
		detail = string(r[:400]) + "…"
	}
	return friendly + "（上游原因：" + detail + "）"
}

func toVertexError(err error) *vertex.VertexError {
	var ve *vertex.VertexError
	if errors.As(err, &ve) {
		return ve
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return vertex.NewContextError(err)
	}
	return vertex.NewInternalError(err.Error(), err)
}

func isSafetyBlock(e *vertex.VertexError) bool {
	if e == nil {
		return false
	}
	if e.Kind == "safety" {
		return true
	}
	msg := strings.ToLower(e.Message)
	status := strings.ToLower(e.Status)
	for _, k := range []string{"safety", "block_reason", "content_filter", "finish_reason_safety"} {
		if strings.Contains(msg, k) || strings.Contains(status, k) {
			return true
		}
	}
	return false
}

func oaiSafetyResponse(model string) map[string]any {
	return map[string]any{
		"id":      "chatcmpl-" + strutil.ReqID(),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": nil},
			"finish_reason": "content_filter",
		}},
	}
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
	return "sk-" + strutil.RandomHex(24)
}
