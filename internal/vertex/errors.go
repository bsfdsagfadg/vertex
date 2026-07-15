// Package vertex 实现与 Google 匿名 batchGraphql 端点交互的核心请求循环。
package vertex

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
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
	Details          map[string]any
	UpstreamResponse string
	RetryAfter       int   // 仅 ratelimit 用，0 表示未设
	cause            error // 仅 NewContextError 设置；供 errors.Is 穿透 context 错误
}

// Error 实现 error 接口。
func (e *VertexError) Error() string { return e.Message }

// Unwrap 让 errors.Is(err, context.DeadlineExceeded) 能穿透 VertexError。
func (e *VertexError) Unwrap() error { return e.cause }

// IsRetryable 判定是否可重试：408/429/5xx 可重试；认证错误（按 Kind 判，不看 code）也可重试。
// context 错误（cause 链含 Canceled/DeadlineExceeded）不可重试——这是防御性兜底；
// 正常路径中这些错误在 stream.go 的 retry 循环和 RunRace 的 Fix 3 中被提前拦截。
func (e *VertexError) IsRetryable() bool {
	if errors.Is(e.cause, context.Canceled) || errors.Is(e.cause, context.DeadlineExceeded) {
		return false
	}
	switch e.Code {
	case 408, 429, 500, 502, 503, 504:
		return true
	}
	return e.Kind == "auth"
}

// ---- 构造器（默认 code/status 对应各错误类）----

// NewAuthenticationError 认证错误（recaptcha/token 过期）。code=502（见类型注释）。
func NewAuthenticationError(msg string) *VertexError {
	return &VertexError{Message: msg, Code: 502, Status: StatusUnauthenticated, Kind: "auth"} //nolint:exhaustruct
}

// NewPermissionDeniedError 权限拒绝（403）。
func NewPermissionDeniedError(msg string) *VertexError {
	return &VertexError{Message: msg, Code: 403, Status: StatusPermissionDenied, Kind: "permission"} //nolint:exhaustruct
}

// NewInvalidArgumentError 参数错误（400）。
func NewInvalidArgumentError(msg string) *VertexError {
	return &VertexError{Message: msg, Code: 400, Status: StatusInvalidArgument, Kind: "invalid"}
}

// NewNotFoundError 资源不存在（404）。
func NewNotFoundError(msg string) *VertexError {
	return &VertexError{Message: msg, Code: 404, Status: StatusNotFound, Kind: "notfound"}
}

// NewRateLimitError 限流/资源耗尽（429）。
func NewRateLimitError(msg string, retryAfter int) *VertexError {
	return &VertexError{Message: msg, Code: 429, Status: StatusResourceExhausted, Kind: "ratelimit", RetryAfter: retryAfter}
}

// NewInternalError 内部错误（500）。
func NewInternalError(msg string) *VertexError {
	return &VertexError{Message: msg, Code: 500, Status: StatusInternal, Kind: "internal"}
}

// NewContextError 包装 context.Canceled/DeadlineExceeded，保留 cause 以供 errors.Is 透传。
// 对外表现与 NewInternalError 一致（Code=500, Kind="internal"），仅多了 cause 链。
func NewContextError(err error) *VertexError {
	return &VertexError{Message: err.Error(), Code: 500, Status: StatusInternal, Kind: "internal", cause: err} //nolint:exhaustruct
}

// NewEmptyResponseError 上游空响应（502）。
func NewEmptyResponseError(msg string) *VertexError {
	return &VertexError{Message: msg, Code: 502, Status: StatusInternal, Kind: "empty"}
}

// NewUnavailableError 服务不可用（503）。
func NewUnavailableError(msg string) *VertexError {
	return &VertexError{Message: msg, Code: 503, Status: StatusUnavailable, Kind: "unavailable"}
}

// raiseForStatus 根据 HTTP/gRPC 状态创建对应错误。
func raiseForStatus(code int, status, message string, details map[string]any, upstream string) *VertexError {
	var e *VertexError
	switch {
	case status == StatusResourceExhausted || code == 8 || code == 429:
		e = NewRateLimitError(message, 0)
	case status == StatusUnauthenticated || code == 16 || code == 401:
		e = NewAuthenticationError(message)
	case status == StatusPermissionDenied || code == 7 || code == 403:
		e = NewPermissionDeniedError(message)
	case status == StatusInvalidArgument || code == 3 || code == 400:
		e = NewInvalidArgumentError(message)
	case status == StatusNotFound || code == 5 || code == 404:
		e = NewNotFoundError(message)
	case status == StatusUnavailable || code == 14 || code == 503:
		e = NewUnavailableError(message)
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
		var parsed any
		if err := json.Unmarshal([]byte(v), &parsed); err != nil {
			return nil
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
				return raiseForStatus(code, status, message, toMap(first["details"]), marshalStr(v))
			}
		}
		// 3. 扁平格式
		if _, hasCode := v["code"]; hasCode {
			return raiseForStatus(toInt(v["code"], 500), toStr(v["status"]), toStrOr(v["message"], "Unknown error"), toMap(v["details"]), marshalStr(v))
		}
		if _, hasStatus := v["status"]; hasStatus {
			return raiseForStatus(toInt(v["code"], 500), toStr(v["status"]), toStrOr(v["message"], "Unknown error"), toMap(v["details"]), marshalStr(v))
		}
		if _, hasMsg := v["message"]; hasMsg {
			return raiseForStatus(toInt(v["code"], 500), toStr(v["status"]), toStrOr(v["message"], "Unknown error"), toMap(v["details"]), marshalStr(v))
		}
		return nil
	default:
		return nil
	}
}

// FriendlyErrorMessage 将 VertexError 转为用户友好的中英混合提示。
func FriendlyErrorMessage(e *VertexError) string {
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
	case e.Kind == "empty":
		return "上游返回空响应，请重试 (empty response)"
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

func toMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
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
