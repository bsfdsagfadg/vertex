package api

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/engine/transform"
	"github.com/bsfdsagfadg/vertex/internal/engine/vertex"
	"github.com/bsfdsagfadg/vertex/internal/infra/config"
	"github.com/bsfdsagfadg/vertex/internal/infra/jsonx"
	"github.com/bsfdsagfadg/vertex/internal/infra/transport"
	"github.com/bsfdsagfadg/vertex/internal/node/entrypool"
	"github.com/bsfdsagfadg/vertex/internal/node/exitpool"
)

// ImportParser 是 admin 导入入口对多格式导入引擎的消费契约（实现 *importer.Service）。
type ImportParser interface {
	Parse(text string) []exitpool.Node
}

// TokenVerifier 是 admin 节点测速对 reCAPTCHA 会话校验的消费契约
// （实现 *recaptcha.TokenPool，复用其网络客户端装配）。
type TokenVerifier interface {
	FetchTokenWithSession(ctx context.Context, sess *transport.Session) (string, error)
}

// ServerDeps 是接入层的显式装配清单：admin 与 Gemini 管道的全部跨域成品实例逐项注入，
// 消除 api 对 nodes/entrynodes/importer/recaptcha 包级状态的装配知识。
type ServerDeps struct {
	VC      *vertex.VertexAIClient  // Gemini 三管道上游客户端
	Keys    *APIKeyManager          // API Key 管理
	Cfg     config.ConfigProvider   // 配置消费口
	Exit    *exitpool.Manager       // 出口节点池（admin CRUD/测速/去重）
	Entry   *entrypool.EntryManager // 前置代理节点池（admin 管理）
	Dialer  transport.ProxyDialer   // admin 前置连通性测试用拨号器
	Imports ImportParser            // 多格式节点导入引擎
	Tokens  TokenVerifier           // reCAPTCHA 会话校验（节点测速路径）
	IR      *transport.IRCache      // IR 解析缓存（admin 能力标注与导入预热）
}

type handler struct {
	vc   *vertex.VertexAIClient
	keys *APIKeyManager
	cfg  config.ConfigProvider
	deps ServerDeps
}

func (h *handler) dialer() transport.ProxyDialer {
	return h.deps.Dialer
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
