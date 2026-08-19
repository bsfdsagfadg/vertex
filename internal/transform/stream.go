package transform

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/jsonx"
	"github.com/bsfdsagfadg/vertex/internal/strutil"
)

// FinishReasonUnspecified 是匿名端点每帧携带的 protobuf 默认值。
const FinishReasonUnspecified = "FINISH_REASON_UNSPECIFIED"



// FunctionCallIDAssigner supplies IDs in response order. Reuse one assigner
// for all chunks in a stream so separate tool-call frames remain distinct.
type FunctionCallIDAssigner struct {
	next int
}

func NewFunctionCallIDAssigner() *FunctionCallIDAssigner {
	return &FunctionCallIDAssigner{}
}

func (a *FunctionCallIDAssigner) Ensure(value any) {
	if a == nil {
		return
	}
	ensureFunctionCallIDs(value, &a.next)
}

// EnsureFunctionCallIDs supplies the call ID required by native Gemini
// clients when an upstream non-streaming functionCall omits it.
func EnsureFunctionCallIDs(value any) {
	NewFunctionCallIDAssigner().Ensure(value)
}

func ensureFunctionCallIDs(value any, next *int) {
	switch v := value.(type) {
	case map[string]any:
		if fc, ok := v["functionCall"].(map[string]any); ok {
			if strings.TrimSpace(toString(fc["name"])) != "" && strings.TrimSpace(toString(firstNonEmpty(fc["id"], fc["call_id"], fc["toolCallId"]))) == "" {
				(*next)++
				fc["id"] = fmt.Sprintf("tool_call_%d", *next)
			}
		}
		for _, child := range v {
			ensureFunctionCallIDs(child, next)
		}
	case []any:
		for _, child := range v {
			ensureFunctionCallIDs(child, next)
		}
	}
}

type streamToolCallEntry struct {
	index  int
	callID string
}

// StreamToolCallTracker 保存一次 OpenAI SSE 请求内的工具调用编号。
type StreamToolCallTracker struct {
	entries   map[string]streamToolCallEntry
	nextIndex map[int]int
	hasCalls  map[int]bool
}

func NewStreamToolCallTracker() *StreamToolCallTracker {
	return &StreamToolCallTracker{
		entries:   map[string]streamToolCallEntry{},
		nextIndex: map[int]int{},
		hasCalls:  map[int]bool{},
	}
}

func (t *StreamToolCallTracker) process(candidateIndex int, ordinal int, functionCall map[string]any) (int, string) {
	if t == nil {
		return ordinal, "call_" + reqID()
	}
	name := toString(functionCall["name"])
	explicitID := toString(firstNonEmpty(functionCall["id"], functionCall["call_id"], functionCall["toolCallId"]))
	key := "candidate:" + strconv.Itoa(candidateIndex) + ":part:" + strconv.Itoa(ordinal) + ":name:" + name
	if explicitID != "" {
		key = "candidate:" + strconv.Itoa(candidateIndex) + ":id:" + explicitID
	}
	t.hasCalls[candidateIndex] = true
	if entry, ok := t.entries[key]; ok {
		return entry.index, entry.callID
	}
	index := t.nextIndex[candidateIndex]
	t.nextIndex[candidateIndex] = index + 1
	callID := explicitID
	if callID == "" {
		callID = "call_" + reqID()
	}
	t.entries[key] = streamToolCallEntry{index: index, callID: callID}
	return index, callID
}

