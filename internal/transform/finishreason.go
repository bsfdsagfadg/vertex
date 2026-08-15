package transform

import "strings"

const FinishReasonUnspecified = "FINISH_REASON_UNSPECIFIED"

// FinishReasonMap 记录 Gemini finishReason 到通用结束原因标识的映射。
var FinishReasonMap = map[string]string{ //nolint:gochecknoglobals
	"STOP":                    "stop",
	"MAX_TOKENS":              "length",
	"SAFETY":                  "content_filter",
	"RECITATION":              "content_filter",
	"PROHIBITED_CONTENT":      "content_filter",
	"TOOL_CALLS":              "tool_calls",
	"MALFORMED_FUNCTION_CALL": "tool_calls",
	"BLOCKLIST":               "content_filter",
	"SPII":                    "content_filter",
	"OTHER":                   "stop",
}

// MapFinishReason 将 Gemini finishReason 转换为通用结束原因字符串。
func MapFinishReason(finish string, hasToolCalls bool) string {
	if hasToolCalls {
		return "tool_calls"
	}
	if finish == "" {
		return "stop"
	}
	if v, ok := FinishReasonMap[strings.ToUpper(finish)]; ok {
		return v
	}
	return "stop"
}
