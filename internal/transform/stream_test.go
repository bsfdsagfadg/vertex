package transform

import (
	"encoding/json"
	"strings"
	"testing"
)

// 真流式增量转换：首帧带 role delta，内容帧带 content delta，UNSPECIFIED 不发 finish。
func TestConvertRealtimeChunk_FirstAndContent(t *testing.T) {
	chunk := map[string]any{"candidates": []any{
		map[string]any{
			"content":      map[string]any{"parts": []any{map[string]any{"text": "Hi"}}, "role": "model"},
			"finishReason": "FINISH_REASON_UNSPECIFIED",
		},
	}}
	events := ConvertRealtimeChunk(chunk, "gemini-3.1-flash", "req123", true)

	// 期望：role 事件 + content 事件，无 finish 事件（UNSPECIFIED 被过滤）。
	if len(events) != 2 {
		t.Fatalf("events=%d, want 2\n%v", len(events), events)
	}
	if !strings.Contains(events[0], `"role":"assistant"`) {
		t.Errorf("首帧应含 role delta: %s", events[0])
	}
	if !strings.Contains(events[1], `"content":"Hi"`) {
		t.Errorf("内容帧应含 content: %s", events[1])
	}
	for _, e := range events {
		if strings.Contains(e, `"finish_reason":"stop"`) || strings.Contains(e, `"finish_reason":"length"`) {
			t.Errorf("🔴 UNSPECIFIED 绝不能发真实 finish_reason（截断血泪教训）: %s", e)
		}
	}
}

func TestConvertRealtimeChunkPreservesMultipleCandidates(t *testing.T) {
	chunk := map[string]any{"candidates": []any{
		map[string]any{"index": 0, "content": map[string]any{"parts": []any{map[string]any{"text": "first"}}}, "finishReason": "STOP"},
		map[string]any{"index": 1, "content": map[string]any{"parts": []any{map[string]any{"text": "second"}}}, "finishReason": "MAX_TOKENS"},
	}}

	events := ConvertRealtimeChunk(chunk, "m", "r", true, NewStreamToolCallTracker())
	joined := strings.Join(events, "")
	for _, want := range []string{`"index":0`, `"index":1`, `"content":"first"`, `"content":"second"`, `"finish_reason":"stop"`, `"finish_reason":"length"`} {
		if !strings.Contains(joined, want) {
			t.Fatalf("events missing %s: %s", want, joined)
		}
	}
}

func TestConvertRealtimeChunkEmitsUsageOnlyChunk(t *testing.T) {
	events := ConvertRealtimeChunk(map[string]any{
		"usageMetadata": map[string]any{"promptTokenCount": float64(2), "candidatesTokenCount": float64(3), "totalTokenCount": float64(5)},
	}, "m", "r", false)
	if len(events) != 1 || !strings.Contains(events[0], `"choices":[]`) || !strings.Contains(events[0], `"total_tokens":5`) {
		t.Fatalf("usage-only events=%#v", events)
	}
}

func TestStreamToolCallTrackerSeparatesSameNameAndStaysStable(t *testing.T) {
	chunk := map[string]any{"candidates": []any{map[string]any{
		"index": 0,
		"content": map[string]any{"parts": []any{
			map[string]any{"functionCall": map[string]any{"name": "lookup", "args": map[string]any{"city": "A"}}},
			map[string]any{"functionCall": map[string]any{"name": "lookup", "args": map[string]any{"city": "B"}}},
		}},
	}}}
	tracker := NewStreamToolCallTracker()
	first := toolCallsFromEvents(t, ConvertRealtimeChunk(chunk, "m", "r", false, tracker))
	second := toolCallsFromEvents(t, ConvertRealtimeChunk(chunk, "m", "r", false, tracker))
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("tool calls first=%#v second=%#v", first, second)
	}
	if first[0]["id"] == first[1]["id"] || first[0]["index"] == first[1]["index"] {
		t.Fatalf("same-name calls were collapsed: %#v", first)
	}
	for index := range first {
		if first[index]["id"] != second[index]["id"] || first[index]["index"] != second[index]["index"] {
			t.Fatalf("tool call %d changed across frames: %#v -> %#v", index, first[index], second[index])
		}
	}
}

