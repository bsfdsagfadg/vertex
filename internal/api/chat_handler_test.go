package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/vertex"
)

func TestWriteStreamErrorEmitsValidTerminalChoices(t *testing.T) {
	var packets []map[string]any
	write := func(line string) bool {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") || strings.TrimSpace(strings.TrimPrefix(line, "data:")) == "[DONE]" {
			return true
		}
		var packet map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &packet); err != nil {
			t.Fatalf("invalid SSE packet %q: %v", line, err)
		}
		packets = append(packets, packet)
		return true
	}

	(&ChatHandler{}).writeStreamError(write, vertex.NewNetworkError(assertableError("stream failed")), "req-1", "gemini-flash")
	if len(packets) != 1 {
		t.Fatalf("packets=%d, want one terminal packet", len(packets))
	}
	choices, ok := packets[0]["choices"].([]any)
	if !ok || len(choices) != 1 {
		t.Fatalf("choices=%#v, want one choice", packets[0]["choices"])
	}
	choice := choices[0].(map[string]any)
	if choice["finish_reason"] != "error" {
		t.Fatalf("finish_reason=%v, want error", choice["finish_reason"])
	}
	if _, ok := choice["delta"].(map[string]any); !ok {
		t.Fatalf("delta=%#v, want object", choice["delta"])
	}
	if _, ok := packets[0]["error"].(map[string]any); !ok {
		t.Fatalf("error=%#v, want object", packets[0]["error"])
	}
}

func TestFirstChoiceToolCallsAddsStreamIndexesAndKeepsIDs(t *testing.T) {
	oai := map[string]any{"choices": []any{map[string]any{"message": map[string]any{
		"tool_calls": []any{
			map[string]any{"id": "tool_call_1", "type": "function"},
			map[string]any{"id": "tool_call_2", "type": "function"},
		},
	}}}}

	calls := firstChoiceToolCalls(oai)
	if len(calls) != 2 {
		t.Fatalf("calls=%#v, want two calls", calls)
	}
	for index, rawCall := range calls {
		call := rawCall.(map[string]any)
		if call["index"] != index || call["id"] == "" {
			t.Fatalf("call %d=%#v, want indexed call with id", index, call)
		}
	}
}

type assertableError string

func (e assertableError) Error() string { return string(e) }
