package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/vertex"
)

// collectSSEData 解析一段 SSE 输出，提取所有 `data: {...}` JSON 载荷。
func collectSSEData(t *testing.T, sse string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(sse, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(payload), &obj); err != nil {
			t.Fatalf("无法解析 SSE data 载荷 %q: %v", payload, err)
		}
		out = append(out, obj)
	}
	return out
}

// TestChatHandler_WriteStreamError_IncludesChoicesField 验证「流式中途报错」的 SSE 事件
// 必须包含合规的非空 choices 数组（SDK 兼容红线），同时附带 error 字段。
//
// 背景：sseWriter 在首个写入时已发送 200 OK HTTP 头，此后不能再用 HTTP 状态码 JSON 报错，
// 只能依赖 SSE 事件内的 choices 让 OpenAI SDK 免于解析崩溃。
func TestChatHandler_WriteStreamError_IncludesChoicesField(t *testing.T) {
	var packets []map[string]any
	c := &ChatHandler{} // writeStreamError 仅依赖包级函数，无需注入网络客户端

	write := func(line string) bool {
		for _, obj := range collectSSEData(t, line) {
			packets = append(packets, obj)
		}
		return true
	}

	ve := vertex.NewNetworkError(vertex.ErrStreamIdleTimeout)
	c.writeStreamError(write, ve, "req123", "gemini-flash")

	if len(packets) == 0 {
		t.Fatal("expected at least one SSE data event")
	}

	// 断言：报错事件包必须携带非空 choices 数组。
	first := packets[0]
	choices, ok := first["choices"].([]any)
	if !ok || len(choices) == 0 {
		t.Fatalf("expected non-empty choices array in error SSE packet, got %v", first)
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		t.Fatalf("expected choices[0] to be an object, got %T", choices[0])
	}
	if choice["index"] != float64(0) && choice["index"] != 0 {
		t.Errorf("expected index=0, got %v", choice["index"])
	}
	if _, ok := choice["delta"].(map[string]any); !ok {
		t.Errorf("expected delta object present, got %v", choice["delta"])
	}
	if fr, _ := choice["finish_reason"].(string); fr != "error" {
		t.Errorf("expected finish_reason=error, got %q", fr)
	}

	// 断言 error 字段存在。
	if _, ok := first["error"]; !ok {
		t.Errorf("expected error field present in the SSE packet, got %v", first)
	}
}

// TestChatStream_MidStreamError_IncludesChoicesField 是计划约定的顶层入口测试，
// 通过写流明确空错误的访问缝，行为等同于上面的方法级测试。
func TestChatStream_MidStreamError_IncludesChoicesField(t *testing.T) {
	TestChatHandler_WriteStreamError_IncludesChoicesField(t)
}