func TestStreamToolCallTrackerKeepsToolFinishAcrossSeparateFrame(t *testing.T) {
	tracker := NewStreamToolCallTracker()
	toolChunk := map[string]any{"candidates": []any{map[string]any{
		"index": 0,
		"content": map[string]any{"parts": []any{map[string]any{
			"functionCall": map[string]any{"name": "lookup", "args": map[string]any{}},
		}}},
	}}}
	if calls := toolCallsFromEvents(t, ConvertRealtimeChunk(toolChunk, "m", "r", false, tracker)); len(calls) != 1 {
		t.Fatalf("tool calls=%#v", calls)
	}
	finishChunk := map[string]any{"candidates": []any{map[string]any{"index": 0, "finishReason": "STOP"}}}
	events := ConvertRealtimeChunk(finishChunk, "m", "r", false, tracker)
	if len(events) != 1 || !strings.Contains(events[0], `"finish_reason":"tool_calls"`) {
		t.Fatalf("finish events=%#v", events)
	}
}

func TestExtractPartsPreservesExplicitToolCallID(t *testing.T) {
	parts := []any{map[string]any{"functionCall": map[string]any{
		"id": "tool_call_1", "name": "lookup", "args": map[string]any{"q": "x"},
	}}}
	_, calls, _ := ExtractParts(parts, false)
	if len(calls) != 1 {
		t.Fatalf("calls=%#v, want one call", calls)
	}
	call, _ := calls[0].(map[string]any)
	if call["id"] != "tool_call_1" {
		t.Fatalf("call id=%v, want tool_call_1", call["id"])
	}
}

func TestFunctionCallIDAssignerFillsMissingNativeIDsAcrossChunks(t *testing.T) {
	assigner := NewFunctionCallIDAssigner()
	first := map[string]any{"functionCall": map[string]any{"name": "one", "args": map[string]any{}}}
	second := map[string]any{"functionCall": map[string]any{"name": "two", "args": map[string]any{}}}
	assigner.Ensure(first)
	assigner.Ensure(second)

	firstCall := first["functionCall"].(map[string]any)
	secondCall := second["functionCall"].(map[string]any)
	if firstCall["id"] != "tool_call_1" || secondCall["id"] != "tool_call_2" {
		t.Fatalf("assigned ids=%v, %v, want tool_call_1/tool_call_2", firstCall["id"], secondCall["id"])
	}
}

func TestNativeAndOAIToolCallIDsStayAligned(t *testing.T) {
	response := map[string]any{"candidates": []any{map[string]any{
		"content": map[string]any{"role": "model", "parts": []any{map[string]any{
			"functionCall": map[string]any{"name": "workspace_list_files", "args": map[string]any{}},
		}}},
		"finishReason": "STOP",
	}}}
	EnsureFunctionCallIDs(response)

	candidate := response["candidates"].([]any)[0].(map[string]any)
	nativePart := candidate["content"].(map[string]any)["parts"].([]any)[0].(map[string]any)
	nativeID := nativePart["functionCall"].(map[string]any)["id"]
	oai := GeminiJSONToOAIJSON(response, "gemini-3.6-flash")
	choice := oai["choices"].([]any)[0].(map[string]any)
	message := choice["message"].(map[string]any)
	oaiID := message["tool_calls"].([]any)[0].(map[string]any)["id"]
	if nativeID == "" || nativeID != oaiID {
		t.Fatalf("native id=%v, OAI id=%v, want the same non-empty id", nativeID, oaiID)
	}
}

func toolCallsFromEvents(t *testing.T, events []string) []map[string]any {
	t.Helper()
	for _, event := range events {
		var payload map[string]any
		raw := strings.TrimSpace(strings.TrimPrefix(event, "data: "))
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			t.Fatalf("decode SSE event: %v", err)
		}
		choices, _ := payload["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		choice, _ := choices[0].(map[string]any)
		delta, _ := choice["delta"].(map[string]any)
		rawCalls, _ := delta["tool_calls"].([]any)
		if len(rawCalls) == 0 {
			continue
		}
		calls := make([]map[string]any, 0, len(rawCalls))
		for _, rawCall := range rawCalls {
			if call, ok := rawCall.(map[string]any); ok {
				calls = append(calls, call)
			}
		}
		return calls
	}
	return nil
}

// 红线：UNSPECIFIED 时 finish_reason 只能是 null（在 role 事件里），不能是真实终止值。
func TestConvertRealtimeChunk_UnspecifiedNoFinishEvent(t *testing.T) {
	chunk := map[string]any{"candidates": []any{
		map[string]any{
			"content":      map[string]any{"parts": []any{map[string]any{"text": "x"}}, "role": "model"},
			"finishReason": "FINISH_REASON_UNSPECIFIED",
		},
	}}
	events := ConvertRealtimeChunk(chunk, "m", "r", false)
	// 非首帧、只有内容 → 只发 content 事件，绝无 finish 事件。
	if len(events) != 1 {
		t.Fatalf("events=%d, want 1（只 content）\n%v", len(events), events)
	}
	if strings.Contains(events[0], `"finish_reason":"`) {
		t.Errorf("UNSPECIFIED 不应发任何带值的 finish_reason: %s", events[0])
	}
}

