package transform

import (
	cryptorand "crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/jsonx"
)

// FinishReasonUnspecified 是匿名端点每帧携带的 protobuf 默认值。
const FinishReasonUnspecified = "FINISH_REASON_UNSPECIFIED"

var (
	streamReqIDFallback atomic.Bool
	streamReqIDCounter  atomic.Uint64
)

// reqID 生成 24 位十六进制 ID。
func reqID() string {
	if !streamReqIDFallback.Load() {
		b := make([]byte, 12)
		if _, err := cryptorand.Read(b); err == nil {
			return hex.EncodeToString(b)
		}
		streamReqIDFallback.Store(true)
	}
	return fmt.Sprintf("%016x%08x", uint64(time.Now().UnixNano()), streamReqIDCounter.Add(1))
}

// sseLine 把对象序列化成一条 SSE 数据行。
func sseLine(obj map[string]any) string {
	data, err := jsonx.Marshal(obj)
	if err != nil {
		return "data: {}\n\n"
	}
	return "data: " + string(data) + "\n\n"
}

// ConvertRealtimeChunk 把单个 Gemini 增量 dict 转为 OAI SSE 事件字符串列表。
func ConvertRealtimeChunk(chunk map[string]any, model, requestID string, isFirst bool) []string {
	candidate := firstCandidate(chunk)
	parts := candidateParts(candidate)
	finish, _ := candidate["finishReason"].(string)

	created := time.Now().Unix()
	base := func() map[string]any {
		return map[string]any{
			"id":      "chatcmpl-" + requestID,
			"object":  "chat.completion.chunk",
			"created": created,
			"model":   model,
		}
	}
	var events []string

	if isFirst {
		b := base()
		b["choices"] = []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant"}, "finish_reason": nil}}
		events = append(events, sseLine(b))
	}

	text, toolCalls, reasoning := ExtractParts(parts, true)

	if reasoning != "" {
		b := base()
		b["choices"] = []any{map[string]any{"index": 0, "delta": map[string]any{"reasoning_content": reasoning}, "finish_reason": nil}}
		events = append(events, sseLine(b))
	}
	if text != "" {
		b := base()
		b["choices"] = []any{map[string]any{"index": 0, "delta": map[string]any{"content": text}, "finish_reason": nil}}
		events = append(events, sseLine(b))
	}
	if len(toolCalls) > 0 {
		b := base()
		b["choices"] = []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": toolCalls}, "finish_reason": nil}}
		events = append(events, sseLine(b))
	}

	if finish != "" && finish != FinishReasonUnspecified {
		oaiFinish := MapFinishReason(finish, len(toolCalls) > 0)
		finishEvt := base()
		choice := map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": oaiFinish}
		finishEvt["choices"] = []any{choice}
		if usageMeta, ok := chunk["usageMetadata"].(map[string]any); ok && len(usageMeta) > 0 {
			finishEvt["usage"] = ConvertUsage(usageMeta)
		}
		events = append(events, sseLine(finishEvt))
	}

	return events
}

// ExtractParts 从 Gemini parts 提取 (text_content, tool_calls, reasoning_content)。
func ExtractParts(parts []any, forStream bool) (string, []any, string) {
	var texts []string
	var thoughts []string
	var toolCalls []any
	var images []string

	for _, pRaw := range parts {
		part, ok := pRaw.(map[string]any)
		if !ok {
			continue
		}
		hasText := toString(part["text"]) != ""
		isThought := isTruthy(part["thought"])

		switch {
		case isFunctionCallWithName(part):
			fc, _ := part["functionCall"].(map[string]any)
			args := fc["args"]
			if args == nil {
				args = map[string]any{}
			}
			argBytes, _ := jsonx.Marshal(args)
			tc := map[string]any{
				"index": len(toolCalls),
				"id":    "call_" + reqID(),
				"type":  "function",
				"function": map[string]any{
					"name":      toString(fc["name"]),
					"arguments": string(argBytes),
				},
			}
			if !forStream {
				delete(tc, "index")
			}
			toolCalls = append(toolCalls, tc)
		case hasInlineImage(part):
			id, _ := part["inlineData"].(map[string]any)
			mime := toString(firstNonEmpty(id["mimeType"], id["mime_type"]))
			data := toString(id["data"])
			images = append(images, "\n![image](data:"+mime+";base64,"+data+")")
		case isThought && hasText:
			thoughts = append(thoughts, toString(part["text"]))
		case hasText:
			texts = append(texts, toString(part["text"]))
		case hasKey(part, "executableCode"):
			if ec, ok := part["executableCode"].(map[string]any); ok {
				lang := strings.ToLower(toString(ec["codeLanguage"]))
				texts = append(texts, "```"+lang+"\n"+toString(ec["code"])+"\n```")
			}
		case hasKey(part, "codeExecutionResult"):
			if cer, ok := part["codeExecutionResult"].(map[string]any); ok {
				texts = append(texts, "```output\n"+toString(cer["output"])+"\n```")
			}
		}
	}

	textContent := strings.Join(texts, "") + strings.Join(images, "")
	reasoning := strings.Join(thoughts, "")
	if len(toolCalls) == 0 {
		return textContent, nil, reasoning
	}
	return textContent, toolCalls, reasoning
}

// ---- 响应解析用的小工具 ----

func firstCandidate(resp map[string]any) map[string]any {
	if cands, ok := resp["candidates"].([]any); ok && len(cands) > 0 {
		if c, ok := cands[0].(map[string]any); ok {
			return c
		}
	}
	return map[string]any{}
}

func candidateParts(candidate map[string]any) []any {
	if content, ok := candidate["content"].(map[string]any); ok {
		if parts, ok := content["parts"].([]any); ok {
			return parts
		}
	}
	return nil
}

func isFunctionCallWithName(part map[string]any) bool {
	if fc, ok := part["functionCall"].(map[string]any); ok {
		return truthyStr(fc["name"])
	}
	return false
}

func hasInlineImage(part map[string]any) bool {
	if id, ok := part["inlineData"].(map[string]any); ok {
		mime := toString(firstNonEmpty(id["mimeType"], id["mime_type"]))
		data := toString(id["data"])
		return mime != "" && data != "" && strings.HasPrefix(mime, "image/")
	}
	return false
}

func hasKey(m map[string]any, k string) bool {
	_, ok := m[k]
	return ok
}

func firstNonEmpty(vals ...any) any {
	for _, v := range vals {
		if v != nil && toString(v) != "" {
			return v
		}
	}
	return ""
}

// numOf 把任意 JSON 数字（float64/int）转 int，非数字返回 0。
func numOf(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	default:
		return 0
	}
}
