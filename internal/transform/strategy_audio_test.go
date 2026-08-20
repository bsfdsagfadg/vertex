package transform

import "testing"

func TestAudioStrategy_Prepare(t *testing.T) {
	st := &AudioStrategy{model: "gemini-3.1-flash-tts-preview"}
	req := &GeminiRequest{
		Tools: []Tool{{FunctionDeclarations: []FunctionDeclaration{{Name: "test"}}}},
		GenerationConfig: &GenerationConfig{
			ThinkingConfig: &ThinkingConfig{ThinkingLevel: "HIGH"},
		},
	}
	st.Prepare(req)

	if len(req.Tools) > 0 {
		t.Fatalf("expected tools to be purged in Prepare, got %v", req.Tools)
	}
	if req.GenerationConfig.ThinkingConfig != nil {
		t.Fatalf("expected thinkingConfig to be purged in Prepare, got %v", req.GenerationConfig.ThinkingConfig)
	}
	if len(req.GenerationConfig.ResponseModalities) != 1 || req.GenerationConfig.ResponseModalities[0] != "AUDIO" {
		t.Fatalf("expected responseModalities AUDIO in Prepare, got %v", req.GenerationConfig.ResponseModalities)
	}
}

func TestAudioStrategy_EnhanceAndValidate(t *testing.T) {
	cfg := &mockConfigProvider{}
	st := &AudioStrategy{model: "gemini-3.1-flash-tts-preview"}

	req := &GeminiRequest{
		Tools: []Tool{{FunctionDeclarations: []FunctionDeclaration{{Name: "test"}}}},
		GenerationConfig: &GenerationConfig{
			ThinkingConfig: &ThinkingConfig{ThinkingLevel: "HIGH"},
		},
	}

	st.Enhance(req, cfg)

	if len(req.Tools) > 0 {
		t.Fatalf("expected tools to be purged for TTS model, got %v", req.Tools)
	}
	if req.GenerationConfig.ThinkingConfig != nil {
		t.Fatalf("expected thinkingConfig to be purged for TTS model, got %v", req.GenerationConfig.ThinkingConfig)
	}
	if len(req.GenerationConfig.ResponseModalities) != 1 || req.GenerationConfig.ResponseModalities[0] != "AUDIO" {
		t.Fatalf("expected responseModalities AUDIO, got %v", req.GenerationConfig.ResponseModalities)
	}

	if err := st.Validate(req); err != nil {
		t.Fatalf("expected Validate to pass after Enhance, got %v", err)
	}
}
