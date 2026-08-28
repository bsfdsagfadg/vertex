package transform

import "testing"

func TestApplyModelCapabilitiesKnownModelClampsExplicitValues(t *testing.T) {
	payload := map[string]any{"generationConfig": map[string]any{"temperature": 9.0, "topP": -1.0, "maxOutputTokens": float64(999999)}}
	ApplyModelCapabilities(payload, "gemini-3.5-flash")
	g := payload["generationConfig"].(map[string]any)
	if g["temperature"] != 2.0 || g["topP"] != 0.0 || g["maxOutputTokens"] != 65536 {
		t.Fatalf("clamped generation config: %#v", g)
	}
}

func TestApplyModelCapabilitiesUnknownModelPassesThrough(t *testing.T) {
	payload := map[string]any{"generationConfig": map[string]any{"temperature": 9.0, "topP": -1.0, "maxOutputTokens": float64(999999)}}
	ApplyModelCapabilities(payload, "future-model")
	g := payload["generationConfig"].(map[string]any)
	if g["temperature"] != 9.0 || g["topP"] != -1.0 || g["maxOutputTokens"] != float64(999999) {
		t.Fatalf("unknown model was modified: %#v", g)
	}
}
