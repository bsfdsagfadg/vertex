package transform

import (
	"encoding/json"
	"testing"
)

func TestConvertUsageTypes(t *testing.T) {
	meta := map[string]any{
		"promptTokenCount":        float64(15),
		"candidatesTokenCount":    float64(25),
		"totalTokenCount":         float64(40),
		"cachedContentTokenCount": float64(5),
		"thoughtsTokenCount":      float64(8),
	}

	usage := ConvertUsage(meta)
	prompt, ok1 := usage["prompt_tokens"].(int)
	comp, ok2 := usage["completion_tokens"].(int)
	total, ok3 := usage["total_tokens"].(int)

	if !ok1 || !ok2 || !ok3 {
		t.Fatalf("expected int types for tokens, got %T, %T, %T", usage["prompt_tokens"], usage["completion_tokens"], usage["total_tokens"])
	}
	if prompt != 15 || comp != 33 || total != 40 {
		t.Errorf("token counts unexpected: prompt=%d, comp=%d, total=%d", prompt, comp, total)
	}

	b, err := json.Marshal(usage)
	if err != nil {
		t.Fatalf("marshal usage failed: %v", err)
	}
	var roundtrip map[string]any
	if err := json.Unmarshal(b, &roundtrip); err != nil {
		t.Fatalf("unmarshal usage failed: %v", err)
	}
	if roundtrip["prompt_tokens"] != float64(15) || roundtrip["total_tokens"] != float64(40) {
		t.Errorf("roundtrip mismatch: %v", roundtrip)
	}
}

func TestTerminalStreamUsageHasSingleOwner(t *testing.T) {
	chunk := map[string]any{
		"candidates": []any{
			map[string]any{
				"index": 0,
				"content": map[string]any{
					"parts": []any{map[string]any{"text": "final answer"}},
				},
				"finishReason": "STOP",
			},
		},
		"usageMetadata": map[string]any{
			"promptTokenCount":     float64(100),
			"candidatesTokenCount": float64(50),
			"totalTokenCount":      float64(150),
		},
	}

	events := ConvertRealtimeChunk(chunk, "gpt-4", "req-test", false)
	// Expect: content frame + finish frame + one terminal usage-only frame (choices:[])
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d: %v", len(events), events)
	}

	finishEvt := events[1]
	terminalUsageEvt := events[2]

	var finishObj, terminalObj map[string]any
	// Strip "data: " prefix and "\n\n"
	finishJSON := finishEvt[6 : len(finishEvt)-2]
	terminalJSON := terminalUsageEvt[6 : len(terminalUsageEvt)-2]

	if err := json.Unmarshal([]byte(finishJSON), &finishObj); err != nil {
		t.Fatalf("failed to parse finishJSON: %v", err)
	}
	if err := json.Unmarshal([]byte(terminalJSON), &terminalObj); err != nil {
		t.Fatalf("failed to parse terminalJSON: %v", err)
	}

	if finishObj["usage"] != nil {
		t.Errorf("finish chunk must not duplicate usage")
	}
	if choices, ok := terminalObj["choices"].([]any); !ok || len(choices) != 0 {
		t.Errorf("terminal usage chunk must have choices: []")
	}
	if terminalObj["usage"] == nil {
		t.Errorf("terminal usage chunk must include usage")
	}
}
