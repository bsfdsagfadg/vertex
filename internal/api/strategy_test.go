package api

import (
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/transform"
)

type mockConfigProvider struct {
	config.ConfigProvider
	defaultThinkingLevel      string
	defaultImageSize          string
	defaultResponseModalities string
}

func (m *mockConfigProvider) DefaultThinkingLevel() string     { return m.defaultThinkingLevel }
func (m *mockConfigProvider) DefaultImageSize() string         { return m.defaultImageSize }
func (m *mockConfigProvider) DefaultResponseModalities() string { return m.defaultResponseModalities }
func (m *mockConfigProvider) DropMaxTokens() bool               { return false }
func (m *mockConfigProvider) SafetySettings() map[string]string { return nil }

func TestTextStrategy_Enhance(t *testing.T) {
	cfg := &mockConfigProvider{defaultThinkingLevel: "中"}

	t.Run("2.5 pro - lowercase high level converted to budget", func(t *testing.T) {
		st := &TextStrategy{model: "gemini-2.5-pro"}
		req := &transform.GeminiRequest{
			GenerationConfig: &transform.GenerationConfig{
				ThinkingConfig: &transform.ThinkingConfig{ThinkingLevel: "high"},
			},
		}
		st.Enhance(req, cfg)
		tc := req.GenerationConfig.ThinkingConfig
		if tc == nil || tc.ThinkingBudget == nil || *tc.ThinkingBudget != 32768 {
			t.Fatalf("expected 32768 budget, got %v", tc)
		}
		if tc.ThinkingLevel != "" {
			t.Errorf("expected empty thinkingLevel, got %q", tc.ThinkingLevel)
		}
	})

	t.Run("2.5 pro - explicit budget passthrough", func(t *testing.T) {
		st := &TextStrategy{model: "gemini-2.5-pro"}
		budget := 8000
		req := &transform.GeminiRequest{
			GenerationConfig: &transform.GenerationConfig{
				ThinkingConfig: &transform.ThinkingConfig{ThinkingBudget: &budget, ThinkingLevel: "high"},
			},
		}
		st.Enhance(req, cfg)
		tc := req.GenerationConfig.ThinkingConfig
		if tc == nil || tc.ThinkingBudget == nil || *tc.ThinkingBudget != 8000 {
			t.Fatalf("expected passthrough 8000 budget, got %v", tc)
		}
		if tc.ThinkingLevel != "" {
			t.Errorf("expected empty thinkingLevel, got %q", tc.ThinkingLevel)
		}
	})

	t.Run("3.6 flash - lowercase level to uppercase enum", func(t *testing.T) {
		st := &TextStrategy{model: "gemini-3.6-flash"}
		req := &transform.GeminiRequest{
			GenerationConfig: &transform.GenerationConfig{
				ThinkingConfig: &transform.ThinkingConfig{ThinkingLevel: "high"},
			},
		}
		st.Enhance(req, cfg)
		tc := req.GenerationConfig.ThinkingConfig
		if tc == nil || tc.ThinkingLevel != "HIGH" {
			t.Fatalf("expected ThinkingLevel HIGH, got %v", tc)
		}
		if tc.ThinkingBudget != nil {
			t.Errorf("expected nil thinkingBudget, got %v", tc.ThinkingBudget)
		}
	})

	t.Run("no client thinkingConfig - fallback to console default", func(t *testing.T) {
		st := &TextStrategy{model: "gemini-3.6-flash"}
		req := &transform.GeminiRequest{}
		st.Enhance(req, cfg)
		tc := req.GenerationConfig.ThinkingConfig
		if tc == nil || tc.ThinkingLevel != "MEDIUM" {
			t.Fatalf("expected console default MEDIUM, got %v", tc)
		}
	})
}

