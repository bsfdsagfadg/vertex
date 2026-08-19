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

// CleanFinishReasonUnspecified 就地清洗 typed 候选的 FINISH_REASON_UNSPECIFIED
// （protobuf 默认值，不代表真实结束），返回首个真实 finishReason。
// 返回 "" 表示所有候选均为 UNSPECIFIED（流未结束）。
func CleanFinishReasonUnspecified(resp *GeminiResponse) string {
	if resp == nil {
		return ""
	}
	var realFR string
	for _, cand := range resp.Candidates {
		if cand == nil {
			continue
		}
		if cand.FinishReason == FinishReasonUnspecified {
			cand.FinishReason = ""
		} else if cand.FinishReason != "" && realFR == "" {
			realFR = cand.FinishReason
		}
	}
	return realFR
}

// SafetyFinishReasons 是 Gemini finishReason 维度的"输出被拦截"枚举集合。
// 覆盖：SAFETY（安全审查）、RECITATION（版权/抄袭）、PROHIBITED_CONTENT（违禁内容）、
// SPII（隐私信息）、BLOCKLIST（黑名单）、IMAGE_SAFETY（生图安全）。
// 注意：不含 OTHER（表示"其它原因完成"，非拦截，MapFinishReason 已将其映射为 stop）。
var SafetyFinishReasons = map[string]struct{}{ //nolint:gochecknoglobals
	"SAFETY":             {},
	"RECITATION":         {},
	"PROHIBITED_CONTENT": {},
	"SPII":               {},
	"BLOCKLIST":          {},
	"IMAGE_SAFETY":       {},
}

// IsSafetyFinishReason 判定 finishReason 是否为安全/内容拦截类结束原因。
// 语义：大写化 + 去首尾空白后查 SafetyFinishReasons；空串/未知值返回 false。
func IsSafetyFinishReason(fr string) bool {
	fr = strings.ToUpper(strings.TrimSpace(fr))
	_, ok := SafetyFinishReasons[fr]
	return ok
}

// IsBlockReason 判定 blockReason 是否表示"提示被拦截"。
// 语义：非空且非 BLOCKED_REASON_UNSPECIFIED 即视为拦截（BlockReason 枚举除 UNSPECIFIED
// 外全部表示拦截，含 SAFETY/PROHIBITED_CONTENT/BLOCKLIST/IMAGE_SAFETY/SPII/OTHER；
// 逐一枚举会漏枚举上游新增值，故采用排除法而非白名单）。
func IsBlockReason(br string) bool {
	br = strings.ToUpper(strings.TrimSpace(br))
	return br != "" && br != "BLOCKED_REASON_UNSPECIFIED"
}

// IsSafetyResponse 判定一个 Gemini 响应整体是否为安全/内容拦截响应：
// ① PromptFeedback.BlockReason 为拦截（IsBlockReason）；
// ② 任一候选 FinishReason 为拦截类（IsSafetyFinishReason）。
// 二者满足其一即为 true；resp 为 nil 返回 false。
func IsSafetyResponse(resp *GeminiResponse) bool {
	if resp == nil {
		return false
	}
	if resp.PromptFeedback != nil && IsBlockReason(resp.PromptFeedback.BlockReason) {
		return true
	}
	for _, cand := range resp.Candidates {
		if cand != nil && IsSafetyFinishReason(cand.FinishReason) {
			return true
		}
	}
	return false
}
