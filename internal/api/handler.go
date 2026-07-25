package api

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/cli"
	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/jsonx"
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

// coreGenerate 统一非流式调用：剥离 fake 前缀、解包 generateContentRequest、
// 更新模型追踪、调用 CompleteChat、错误转换、清洗 finishReason。
func (h *handler) coreGenerate(ctx context.Context, rawModel string, payload map[string]any) (map[string]any, *vertex.VertexError) {
	actualModel, _ := stripFakePrefix(rawModel, h.cfg.FakePrefixes())
	if reqObj, ok := payload["generateContentRequest"].(map[string]any); ok {
		payload = reqObj
	}
	cli.UpdateReqModel(vertex.RequestIDFromContext(ctx), actualModel)
	resp, err := h.vc.CompleteChat(ctx, actualModel, payload)
	if err != nil {
		return nil, toVertexError(err)
	}
	cleanGeminiFinishReason(resp)
	return resp, nil
}

// coreStreamGenerate 统一真流式调用：剥离 fake 前缀、解包 generateContentRequest、
// 更新模型追踪、调用 StreamChat，回调中自动清洗 finishReason。
func (h *handler) coreStreamGenerate(ctx context.Context, rawModel string, payload map[string]any, onChunk StreamCallback) {
	actualModel, _ := stripFakePrefix(rawModel, h.cfg.FakePrefixes())
	if reqObj, ok := payload["generateContentRequest"].(map[string]any); ok {
		payload = reqObj
	}
	cli.UpdateReqModel(vertex.RequestIDFromContext(ctx), actualModel)
	h.vc.StreamChat(ctx, actualModel, payload, func(ch vertex.StreamChunk) bool {
		if ch.Err != nil {
			return onChunk(nil, ch.Err)
		}
		cleanGeminiFinishReason(ch.Data)
		return onChunk(ch.Data, nil)
	})
}

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
	return map[string]any{
		"id":      "chatcmpl-" + requestID,
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
	}
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
	return map[string]any{"error": map[string]any{
		"message": withUpstreamDetail(vertex.FriendlyErrorMessage(e), e),
		"type":    errType,
		"code":    e.Code,
	}}
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
	return vertex.NewInternalError(err.Error(), nil)
}

func isSafetyBlock(e *vertex.VertexError) bool {
	if e == nil {
		return false
	}
	if e.Kind == "safety" {
		return true
	}
	status := strings.ToUpper(e.Status)
	return status == "SAFETY" || status == "BLOCKED_REASON_SAFETY"
}

func oaiSafetyResponse(model string) map[string]any {
	return map[string]any{
		"id":      "chatcmpl-" + reqID24(),
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

func randomDigits(n int) string {
	const digits = "0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = digits[rand.Intn(len(digits))]
	}
	return string(b)
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
