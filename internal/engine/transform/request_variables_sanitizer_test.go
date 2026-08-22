package transform

import (
	"reflect"
	"testing"
)

func TestPrepareNativeGenerationConfig_ThinkingLevel(t *testing.T) {
	tests := []struct {
		name     string
		input    *GenerationConfig
		expected string
	}{
		{
			name: "Valid lowercase thinking level",
			input: &GenerationConfig{
				ThinkingConfig: &ThinkingConfig{
					ThinkingLevel: "high",
				},
			},
			expected: "HIGH",
		},
		{
			name: "Valid uppercase thinking level with spaces",
			input: &GenerationConfig{
				ThinkingConfig: &ThinkingConfig{
					ThinkingLevel: " low ",
				},
			},
			expected: "LOW",
		},
		{
			name: "Unspecified thinking level",
			input: &GenerationConfig{
				ThinkingConfig: &ThinkingConfig{
					ThinkingLevel: "THINKING_LEVEL_UNSPECIFIED",
				},
			},
			expected: "THINKING_LEVEL_UNSPECIFIED",
		},
		{
			name: "Invalid thinking level fallback to MEDIUM",
			input: &GenerationConfig{
				ThinkingConfig: &ThinkingConfig{
					ThinkingLevel: "invalid_level",
				},
			},
			expected: "MEDIUM",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := prepareNativeGenerationConfig(tt.input)
			if got == nil || got.ThinkingConfig == nil {
				t.Fatalf("expected non-nil ThinkingConfig")
			}
			if got.ThinkingConfig.ThinkingLevel != tt.expected {
				t.Errorf("ThinkingLevel = %v, want %v", got.ThinkingConfig.ThinkingLevel, tt.expected)
			}
		})
	}
}

func TestPrepareNativeGenerationConfig_MediaResolution(t *testing.T) {
	tests := []struct {
		name     string
		input    *GenerationConfig
		expected string
	}{
		{
			name: "Short form low",
			input: &GenerationConfig{
				MediaResolution: "low",
			},
			expected: "MEDIA_RESOLUTION_LOW",
		},
		{
			name: "Short form medium with spaces",
			input: &GenerationConfig{
				MediaResolution: " medium ",
			},
			expected: "MEDIA_RESOLUTION_MEDIUM",
		},
		{
			name: "Full form high",
			input: &GenerationConfig{
				MediaResolution: "MEDIA_RESOLUTION_HIGH",
			},
			expected: "MEDIA_RESOLUTION_HIGH",
		},
		{
			name: "Full form unspecified",
			input: &GenerationConfig{
				MediaResolution: "media_resolution_unspecified",
			},
			expected: "MEDIA_RESOLUTION_UNSPECIFIED",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := prepareNativeGenerationConfig(tt.input)
			if got == nil {
				t.Fatalf("expected non-nil GenerationConfig")
			}
			if got.MediaResolution != tt.expected {
				t.Errorf("MediaResolution = %v, want %v", got.MediaResolution, tt.expected)
			}
		})
	}
}

func TestPrepareNativeGenerationConfig_ResponseModalities(t *testing.T) {
	input := &GenerationConfig{
		ResponseModalities: []string{"text", " image "},
	}
	expected := []string{"TEXT", "IMAGE"}

	got := prepareNativeGenerationConfig(input)
	if got == nil {
		t.Fatalf("expected non-nil GenerationConfig")
	}
	if !reflect.DeepEqual(got.ResponseModalities, expected) {
		t.Errorf("ResponseModalities = %v, want %v", got.ResponseModalities, expected)
	}
}