func TestImageStrategy_EnhanceAndValidate(t *testing.T) {
	cfg := &mockConfigProvider{defaultImageSize: "1K", defaultResponseModalities: "默认"}

	t.Run("3.1 flash image - lowercase level normalized and validated", func(t *testing.T) {
		st := &ImageStrategy{model: "gemini-3.1-flash-image"}
		req := &transform.GeminiRequest{
			GenerationConfig: &transform.GenerationConfig{
				ThinkingConfig: &transform.ThinkingConfig{ThinkingLevel: "high"},
			},
		}
		st.Enhance(req, cfg)
		if err := st.Validate(req); err != nil {
			t.Fatalf("validation failed: %v", err)
		}
		tc := req.GenerationConfig.ThinkingConfig
		if tc == nil || tc.ThinkingLevel != "HIGH" {
			t.Fatalf("expected HIGH, got %v", tc)
		}
	})

	t.Run("unsupported image model - thinkingConfig purged to nil", func(t *testing.T) {
		st := &ImageStrategy{model: "gemini-2.5-flash-image"}
		req := &transform.GeminiRequest{
			GenerationConfig: &transform.GenerationConfig{
				ThinkingConfig: &transform.ThinkingConfig{ThinkingLevel: "high"},
			},
		}
		st.Enhance(req, cfg)
		if err := st.Validate(req); err != nil {
			t.Fatalf("validation failed: %v", err)
		}
		if req.GenerationConfig.ThinkingConfig != nil {
			t.Fatalf("expected nil thinkingConfig for unsupported image model, got %v", req.GenerationConfig.ThinkingConfig)
		}
	})

	t.Run("no client thinkingConfig - image strategy does not inject console default level", func(t *testing.T) {
		cfgWithThinking := &mockConfigProvider{defaultThinkingLevel: "中", defaultImageSize: "1K", defaultResponseModalities: "默认"}

		// 2.5-flash-image (unsupported thinking)
		st25 := &ImageStrategy{model: "gemini-2.5-flash-image"}
		req25 := &transform.GeminiRequest{}
		st25.Enhance(req25, cfgWithThinking)
		if req25.GenerationConfig.ThinkingConfig != nil {
			t.Fatalf("expected nil thinkingConfig for gemini-2.5-flash-image without client input, got %v", req25.GenerationConfig.ThinkingConfig)
		}
		if err := st25.Validate(req25); err != nil {
			t.Fatalf("validation failed for gemini-2.5-flash-image: %v", err)
		}

		// 3.1-flash-image (supports MINIMAL and HIGH thinking)
		st31 := &ImageStrategy{model: "gemini-3.1-flash-image"}
		req31 := &transform.GeminiRequest{}
		st31.Enhance(req31, cfgWithThinking)
		if req31.GenerationConfig.ThinkingConfig != nil {
			t.Fatalf("expected nil thinkingConfig for gemini-3.1-flash-image without client input, got %v", req31.GenerationConfig.ThinkingConfig)
		}
		if err := st31.Validate(req31); err != nil {
			t.Fatalf("validation failed for gemini-3.1-flash-image: %v", err)
		}
	})
}

func TestImageStrategy_All4Models_ThinkingIsolation(t *testing.T) {
	cfgWithThinking := &mockConfigProvider{
		defaultThinkingLevel:      "高",
		defaultImageSize:          "1K",
		defaultResponseModalities: "默认",
	}

	models := []string{
		"gemini-2.5-flash-image",
		"gemini-3-pro-image",
		"gemini-3.1-flash-image",
		"gemini-3.1-flash-lite-image",
	}

	for _, model := range models {
		t.Run(model+" thinking isolation", func(t *testing.T) {
			st := &ImageStrategy{model: model}
			req := &transform.GeminiRequest{}
			st.Enhance(req, cfgWithThinking)
			if req.GenerationConfig.ThinkingConfig != nil {
				t.Fatalf("expected nil thinkingConfig for model %s without client input when console default is High, got %v", model, req.GenerationConfig.ThinkingConfig)
			}
			if err := st.Validate(req); err != nil {
				t.Fatalf("validation failed for model %s: %v", model, err)
			}
		})
	}
}

func TestImageStrategy_SafetySettings_CleanJailbreak(t *testing.T) {
	cfg := &mockConfigProvider{}
	models := []string{
		"gemini-2.5-flash-image",
		"gemini-3-pro-image",
		"gemini-3.1-flash-image",
		"gemini-3.1-flash-lite-image",
	}

	for _, model := range models {
		t.Run(model+" cleans jailbreak", func(t *testing.T) {
			st := &ImageStrategy{model: model}
			req := &transform.GeminiRequest{
				SafetySettings: []transform.SafetySetting{
					{Category: "HARM_CATEGORY_HATE_SPEECH", Threshold: "BLOCK_NONE"},
					{Category: "HARM_CATEGORY_JAILBREAK", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
				},
			}
			st.Enhance(req, cfg)
			for _, ss := range req.SafetySettings {
				if ss.Category == "HARM_CATEGORY_JAILBREAK" {
					t.Fatalf("model %s still has HARM_CATEGORY_JAILBREAK in SafetySettings after Enhance", model)
				}
			}
			if len(req.SafetySettings) != 1 || req.SafetySettings[0].Category != "HARM_CATEGORY_HATE_SPEECH" {
				t.Fatalf("expected 1 safety setting for model %s, got %v", model, req.SafetySettings)
			}
		})
	}
}

func TestAudioStrategy_EnhanceAndValidate(t *testing.T) {
	cfg := &mockConfigProvider{}
	st := &AudioStrategy{model: "gemini-3.1-flash-tts-preview"}

	req := &transform.GeminiRequest{
		Tools: []transform.Tool{{FunctionDeclarations: []transform.FunctionDeclaration{{Name: "test"}}}},
		GenerationConfig: &transform.GenerationConfig{
			ThinkingConfig: &transform.ThinkingConfig{ThinkingLevel: "HIGH"},
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
