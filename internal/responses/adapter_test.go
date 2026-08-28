package responses

import (
	"encoding/json"
	"testing"
)

func TestNormalizeInputAndBuildGeminiMultimodal(t *testing.T) {
	r := CreateRequest{Model: "gemini-test", Input: []any{
		map[string]any{"type": "message", "role": "user", "content": []any{
			map[string]any{"type": "input_text", "text": "describe"},
			map[string]any{"type": "input_image", "image_url": "https://example.com/cat.jpg"},
			map[string]any{"type": "input_audio", "data": "QUFB", "mime_type": "audio/mpeg"},
		}},
	}, Text: map[string]any{"format": map[string]any{"type": "json_schema", "schema": map[string]any{"type": "object"}}}}
	payload, _, err := BuildGemini(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	contents := payload["contents"].([]any)
	parts := contents[0].(map[string]any)["parts"].([]any)
	if len(parts) != 3 || parts[0].(map[string]any)["text"] != "describe" {
		t.Fatalf("unexpected parts: %#v", parts)
	}
	if fd := parts[1].(map[string]any)["fileData"].(map[string]any); fd["fileUri"] != "https://example.com/cat.jpg" {
		t.Fatalf("image conversion: %#v", fd)
	}
	if id := parts[2].(map[string]any)["inlineData"].(map[string]any); id["mimeType"] != "audio/mpeg" || id["data"] != "QUFB" {
		t.Fatalf("audio conversion: %#v", id)
	}
	g := payload["generationConfig"].(map[string]any)
	if g["responseMimeType"] != "application/json" || g["responseSchema"] == nil {
		t.Fatalf("json schema conversion: %#v", g)
	}
}

func TestBuildGeminiReasoningAndTools(t *testing.T) {
	r := CreateRequest{Model: "m", Input: "hello", Reasoning: map[string]any{"effort": "high"}, Tools: []any{map[string]any{"type": "function", "name": "lookup", "parameters": map[string]any{"type": "object"}}}, ToolChoice: map[string]any{"type": "function", "name": "lookup"}}
	p, _, err := BuildGemini(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	g := p["generationConfig"].(map[string]any)
	if g["thinkingConfig"].(map[string]any)["thinkingLevel"] != "HIGH" {
		t.Fatalf("reasoning not normalized: %#v", g)
	}
	if p["tools"] == nil || p["toolConfig"] == nil {
		t.Fatalf("tools missing: %#v", p)
	}
}

func TestFunctionCallIdentityAndOutputItems(t *testing.T) {
	_, _, err := BuildGemini(CreateRequest{Model: "m", Input: []any{map[string]any{"type": "function_call_output", "call_id": "c1", "output": "ok"}}}, nil)
	if err == nil {
		t.Fatal("expected missing function call state")
	}
	r := CreateRequest{Model: "m", Input: []any{
		map[string]any{"type": "function_call", "call_id": "c1", "name": "lookup", "arguments": `{"q":"x"}`},
		map[string]any{"type": "function_call_output", "call_id": "c1", "output": "ok"},
	}}
	p, _, err := BuildGemini(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs := p["contents"].([]any)
	if cs[0].(map[string]any)["role"] != "model" || cs[1].(map[string]any)["role"] != "function" {
		t.Fatalf("function turns: %#v", cs)
	}
	resp := map[string]any{"responseId": "r1", "candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": "done"}, map[string]any{"functionCall": map[string]any{"id": "c1", "name": "lookup", "args": map[string]any{"q": "x"}}}}}, "finishReason": "STOP"}}}
	items, _, status := OutputItems(resp)
	if status != "completed" || len(items) != 2 {
		t.Fatalf("output conversion: status=%s items=%#v", status, items)
	}
	foundArgs := false
	for _, raw := range items {
		if m, ok := raw.(map[string]any); ok && m["type"] == "function_call" {
			_, foundArgs = m["arguments"].(string)
		}
	}
	if !foundArgs {
		t.Fatal("function arguments must be JSON string")
	}
}

func TestCreateRequestUnknownFieldsSorted(t *testing.T) {
	var r CreateRequest
	if err := json.Unmarshal([]byte(`{"model":"m","input":"x","z":1,"a":2}`), &r); err != nil {
		t.Fatal(err)
	}
	got := r.UnknownFields()
	if len(got) != 2 || got[0] != "a" || got[1] != "z" {
		t.Fatalf("unknown fields: %#v", got)
	}
}
