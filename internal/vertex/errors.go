// Package vertex 实现与 Google 匿名 batchGraphql 端点交互的核心请求循环。
package vertex

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/transform"
)

// gRPC 错误状态字符串。
const (
	StatusInvalidArgument   = "INVALID_ARGUMENT"
	StatusNotFound          = "NOT_FOUND"
	StatusPermissionDenied  = "PERMISSION_DENIED"
	StatusResourceExhausted = "RESOURCE_EXHAUSTED"
	StatusInternal          = "INTERNAL"
	StatusUnavailable       = "UNAVAILABLE"
	StatusUnauthenticated   = "UNAUTHENTICATED"
	StatusUnknown           = "UNKNOWN"
)

// VertexError 是统一错误类型，兼容 Gemini API 错误格式。
//
// Kind 用于区分语义（auth/ratelimit/invalid/...），便于 IsRetryable 判定与对外错误映射。
// 认证错误对外返回 502 而非 401：这是我方 recaptcha/token 的临时问题，返 401 会让上游
// 网关误判为“密钥失效”并自动禁用渠道，造成误杀；用 502 让网关当作可重试的服务端错误。
type VertexError struct { //nolint:govet
	Message          string
	Code             int
	Status           string
	Kind             string
	Param            string // 错误关联参数名，空值不输出
	Details          map[string]any
	UpstreamResponse string
	RetryAfter       int   // 仅 ratelimit 用，0 表示未设
	Truncated        bool  // 流式首帧已交付后断流标记：已向客户端输出内容，绝不重试
	cause            error // 供 errors.Is 穿透底层 cause；所有构造器通过参数或 WithCause 设置
}

// Error 实现 error 接口。
func (e *VertexError) Error() string { return e.Message }

// Unwrap 让 errors.Is(err, context.DeadlineExceeded) 能穿透 VertexError。
func (e *VertexError) Unwrap() error { return e.cause }

// WithCause 设置 cause 并返回自身，用于构造器链式保留原始错误。
func (e *VertexError) WithCause(cause error) *VertexError {
	e.cause = cause
	return e
}

// WithTruncated 标记流式首帧已交付后断流的错误（Committed 语义：绝不重试）。
func (e *VertexError) WithTruncated() *VertexError {
	e.Truncated = true
	return e
}

// IsRetryable 判定是否可重试：薄别名，语义收敛为 ClassifyBatch() == Transient。
// context 错误（cause 链含 Canceled/DeadlineExceeded）由 ClassifyBatch 首步判为
// Terminal（不可重试），与旧实现的防御性兜底语义一致。
func (e *VertexError) IsRetryable() bool {
	return e.ClassifyBatch() == Transient
}

// BatchDisposition 是批次级重试裁决的四态分类。
type BatchDisposition int

const (
	// Committed 批次已向客户端输出内容，最高优先级透传真实原因，绝不重试。
	Committed BatchDisposition = iota
	// Transient 该候选瞬时失败，整批可退避重试。
	Transient
	// FailFast 请求级硬错误，所有候选必败，首个即终止整批。
	FailFast
	// Terminal 不可重试，但不禁杀其他候选（其他候选独立尝试可能成功）。
	Terminal
)

// ClassifyBatch 按批次级口径分类错误（顺序敏感）：
//  1. context 错误（cause 链含 Canceled/DeadlineExceeded）→ Terminal（防御性，防止 NewContextError 被误判可重试）；
//  2. Truncated 标记 → Committed（最高优先级短路，绝不重试）；
//  3. IsGlobalHardError（invalid/notfound/safety/infra）→ FailFast；
//  4. Kind == "permission" → Terminal；
//  5. 瞬态条件（network/ratelimit/auth/408/429/5xx/empty）→ Transient；
//  6. 其余 → Terminal。
func (e *VertexError) ClassifyBatch() BatchDisposition {
	if errors.Is(e.cause, context.Canceled) || errors.Is(e.cause, context.DeadlineExceeded) {
		return Terminal
	}
	if e.Truncated {
		return Committed
	}
	if e.IsGlobalHardError() {
		return FailFast
	}
	if e.Kind == "permission" {
		return Terminal
	}
	switch {
	case e.Kind == "network", e.Kind == "ratelimit", e.Kind == "auth",
		e.Kind == "empty", isEmptyResponseError(e):
		return Transient
	}
	switch e.Code {
	case 408, 429, 500, 502, 503, 504:
		return Transient
	}
	return Terminal
}

