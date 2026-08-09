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
	events := ConvertRealtimeChunk(chunk, "gemini-3.1-flash", "req123", true, nil)

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

// 红线：UNSPECIFIED 时 finish_reason 只能是 null（在 role 事件里），不能是真实终止值。
func TestConvertRealtimeChunk_UnspecifiedNoFinishEvent(t *testing.T) {
	chunk := map[string]any{"candidates": []any{
		map[string]any{
			"content":      map[string]any{"parts": []any{map[string]any{"text": "x"}}, "role": "model"},
			"finishReason": "FINISH_REASON_UNSPECIFIED",
		},
	}}
	events := ConvertRealtimeChunk(chunk, "m", "r", false, nil)
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
	events := ConvertRealtimeChunk(chunk, "m", "r", false, nil)
	// content 事件 + finish 事件。
	if len(events) != 2 {
		t.Fatalf("events=%d, want 2\n%v", len(events), events)
	}
	finishEvt := events[1]
	if !strings.Contains(finishEvt, `"finish_reason":"stop"`) {
		t.Errorf("应发 finish_reason=stop: %s", finishEvt)
	}
	if !strings.Contains(finishEvt, `"usage"`) || !strings.Contains(finishEvt, `"total_tokens":15`) {
		t.Errorf("finish 事件应带 usage: %s", finishEvt)
	}
}