// 真实 finishReason（STOP）发 finish 事件，并带上 usage。
func TestConvertRealtimeChunk_RealFinishWithUsage(t *testing.T) {
	chunk := map[string]any{
		"candidates": []any{map[string]any{
			"content":      map[string]any{"parts": []any{map[string]any{"text": "done"}}, "role": "model"},
			"finishReason": "STOP",
		}},
		"usageMetadata": map[string]any{
			"promptTokenCount": float64(10), "candidatesTokenCount": float64(5), "totalTokenCount": float64(15),
		},
	}
	events := ConvertRealtimeChunk(chunk, "m", "r", false)
	// content 事件 + finish(with usage) 事件 + 独立 terminal usage 事件。
	if len(events) != 3 {
		t.Fatalf("events=%d, want 3 (content + finish_with_usage + terminal_usage)\n%v", len(events), events)
	}
	finishEvt := events[1]
	if !strings.Contains(finishEvt, `"finish_reason":"stop"`) {
		t.Errorf("应发 finish_reason=stop: %s", finishEvt)
	}
	if !strings.Contains(finishEvt, `"usage"`) || !strings.Contains(finishEvt, `"total_tokens":15`) {
		t.Errorf("finish 事件应带 usage: %s", finishEvt)
	}
	terminalUsageEvt := events[2]
	if !strings.Contains(terminalUsageEvt, `"choices":[]`) || !strings.Contains(terminalUsageEvt, `"total_tokens":15`) {
		t.Errorf("末尾应发标准 choices:[] usage 帧: %s", terminalUsageEvt)
	}
}
// MAX_TOKENS → length。
func TestConvertRealtimeChunk_MaxTokensLength(t *testing.T) {
	chunk := map[string]any{"candidates": []any{map[string]any{
		"content":      map[string]any{"parts": []any{map[string]any{"text": "y"}}, "role": "model"},
		"finishReason": "MAX_TOKENS",
	}}}
	events := ConvertRealtimeChunk(chunk, "m", "r", false)
	if len(events) == 0 {
		t.Fatalf("events is empty")
	}
	last := events[len(events)-1]
	if !strings.Contains(last, `"finish_reason":"length"`) {
		t.Errorf("MAX_TOKENS → length: %s", last)
	}
}

// SSE 行格式：data: {json}\n\n。
func TestSseLine_Format(t *testing.T) {
	line := sseLine(map[string]any{"a": 1})
	if !strings.HasPrefix(line, "data: ") {
		t.Errorf("SSE 行应以 'data: ' 开头: %q", line)
	}
	if !strings.HasSuffix(line, "\n\n") {
		t.Errorf("SSE 行应以 \\n\\n 结尾: %q", line)
	}
}

// 关 HTML 转义（红线⑥）：SSE 行里的 < > & 不应被转义。
func TestSseLine_NoHTMLEscape(t *testing.T) {
	line := sseLine(map[string]any{"x": "a<b>&c"})
	if !strings.Contains(line, "a<b>&c") {
		t.Errorf("SSE 应关 HTML 转义（红线⑥）: %q", line)
	}
}

// 工具调用流式：tool_calls delta 带 index 字段（_extract_parts for_stream=True）。
func TestConvertRealtimeChunk_ToolCall(t *testing.T) {
	chunk := map[string]any{"candidates": []any{map[string]any{
		"content": map[string]any{"parts": []any{
			map[string]any{"functionCall": map[string]any{"name": "get_weather", "args": map[string]any{"city": "SF"}}},
		}, "role": "model"},
		"finishReason": "STOP",
	}}}
	events := ConvertRealtimeChunk(chunk, "m", "r", false)
	var toolEvt string
	for _, e := range events {
		// 找 delta 里带 tool_calls 数组的事件（避免误匹配 finish 事件里的 "finish_reason":"tool_calls"）。
		if strings.Contains(e, `"tool_calls":[`) {
			toolEvt = e
		}
	}
	if toolEvt == "" {
		t.Fatalf("应有 tool_calls 事件\n%v", events)
	}
	if !strings.Contains(toolEvt, `"index":0`) {
		t.Errorf("流式 tool_call 应带 index（M18）: %s", toolEvt)
	}
	if !strings.Contains(toolEvt, `"get_weather"`) {
		t.Errorf("tool_call 应含函数名: %s", toolEvt)
	}
	// STOP + 有 tool_call → finish_reason=tool_calls。
	if len(events) == 0 {
		t.Fatalf("events is empty")
	}
	last := events[len(events)-1]
	if !strings.Contains(last, `"finish_reason":"tool_calls"`) {
		t.Errorf("有工具调用应 finish_reason=tool_calls: %s", last)
	}
}