// IsGlobalHardError 判定是否为请求级别的全局硬错误（所有节点去发都会必定失败，如参数错误/模型不存在/安全审查）。
// 只有此类错误才应触发竞速引擎的 Fail-Fast 终止整场竞速。
// 节点级异常（permission 403、auth 502、network、ratelimit 429）返回 false。
func (e *VertexError) IsGlobalHardError() bool {
	switch e.Kind {
	case "invalid", "notfound", "safety", "infra":
		return true
	}
	return false
}

// ---- 构造器（默认 code/status 对应各错误类）----

// NewAuthenticationError 认证错误（recaptcha/token 过期）。code=502（见类型注释）。
func NewAuthenticationError(msg string, cause error) *VertexError {
	return &VertexError{Message: msg, Code: 502, Status: StatusUnauthenticated, Kind: "auth", cause: cause} //nolint:exhaustruct
}

// NewRecaptchaUnavailableError reCAPTCHA token 子系统不可用（GetTokenShared 穷尽重试后仍失败）。
// Kind="infra" 归 FailFast：子系统整体挂了，所有候选都会在 rT 上失败，不浪费预算重试。
// 替代旧实现把 rT 耗尽错塞进 auth 触发节点禁用的现状（infra 不归因节点）。
func NewRecaptchaUnavailableError(msg string, cause error) *VertexError {
	return &VertexError{Message: msg, Code: 502, Status: StatusUnavailable, Kind: "infra", cause: cause} //nolint:exhaustruct
}

// NewPermissionDeniedError 权限拒绝（403）。
func NewPermissionDeniedError(msg string, cause error) *VertexError {
	return &VertexError{Message: msg, Code: 403, Status: StatusPermissionDenied, Kind: "permission", cause: cause} //nolint:exhaustruct
}

// NewInvalidArgumentError 参数错误（400）。
func NewInvalidArgumentError(msg string, cause error) *VertexError {
	return &VertexError{Message: msg, Code: 400, Status: StatusInvalidArgument, Kind: "invalid", cause: cause}
}

// NewInvalidParamError 带参数字段名的参数错误（400）。
func NewInvalidParamError(msg, param string, cause error) *VertexError {
	return &VertexError{Message: msg, Code: 400, Status: StatusInvalidArgument, Kind: "invalid", Param: param, cause: cause}
}

// NewNotFoundError 资源不存在（404）。
func NewNotFoundError(msg string, cause error) *VertexError {
	return &VertexError{Message: msg, Code: 404, Status: StatusNotFound, Kind: "notfound", cause: cause}
}

// NewRateLimitError 限流/资源耗尽（429）。
func NewRateLimitError(msg string, retryAfter int, cause error) *VertexError {
	return &VertexError{Message: msg, Code: 429, Status: StatusResourceExhausted, Kind: "ratelimit", RetryAfter: retryAfter, cause: cause}
}

// NewInternalError 内部错误（500）。
func NewInternalError(msg string, cause error) *VertexError {
	return &VertexError{Message: msg, Code: 500, Status: StatusInternal, Kind: "internal", cause: cause}
}

// NewContextError 包装 context.Canceled/DeadlineExceeded，保留 cause 以供 errors.Is 透传。
// 对外表现与 NewInternalError 一致（Code=500, Kind="internal"），仅多了 cause 链。
func NewContextError(err error) *VertexError {
	return &VertexError{Message: err.Error(), Code: 500, Status: StatusInternal, Kind: "internal", cause: err} //nolint:exhaustruct
}

// NewEmptyResponseError 上游空响应（502）。
func NewEmptyResponseError(msg string, cause error) *VertexError {
	return &VertexError{Message: msg, Code: 502, Status: StatusInternal, Kind: "network", cause: cause}
}

// NewNetworkError 包装网络抖动错误（连接超时、DNS 失败、TCP Reset 等）。
// Code=502 避免上游网关误判；Kind="network" 触发 IsRetryable 持续重试。
func NewNetworkError(err error) *VertexError {
	return &VertexError{Message: err.Error(), Code: 502, Status: StatusUnavailable, Kind: "network", cause: err}
}

// NewSafetyError 包装 Google 安全审查拦截响应。
// Code=400，Status 为安全相关状态（如 "SAFETY"），Kind="safety"。
func NewSafetyError(msg, status string, cause error) *VertexError {
	return &VertexError{Message: msg, Code: 400, Status: status, Kind: "safety", cause: cause}
}

