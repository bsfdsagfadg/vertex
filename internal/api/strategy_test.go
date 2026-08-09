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
