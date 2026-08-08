package transform

import (
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

// A tool result followed by ordinary user text must remain separate contents.
// Merging the mixed parts into one turn is rejected by the Gemini endpoint with
// a 400 and is the smallest regression fixture for the historical tool loop.
func TestConvertChatRequestKeepsMixedFunctionResponseHistorySeparate(t *testing.T) {
	cfg := config.StaticProvider(config.DefaultConfig())
	_, payload, err := ConvertChatRequest(map[string]any{
		"model": "gemini-3.1-flash",
		"messages": []any{
			map[string]any{"role": "assistant", "tool_calls": []any{
				map[string]any{"id": "call-1", "type": "function", "function": map[string]any{
					"name": "lookup", "arguments": "{}",
				}},
			}},
			map[string]any{"role": "tool", "tool_call_id": "call-1", "content": `{"ok":true}`},
			map[string]any{"role": "user", "content": "continue"},
		},
	}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	contents, ok := payload["contents"].([]any)
	if !ok || len(contents) != 3 {
		t.Fatalf("contents=%#v, want separate model/function/user turns", payload["contents"])
	}
	if contents[1].(map[string]any)["role"] != "function" || contents[2].(map[string]any)["role"] != "user" {
		t.Fatalf("mixed function response history was merged: %#v", contents)
	}
}