// NewUnavailableError 服务不可用（503）。
func NewUnavailableError(msg string, cause error) *VertexError {
	return &VertexError{Message: msg, Code: 503, Status: StatusUnavailable, Kind: "unavailable", cause: cause}
}

// raiseForStatus 根据 HTTP/gRPC 状态创建对应错误。
func raiseForStatus(code int, status, message string, details map[string]any, upstream string) *VertexError {
	var e *VertexError
	switch {
	case status == StatusResourceExhausted || code == 8 || code == 429:
		e = NewRateLimitError(message, 0, nil)
	case status == StatusUnauthenticated || code == 16 || code == 401:
		e = NewAuthenticationError(message, nil)
	case status == StatusPermissionDenied || code == 7 || code == 403:
		e = NewPermissionDeniedError(message, nil)
	case status == StatusInvalidArgument || code == 3 || code == 400:
		e = NewInvalidArgumentError(message, nil)
	case status == StatusNotFound || code == 5 || code == 404:
		e = NewNotFoundError(message, nil)
	case status == StatusUnavailable || code == 14 || code == 503:
		e = NewUnavailableError(message, nil)
	case code >= 400 && code < 500:
		e = &VertexError{Message: message, Code: code, Status: status, Kind: "client"}
	default:
		c := code
		if c == 0 {
			c = 500
		}
		e = &VertexError{Message: message, Code: c, Status: status, Kind: "server"}
	}
	if details != nil {
		e.Details = details
	}
	if upstream != "" {
		e.UpstreamResponse = upstream
	}
	return e
}

// parseErrorResponse 从上游响应中解析错误（支持 string/数组/对象 三态、gRPC 风格）。
// 解析上游错误响应，无错误返回 nil。
func parseErrorResponse(data any) *VertexError {
	switch v := data.(type) {
	case string:
		v = strings.TrimSpace(v)
		if v == "" {
			return nil
		}
		var parsed any
		if err := json.Unmarshal([]byte(v), &parsed); err != nil {
			// 上游 HTML/纯文本错误（如 Cloudflare 502/504 网关页）非 JSON。
			// 兜底按 502 server/network 解析，避免静默返回 nil 导致下游误判为空响应。
			e := raiseForStatus(502, "", "Upstream non-JSON response: "+truncateStr(v, 200), nil, v)
			return e
		}
		return parseErrorResponse(parsed)
	case []any:
		for _, item := range v {
			if e := parseErrorResponse(item); e != nil {
				return e
			}
		}
		return nil
	case map[string]any:
		// 1. 嵌套 error 字段（标准 Google API）
		if errObj, ok := v["error"].(map[string]any); ok {
			// 安全审查拦截：error.message 为拦截类 finishReason（SAFETY/RECITATION/PROHIBITED_CONTENT 等）
			if msg, _ := errObj["message"].(string); transform.IsSafetyFinishReason(msg) {
				return NewSafetyError(msg, strings.ToUpper(strings.TrimSpace(msg)), nil)
			}
			return raiseForStatus(
				toInt(errObj["code"], 500), toStr(errObj["status"]),
				toStrOr(errObj["message"], "Unknown error"), toMap(errObj["details"]), marshalStr(v),
			)
		}
		// 2. GraphQL 风格 errors 数组
		if errs, ok := v["errors"].([]any); ok && len(errs) > 0 {
			if first, ok := errs[0].(map[string]any); ok {
				ext := toMap(first["extensions"])
				extStatus := toMap(ext["status"])
				code := toInt(firstNonNil(extStatus["code"], first["code"]), 500)
				status := toStr(firstNonNil(extStatus["status"], first["status"]))
				message := toStrOr(firstNonNil(extStatus["message"], first["message"]), "Unknown error")
				// 安全审查拦截：finishReason 为拦截类（SAFETY/RECITATION/PROHIBITED_CONTENT 等），
				// Status 携带真实 finishReason（保证"Status 恒为拦截原因"约定一致）。
				if fr := toStr(firstNonNil(extStatus["finishReason"], first["finishReason"])); transform.IsSafetyFinishReason(fr) {
					return NewSafetyError(message, strings.ToUpper(strings.TrimSpace(fr)), nil)
				}
				return raiseForStatus(code, status, message, toMap(first["details"]), marshalStr(v))
			}
		}
		// 3. 扁平格式
		// 安全审查拦截：finishReason、blockReason、promptFeedback.blockReason
		if fr := toStr(v["finishReason"]); transform.IsSafetyFinishReason(fr) {
			return NewSafetyError(toStrOr(v["message"], "Blocked by safety"), strings.ToUpper(fr), nil)
		}
		// blockReason 采用排除法：非空且非 BLOCKED_REASON_UNSPECIFIED 即拦截（IMAGE_SAFETY/OTHER 等均命中）。
		if br := toStr(v["blockReason"]); transform.IsBlockReason(br) {
			return NewSafetyError(toStrOr(v["message"], "Blocked by safety"), strings.ToUpper(br), nil)
		}
		if pf, ok := v["promptFeedback"].(map[string]any); ok {
			if br := toStr(pf["blockReason"]); transform.IsBlockReason(br) {
				return NewSafetyError(toStrOr(pf["blockReasonMessage"], "Blocked by safety"), strings.ToUpper(br), nil)
			}
		}
		if _, hasCode := v["code"]; hasCode {
			return raiseForStatus(toInt(v["code"], 500), toStr(v["status"]), toStrOr(v["message"], "Unknown error"), toMap(v["details"]), marshalStr(v))
		}
		if _, hasStatus := v["status"]; hasStatus {
			return raiseForStatus(toInt(v["code"], 500), toStr(v["status"]), toStrOr(v["message"], "Unknown error"), toMap(v["details"]), marshalStr(v))
		}
		if _, hasMsg := v["message"]; hasMsg {
			msg := toStr(v["message"])
			if transform.IsSafetyFinishReason(msg) {
				return NewSafetyError(msg, strings.ToUpper(strings.TrimSpace(msg)), nil)
			}
			return raiseForStatus(toInt(v["code"], 500), toStr(v["status"]), msg, toMap(v["details"]), marshalStr(v))
		}
		return nil
	default:
		return nil
	}
}

