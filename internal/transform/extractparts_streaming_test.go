package transform

import (
	"strings"
	"testing"
)

// 上游流式每个 part 会带上所有字段的空默认值（text:"" + 空 inlineData/functionCall），
// 靠真实非空字段区分类型（实测结构，见上游流式探针）。此前 ExtractParts 用「text 键存在」
// 判类型，会把带 text:"" 的工具/图片帧误判成空文本，导致流式下 functionCall/inlineData 被丢。
// 这些回归测试锁定按「值非空」判类型。

func TestExtractParts_StreamingDirtyFunctionCall(t *testing.T) {
	// 实测的工具帧：text:"" + 真实 functionCall。
	part := map[string]any{
		"data": "functionCall", "text": "", "thought": false,
		"inlineData":       map[string]any{"mimeType": "", "data": ""},
		"functionCall":     map[string]any{"name": "get_weather", "args": map[string]any{"city": "北京"}},
		"functionResponse": map[string]any{"name": "", "response": map[string]any{}},
	}
	_, tools, _ := ExtractParts([]any{part}, true, nil)
	if len(tools) != 1 {
		t.Fatalf("带 text:'' 的 functionCall 帧应识别为 1 个 tool_call，got %d", len(tools))
	}
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "get_weather" {
		t.Errorf("tool name 应为 get_weather，got %v", fn["name"])
	}
}

func TestExtractParts_StreamingDirtyInlineData(t *testing.T) {
	// 实测的生图帧：text:"" + 真实 inlineData(image)。
	part := map[string]any{
		"data": "inlineData", "text": "", "thought": false,
		"inlineData":   map[string]any{"mimeType": "image/png", "data": "iVBORw0KGgoAAAANS"},
		"functionCall": map[string]any{"name": "", "args": map[string]any{}},
	}
	text, _, _ := ExtractParts([]any{part}, true, nil)
	if !strings.Contains(text, "data:image/png;base64,iVBORw0KGgoAAAANS") {
		t.Errorf("带 text:'' 的生图帧应输出图片 markdown，got %q", text)
	}
}

func TestExtractParts_StreamingDirtyThought(t *testing.T) {
	// 思考帧：text 非空 + thought=true + 空 functionCall/inlineData。
	part := map[string]any{
		"data": "text", "text": "**Calling Weather Tool**", "thought": true,
		"inlineData":   map[string]any{"mimeType": "", "data": ""},
		"functionCall": map[string]any{"name": "", "args": map[string]any{}},
	}
	text, tools, reasoning := ExtractParts([]any{part}, true, nil)
	if reasoning != "**Calling Weather Tool**" {
		t.Errorf("思考帧应进 reasoning，got %q", reasoning)
	}
	if tools != nil || text != "" {
		t.Errorf("思考帧不应产生 tool_calls/text，got tools=%v text=%q", tools, text)
	}
}

func TestExtractParts_EmptyTextNotTreatedAsText(t *testing.T) {
	part := map[string]any{"text": "", "functionCall": map[string]any{"name": "f", "args": map[string]any{}}}
	text, tools, _ := ExtractParts([]any{part}, false, nil)
	if text != "" {
		t.Errorf("text:'' 不应产生文本，got %q", text)
	}
	if len(tools) != 1 {
		t.Fatalf("应识别为 tool_call，got %d", len(tools))
	}
}

// 回归：干净 part 仍正常（非流式逐字节不变）。
func TestExtractParts_CleanPartsUnchanged(t *testing.T) {
	text, tools, _ := ExtractParts([]any{map[string]any{"text": "hello"}}, false, nil)
	if text != "hello" || tools != nil {
		t.Errorf("干净文本 part 回归失败：text=%q tools=%v", text, tools)
	}
	_, tools2, _ := ExtractParts([]any{map[string]any{"functionCall": map[string]any{"name": "g", "args": map[string]any{}}}}, false, nil)
	if len(tools2) != 1 {
		t.Errorf("干净 functionCall part 回归失败")
	}
	text3, _, reasoning3 := ExtractParts([]any{
		map[string]any{"text": "thinking", "thought": true},
		map[string]any{"text": "answer"},
	}, false, nil)
	if text3 != "answer" || reasoning3 != "thinking" {
		t.Errorf("thought+text 回归失败：text=%q reasoning=%q", text3, reasoning3)
	}
}
