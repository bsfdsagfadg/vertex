package config

import "testing"

func TestDefaultModelsContainsAllExpected(t *testing.T) {
	expected := []string{
		"gemini-3.1-flash-tts-preview",
		"gemini-3.1-flash-lite-image",
		"gemini-3.5-flash-lite",
		"gemini-3.6-flash",
	}
	for _, m := range expected {
		if !contains(defaultModels, m) {
			t.Errorf("defaultModels 缺少模型: %s", m)
		}
	}
}