// FriendlyErrorMessage 将 VertexError 转为用户友好的中英混合提示。
func FriendlyErrorMessage(e *VertexError) string {
	msg := friendlyErrorMessageBase(e)
	if e.Truncated {
		msg += "（内容已截断）"
	}
	return msg
}

func friendlyErrorMessageBase(e *VertexError) string {
	switch {
	case e.Kind == "ratelimit" || e.Code == 429:
		return "服务器繁忙，请求过于频繁，请稍后重试 (rate limited)"
	case e.Kind == "auth" || e.Code == 401:
		return "认证失败，recaptcha 验证未通过，请稍后再试 (auth failed)"
	case e.Kind == "permission" || e.Code == 403:
		return "权限不足，访问被拒绝 (permission denied)"
	case e.Kind == "notfound" || e.Code == 404:
		return "模型不存在，请检查模型名称是否正确 (not found)"
	case e.Kind == "invalid" || e.Code == 400:
		if strings.Contains(strings.ToLower(e.Message), "json") {
			return "请求格式错误，JSON 解析失败 (invalid JSON)"
		}
		return "请求参数有误，请检查请求内容 (invalid argument)"
	case e.Kind == "unavailable" || e.Code == 503:
		return "服务暂时不可用，请稍后再试 (service unavailable)"
	case e.Code == 502:
		return "上游服务异常，请稍后重试 (upstream error)"
	case e.Kind == "server" || e.Kind == "internal" || e.Code >= 500:
		return "服务内部错误，请联系管理员 (internal error)"
	}
	return "请求失败: " + e.Message
}

// ---- 小工具 ----

func toInt(v any, def int) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case string:
		// 偶有字符串码，尽力转
		if x, err := strconv.Atoi(n); err == nil {
			return x
		}
	}
	return def
}

func toStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func toStrOr(v any, def string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return def
}

// toMap 提取上游 details：google.rpc.Status 规范中 details 为 Any 数组（单元素为原始
// 对象），同时兼容裸 map 形态。
func toMap(v any) map[string]any {
	switch t := v.(type) {
	case map[string]any:
		return t
	case []any:
		if len(t) > 0 {
			if m, ok := t[0].(map[string]any); ok {
				return m
			}
		}
	}
	return nil
}

func firstNonNil(vals ...any) any {
	for _, v := range vals {
		if v != nil {
			return v
		}
	}
	return nil
}

func marshalStr(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
