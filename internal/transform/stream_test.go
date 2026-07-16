package transform

import (
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

// TestConvertRealtimeChunk_RoleOnlyFirstOnce 验证 isFirst 语义：
// 首个无错误 chunk 产出 role:assistant，后续 chunk 不产出 role 帧。
// 即"先发空 metadata chunk、后发 content chunk"序列中 role 仅出现一次。
func TestConvertRealtimeChunk_RoleOnlyFirstOnce(t *testing.T) {
	// metadata chunk：无 candidates（类似空的 modelVersion / 中间 usage 帧）
	metaChunk := map[string]any{"modelVersion": "gemini-2.0-flash"}
	events := ConvertRealtimeChunk(metaChunk, "m", "r", true)
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
	events = ConvertRealtimeChunk(contentChunk, "m", "r", false)
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
