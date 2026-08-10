package api

import "testing"

func TestRewriteGeminiIDsSupportsToolCallPrefix(t *testing.T) {
	value := map[string]any{"functionCall": map[string]any{"id": "tool_call_1"}}
	rewriteGeminiIDs(value, "-vp12345678")
	call := value["functionCall"].(map[string]any)
	if call["id"] != "tool_call_1-vp12345678" {
		t.Fatalf("rewritten id=%v, want tool_call_1-vp12345678", call["id"])
	}
}