// MAX_TOKENS → length。
func TestConvertRealtimeChunk_MaxTokensLength(t *testing.T) {
	chunk := map[string]any{"candidates": []any{map[string]any{
		"content":      map[string]any{"parts": []any{map[string]any{"text": "y"}}, "role": "model"},
		"finishReason": "MAX_TOKENS",
	}}}
	events := ConvertRealtimeChunk(chunk, "m", "r", false, nil)
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

// TestConvertRealtimeChunk_RoleOnlyFirstOnce 验证 isFirst 语义：
// 首个无错误 chunk 产出 role:assistant，后续 chunk 不产出 role 帧。
// 即"先发空 metadata chunk、后发 content chunk"序列中 role 仅出现一次。
func TestConvertRealtimeChunk_RoleOnlyFirstOnce(t *testing.T) {
	// metadata chunk：无 candidates（类似空的 modelVersion / 中间 usage 帧）
	metaChunk := map[string]any{"modelVersion": "gemini-2.0-flash"}
	events := ConvertRealtimeChunk(metaChunk, "m", "r", true, nil)
	// 首帧应为 role 事件（即使无 content）
	if len(events) != 1 {
		t.Fatalf("metadata chunk 应产出 1 个 role 事件，got %d: %v", len(events), events)
	}
	if !strings.Contains(events[0], `"role":"assistant"`) {
		t.Errorf("metadata chunk 产出的事件应含 role delta: %s", events[0])
	}

	// content chunk：isFirst=false，不应再产出 role 事件
	contentChunk := map[string]any{"candidates": []any{
		map[string]any{
			"content":      map[string]any{"parts": []any{map[string]any{"text": "Hi"}}, "role": "model"},
			"finishReason": "FINISH_REASON_UNSPECIFIED",
		},
	}}
	events = ConvertRealtimeChunk(contentChunk, "m", "r", false, nil)
	// 只 content，无 role
	if len(events) != 1 {
		t.Fatalf("content chunk 应产出 1 个事件（无 role），got %d: %v", len(events), events)
	}
	if strings.Contains(events[0], `"role":"assistant"`) {
		t.Errorf("非首帧不应产出 role delta: %s", events[0])
	}
	if !strings.Contains(events[0], `"content":"Hi"`) {
		t.Errorf("content chunk 应含 content: %s", events[0])
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
	events := ConvertRealtimeChunk(chunk, "m", "r", false, nil)
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

func TestExtractParts_ThoughtOnlyPart(t *testing.T) {
	// 纯思考 part：thought:true，text 为空 → 不产生 reasoning_content（但不应导致 error/drop）
	parts := []any{map[string]any{"thought": true}}
	text, tools, reasoning := ExtractParts(parts, true, nil)
	if text != "" || tools != nil || reasoning != "" {
		t.Logf("纯 thought part 产生空输出是合理的，got text=%q tools=%v reasoning=%q", text, tools, reasoning)
	}
	// text+thought 混合
	parts2 := []any{
		map[string]any{"text": "thinking text", "thought": true},
		map[string]any{"text": "answer"},
	}
	text2, _, reasoning2 := ExtractParts(parts2, true, nil)
	if reasoning2 != "thinking text" {
		t.Errorf("reasoning=%q, want 'thinking text'", reasoning2)
	}
	if text2 != "answer" {
		t.Errorf("text=%q, want 'answer'", text2)
	}
}

func TestStreamToolCallTracker_SameCallIDAcrossFrames(t *testing.T) {
	tracker := NewStreamToolCallTracker()

	idx1, id1, isNew1 := tracker.ProcessFunctionCall("get_weather")
	if !isNew1 {
		t.Error("首次调用应标记 isNew=true")
	}
	if idx1 != 0 {
		t.Errorf("index=%d, want 0", idx1)
	}

	idx2, id2, isNew2 := tracker.ProcessFunctionCall("get_weather")
	if isNew2 {
		t.Error("同名工具第二次调用不应是 isNew（漏洞2：稳定 ID）")
	}
	if idx2 != idx1 {
		t.Errorf("index 变化: %d -> %d", idx1, idx2)
	}
	if id2 != id1 {
		t.Errorf("call_id 变化: %s -> %s（漏洞2：随机 ID 生成）", id1, id2)
	}
}

func TestStreamToolCallTracker_DifferentNames(t *testing.T) {
	tracker := NewStreamToolCallTracker()
	_, id1, _ := tracker.ProcessFunctionCall("get_weather")
	idx2, id2, isNew2 := tracker.ProcessFunctionCall("get_time")
	if !isNew2 {
		t.Error("不同名工具应为新调用")
	}
	if idx2 != 1 {
		t.Errorf("index=%d, want 1", idx2)
	}
	if id2 == id1 {
		t.Error("不同工具应生成不同 call_id")
	}
}

func TestStreamToolCallTracker_EmptyNameLimit(t *testing.T) {
	// 空 name 每次生成新 entry，达到上限后应重置（防内存泄漏）
	tracker := NewStreamToolCallTracker()
	for i := 0; i < 70; i++ {
		idx, _, isNew := tracker.ProcessFunctionCall("")
		if i < 65 && !isNew {
			t.Errorf("空 name 第 %d 次应为新调用", i)
		}
		if i >= 65 {
			// 超过 64 上限后重置，index 重新从 0 开始
			if idx != i-65 {
				t.Errorf("重置后 idx=%d, want %d", idx, i-65)
			}
		}
	}
}

func TestConvertRealtimeChunk_StreamingToolCallWithTracker(t *testing.T) {
	tracker := NewStreamToolCallTracker()
	// 模拟增量帧：同一工具名在两帧中出现
	chunk1 := map[string]any{"candidates": []any{map[string]any{
		"content": map[string]any{"parts": []any{
			map[string]any{"functionCall": map[string]any{"name": "get_weather", "args": map[string]any{"city": "SF"}}},
		}, "role": "model"},
		"finishReason": "FINISH_REASON_UNSPECIFIED",
	}}}
	chunk2 := map[string]any{"candidates": []any{map[string]any{
		"content": map[string]any{"parts": []any{
			map[string]any{"functionCall": map[string]any{"name": "get_weather", "args": map[string]any{"city": "SF", "unit": "celsius"}}},
		}, "role": "model"},
		"finishReason": "FINISH_REASON_UNSPECIFIED",
	}}}
	events1 := ConvertRealtimeChunk(chunk1, "m", "r", false, tracker)
	events2 := ConvertRealtimeChunk(chunk2, "m", "r", false, tracker)

	// 两帧都应生成 tool_calls
	var tcID1, tcID2 string
	var tcIdx1, tcIdx2 int
	for _, e := range events1 {
		if strings.Contains(e, `"tool_calls"`) {
			tcID1 = extractToolCallID(e)
			tcIdx1 = extractToolCallIndex(e)
		}
	}
	for _, e := range events2 {
		if strings.Contains(e, `"tool_calls"`) {
			tcID2 = extractToolCallID(e)
			tcIdx2 = extractToolCallIndex(e)
		}
	}
	if tcID1 == "" || tcID2 == "" {
		t.Fatalf("两帧都应生成 tool_call 事件\n事件1=%v\n事件2=%v", events1, events2)
	}
	if tcID1 != tcID2 {
		t.Errorf("call_id 应稳定: %s -> %s（漏洞2）", tcID1, tcID2)
	}
	if tcIdx1 != tcIdx2 {
		t.Errorf("index 应稳定: %d -> %d", tcIdx1, tcIdx2)
	}
}

func TestConvertRealtimeChunk_MultipleToolCallsSameFrame(t *testing.T) {
	// 同一帧中有多个不同工具名的 tool calls（T2 覆盖）
	chunk := map[string]any{"candidates": []any{map[string]any{
		"content": map[string]any{"parts": []any{
			map[string]any{"functionCall": map[string]any{"name": "get_weather", "args": map[string]any{"city": "SF"}}},
			map[string]any{"functionCall": map[string]any{"name": "get_time", "args": map[string]any{"tz": "EST"}}},
		}, "role": "model"},
		"finishReason": "STOP",
	}}}
	events := ConvertRealtimeChunk(chunk, "m", "r", false, nil)
	toolCallCount := 0
	for _, e := range events {
		// 仅匹配 delta 中的 tool_calls，排除 finish 事件的 finish_reason="tool_calls"
		if strings.Contains(e, `"tool_calls":[`) {
			const prefix = "data: "
			if !strings.HasPrefix(e, prefix) {
				continue
			}
			var parsed map[string]any
			if err := json.Unmarshal([]byte(e[len(prefix):]), &parsed); err != nil {
				continue
			}
			choices, _ := parsed["choices"].([]any)
			if len(choices) == 0 {
				continue
			}
			choice, _ := choices[0].(map[string]any)
			delta, _ := choice["delta"].(map[string]any)
			tcs, _ := delta["tool_calls"].([]any)
			toolCallCount = len(tcs)
		}
	}
	if toolCallCount != 2 {
		t.Errorf("应包含 2 个 tool_calls，got %d", toolCallCount)
	}
}

// ---- test helpers ----

func extractToolCallID(event string) string {
	const prefix = "data: "
	if !strings.HasPrefix(event, prefix) {
		return ""
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(event[len(prefix):]), &parsed); err != nil {
		return ""
	}
	choices, _ := parsed["choices"].([]any)
	if len(choices) == 0 {
		return ""
	}
	choice, _ := choices[0].(map[string]any)
	delta, _ := choice["delta"].(map[string]any)
	tcs, _ := delta["tool_calls"].([]any)
	if len(tcs) == 0 {
		return ""
	}
	tc, _ := tcs[0].(map[string]any)
	id, _ := tc["id"].(string)
	return id
}

func extractToolCallIndex(event string) int {
	const prefix = "data: "
	if !strings.HasPrefix(event, prefix) {
		return -1
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(event[len(prefix):]), &parsed); err != nil {
		return -1
	}
	choices, _ := parsed["choices"].([]any)
	if len(choices) == 0 {
		return -1
	}
	choice, _ := choices[0].(map[string]any)
	delta, _ := choice["delta"].(map[string]any)
	tcs, _ := delta["tool_calls"].([]any)
	if len(tcs) == 0 {
		return -1
	}
	tc, _ := tcs[0].(map[string]any)
	idx, ok := tc["index"].(int)
	if !ok {
		return -1
	}
	return idx
}
