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

func TestModelsWithFakeVariants(t *testing.T) {
	variants := ModelsWithFakeVariants()
	// 文本模型应该有假非流前缀变体
	if !contains(variants, "假非流-gemini-3.6-flash") {
		t.Errorf("ModelsWithFakeVariants 缺少文本模型的假非流变体: 假非流-gemini-3.6-flash")
	}
	// 图片和语音模型不应该有假非流前缀变体
	if contains(variants, "假非流-gemini-3.1-flash-image") {
		t.Errorf("ModelsWithFakeVariants 不应包含图像模型的假非流变体: 假非流-gemini-3.1-flash-image")
	}
	if contains(variants, "假非流-gemini-3.1-flash-tts-preview") {
		t.Errorf("ModelsWithFakeVariants 不应包含语音模型的假非流变体: 假非流-gemini-3.1-flash-tts-preview")
	}
}