// reqID 生成唯一 ID。
func reqID() string {
	return strutil.ReqID()
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
func ConvertRealtimeChunk(chunk map[string]any, model, requestID string, isFirst bool, trackers ...*StreamToolCallTracker) []string {
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
	var tracker *StreamToolCallTracker
	if len(trackers) > 0 {
		tracker = trackers[0]
	}
	candidates := responseCandidates(chunk)

	if isFirst {
		b := base()
		roleChoices := make([]any, 0, len(candidates))
		for position, candidate := range candidates {
			roleChoices = append(roleChoices, map[string]any{"index": candidateResponseIndex(candidate, position), "delta": map[string]any{"role": "assistant"}, "finish_reason": nil})
		}
		if len(roleChoices) == 0 {
			roleChoices = append(roleChoices, map[string]any{"index": 0, "delta": map[string]any{"role": "assistant"}, "finish_reason": nil})
		}
		b["choices"] = roleChoices
		events = append(events, sseLine(b))
	}

	finishChoices := make([]any, 0, len(candidates))
	for position, candidate := range candidates {
		candidateIndex := candidateResponseIndex(candidate, position)
		parts := candidateParts(candidate)
		text, toolCalls, reasoning := extractPartsTracked(parts, true, tracker, candidateIndex)
		if reasoning != "" {
			b := base()
			b["choices"] = []any{map[string]any{"index": candidateIndex, "delta": map[string]any{"reasoning_content": reasoning}, "finish_reason": nil}}
			events = append(events, sseLine(b))
		}
		if text != "" {
			b := base()
			b["choices"] = []any{map[string]any{"index": candidateIndex, "delta": map[string]any{"content": text}, "finish_reason": nil}}
			events = append(events, sseLine(b))
		}
		if len(toolCalls) > 0 {
			b := base()
			b["choices"] = []any{map[string]any{"index": candidateIndex, "delta": map[string]any{"tool_calls": toolCalls}, "finish_reason": nil}}
			events = append(events, sseLine(b))
		}
		finish, _ := candidate["finishReason"].(string)
		if finish != "" && finish != FinishReasonUnspecified {
			hasToolCalls := len(toolCalls) > 0 || (tracker != nil && tracker.hasCalls[candidateIndex])
			finishChoices = append(finishChoices, map[string]any{
				"index": candidateIndex, "delta": map[string]any{}, "finish_reason": MapFinishReason(finish, hasToolCalls),
			})
		}
	}

	usageMeta, hasUsage := chunk["usageMetadata"].(map[string]any)
	if len(finishChoices) > 0 {
		finishEvent := base()
		finishEvent["choices"] = finishChoices
		if hasUsage && len(usageMeta) > 0 {
			// Track 1: Include usage directly in the finish frame for legacy aggregators
			finishEvent["usage"] = ConvertUsage(usageMeta)
		}
		events = append(events, sseLine(finishEvent))
	}
	if hasUsage && len(usageMeta) > 0 {
		// Track 2: Dedicated terminal usage chunk with empty choices for standard OpenAI SDKs / Cherry Studio
		usageEvent := base()
		usageEvent["choices"] = []any{}
		usageEvent["usage"] = ConvertUsage(usageMeta)
		events = append(events, sseLine(usageEvent))
	}
	return events
}

// ExtractParts 从 Gemini parts 提取 (text_content, tool_calls, reasoning_content)。
func ExtractParts(parts []any, forStream bool) (string, []any, string) {
	return extractPartsTracked(parts, forStream, nil, 0)
}

func extractPartsTracked(parts []any, forStream bool, tracker *StreamToolCallTracker, candidateIndex int) (string, []any, string) {
	var texts []string
	var thoughts []string
	var toolCalls []any
	var images []string

	toolOrdinal := 0
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
			index, callID := toolOrdinal, "call_"+reqID()
			if explicitID := toString(firstNonEmpty(fc["id"], fc["call_id"], fc["toolCallId"])); explicitID != "" {
				callID = explicitID
			}
			if tracker != nil {
				index, callID = tracker.process(candidateIndex, toolOrdinal, fc)
			}
			toolOrdinal++
			tc := map[string]any{
				"index": index,
				"id":    callID,
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

func candidateResponseIndex(candidate map[string]any, fallback int) int {
	value, ok := candidate["index"]
	if !ok {
		return fallback
	}
	switch index := value.(type) {
	case int:
		return index
	case int64:
		return int(index)
	case float64:
		return int(index)
	case string:
		if parsed, err := strconv.Atoi(index); err == nil {
			return parsed
		}
	}
	return fallback
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
