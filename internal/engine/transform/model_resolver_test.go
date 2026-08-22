package transform

import (
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/infra/config"
)

type mockResolverConfigProvider struct {
	config.ConfigProvider
	fakePrefixes []string
	aliasMap     map[string]string
}

func (m *mockResolverConfigProvider) FakePrefixes() []string {
	if m.fakePrefixes != nil {
		return m.fakePrefixes
	}
	return []string{"假非流-"}
}

func (m *mockResolverConfigProvider) ResolveModelName(model string) string {
	if m.aliasMap != nil {
		if realName, ok := m.aliasMap[model]; ok {
			return realName
		}
	}
	return model
}

func TestResolveModel(t *testing.T) {
	cfg := &mockResolverConfigProvider{
		fakePrefixes: []string{"假非流-"},
		aliasMap: map[string]string{
			"my-image-alias": "gemini-3.1-flash-image",
			"my-text-alias":  "gemini-3.6-flash",
			"my-audio-alias": "gemini-3.1-flash-tts-preview",
		},
	}

	tests := []struct {
		name           string
		rawModel       string
		expectedActual string
		expectedFake   bool
		expectedFamily ModelFamily
	}{
		{
			name:           "Standard text model",
			rawModel:       "gemini-2.5-pro",
			expectedActual: "gemini-2.5-pro",
			expectedFake:   false,
			expectedFamily: FamilyText,
		},
		{
			name:           "GCP prefix text model",
			rawModel:       "models/gemini-2.5-pro",
			expectedActual: "gemini-2.5-pro",
			expectedFake:   false,
			expectedFamily: FamilyText,
		},
		{
			name:           "Publishers prefix text model",
			rawModel:       "projects/p/locations/l/publishers/google/models/gemini-2.5-pro",
			expectedActual: "gemini-2.5-pro",
			expectedFake:   false,
			expectedFamily: FamilyText,
		},
		{
			name:           "Fake prefix text model",
			rawModel:       "models/假非流-gemini-3.6-flash",
			expectedActual: "gemini-3.6-flash",
			expectedFake:   true,
			expectedFamily: FamilyText,
		},
		{
			name:           "Alias image model with fake prefix",
			rawModel:       "models/假非流-my-image-alias",
			expectedActual: "gemini-3.1-flash-image",
			expectedFake:   false,
			expectedFamily: FamilyImage,
		},
		{
			name:           "Audio model",
			rawModel:       "models/my-audio-alias",
			expectedActual: "gemini-3.1-flash-tts-preview",
			expectedFake:   false,
			expectedFamily: FamilyAudio,
		},
		{
			name:           "Uppercase Models prefix image model",
			rawModel:       "Models/gemini-3.1-flash-image",
			expectedActual: "gemini-3.1-flash-image",
			expectedFake:   false,
			expectedFamily: FamilyImage,
		},
		{
			name:           "Publishers uppercase Models prefix",
			rawModel:       "projects/p/locations/l/publishers/google/Models/gemini-3-pro-image",
			expectedActual: "gemini-3-pro-image",
			expectedFake:   false,
			expectedFamily: FamilyImage,
		},
		{
			name:           "Imagen model family routing",
			rawModel:       "imagen-3.0-generate-002",
			expectedActual: "imagen-3.0-generate-002",
			expectedFake:   false,
			expectedFamily: FamilyImage,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := ResolveModel(tt.rawModel, cfg)
			if res.ActualModel != tt.expectedActual {
				t.Errorf("ActualModel = %q, want %q", res.ActualModel, tt.expectedActual)
			}
			if res.IsFake != tt.expectedFake {
				t.Errorf("IsFake = %v, want %v", res.IsFake, tt.expectedFake)
			}
			if res.Family != tt.expectedFamily {
				t.Errorf("Family = %v, want %v", res.Family, tt.expectedFamily)
			}
			if res.Strategy == nil {
				t.Errorf("Strategy is nil")
			}
		})
	}
}
