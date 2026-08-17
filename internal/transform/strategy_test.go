package transform

import (
	"testing"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

type mockConfigProvider struct {
	config.ConfigProvider
	defaultThinkingLevel      string
	defaultImageSize          string
	defaultResponseModalities string
	dropMax                   bool
}

func (m *mockConfigProvider) DefaultThinkingLevel() string     { return m.defaultThinkingLevel }
func (m *mockConfigProvider) DefaultImageSize() string         { return m.defaultImageSize }
func (m *mockConfigProvider) DefaultResponseModalities() string { return m.defaultResponseModalities }
func (m *mockConfigProvider) DropMaxTokens() bool               { return m.dropMax }
func (m *mockConfigProvider) SafetySettings() map[string]string { return nil }
func (m *mockConfigProvider) TrailingModelFixEnabled() bool    { return false }
func (m *mockConfigProvider) TrailingFixModels() []string      { return nil }

func assertFixed4OFF(t *testing.T, settings []SafetySetting) {
	t.Helper()
	want := []struct{ cat, th string }{
		{"HARM_CATEGORY_HATE_SPEECH", "OFF"},
		{"HARM_CATEGORY_DANGEROUS_CONTENT", "OFF"},
		{"HARM_CATEGORY_SEXUALLY_EXPLICIT", "OFF"},
		{"HARM_CATEGORY_HARASSMENT", "OFF"},
	}
	if len(settings) != len(want) {
		t.Fatalf("expected %d fixed safety settings, got %d: %v", len(want), len(settings), settings)
	}
	for i, w := range want {
		if settings[i].Category != w.cat || settings[i].Threshold != w.th {
			t.Errorf("settings[%d] = %v, want %v", i, settings[i], w)
		}
	}
}

func TestStrategies_CalculateIdleTimeouts(t *testing.T) {
	base := 15

	t.Run("TextStrategy timeouts", func(t *testing.T) {
		st := &TextStrategy{model: "gemini-2.5-flash"}
		pre, post := st.CalculateIdleTimeouts(base)
		if pre != 30*time.Second || post != 15*time.Second {
			t.Errorf("TextStrategy expected 30s/15s, got pre=%v, post=%v", pre, post)
		}

		// 测试防御性下限 (base=1s)
		preFloor, postFloor := st.CalculateIdleTimeouts(1)
		if preFloor != 20*time.Second || postFloor != 10*time.Second {
			t.Errorf("TextStrategy floor expected 20s/10s, got pre=%v, post=%v", preFloor, postFloor)
		}
	})

	t.Run("ImageStrategy timeouts", func(t *testing.T) {
		st := &ImageStrategy{model: "gemini-3.1-flash-image"}
		pre, post := st.CalculateIdleTimeouts(base)
		if pre != 60*time.Second || post != 30*time.Second {
			t.Errorf("ImageStrategy expected 60s/30s, got pre=%v, post=%v", pre, post)
		}

		preFloor, postFloor := st.CalculateIdleTimeouts(1)
		if preFloor != 60*time.Second || postFloor != 30*time.Second {
			t.Errorf("ImageStrategy floor expected 60s/30s, got pre=%v, post=%v", preFloor, postFloor)
		}
	})

	t.Run("AudioStrategy timeouts", func(t *testing.T) {
		st := &AudioStrategy{model: "gemini-3.1-flash-tts-preview"}
		pre, post := st.CalculateIdleTimeouts(base)
		if pre != 45*time.Second || post != 30*time.Second {
			t.Errorf("AudioStrategy expected 45s/30s, got pre=%v, post=%v", pre, post)
		}

		preFloor, postFloor := st.CalculateIdleTimeouts(1)
		if preFloor != 40*time.Second || postFloor != 20*time.Second {
			t.Errorf("AudioStrategy floor expected 40s/20s, got pre=%v, post=%v", preFloor, postFloor)
		}
	})
}

func TestStrategies_IsValidChunk(t *testing.T) {
	textSt := &TextStrategy{model: "gemini-2.5-flash"}
	imageSt := &ImageStrategy{model: "gemini-3.1-flash-image"}
	audioSt := &AudioStrategy{model: "gemini-3.1-flash-tts-preview"}

	// 1. Safety Block 帧 -> 所有家族策略均视作有效 Chunk
	safetyChunk := &GeminiChunk{
		Candidates: []*Candidate{
			{FinishReason: "SAFETY"},
		},
	}
	if !textSt.IsValidChunk(safetyChunk) || !imageSt.IsValidChunk(safetyChunk) || !audioSt.IsValidChunk(safetyChunk) {
		t.Error("safetyChunk should be valid for all strategies")
	}

	// 2. 纯文本 Chunk -> Text & Image 放行, Audio 拦截
	textChunk := &GeminiChunk{
		Candidates: []*Candidate{
			{
				Content: &Content{
					Parts: []Part{{Text: "Hello world"}},
				},
			},
		},
	}
	if !textSt.IsValidChunk(textChunk) {
		t.Error("textChunk should be valid for TextStrategy")
	}
	if !imageSt.IsValidChunk(textChunk) {
		t.Error("textChunk should be valid for ImageStrategy in hybrid mode")
	}
	if audioSt.IsValidChunk(textChunk) {
		t.Error("textChunk should NOT be valid for AudioStrategy")
	}

	// 3. 音频 InlineData Chunk -> Audio, Image, Text 均放行
	audioChunk := &GeminiChunk{
		Candidates: []*Candidate{
			{
				Content: &Content{
					Parts: []Part{
						{InlineData: &InlineData{MimeType: "audio/mp3", Data: "YXVkaW8="}},
					},
				},
			},
		},
	}
	if !audioSt.IsValidChunk(audioChunk) {
		t.Error("audioChunk should be valid for AudioStrategy")
	}

	// 4. 图片 InlineData Chunk -> Image 放行
	imageChunk := &GeminiChunk{
		Candidates: []*Candidate{
			{
				Content: &Content{
					Parts: []Part{
						{InlineData: &InlineData{MimeType: "image/png", Data: "aW1hZ2U="}},
					},
				},
			},
		},
	}
	if !imageSt.IsValidChunk(imageChunk) {
		t.Error("imageChunk should be valid for ImageStrategy")
	}

	// 5. 纯生图模型 (gemini-3-pro-image / imagen) 的纯文本 Chunk -> 拦截
	pureImageSt := &ImageStrategy{model: "gemini-3-pro-image"}
	if pureImageSt.IsValidChunk(textChunk) {
		t.Error("textChunk should NOT be valid for pure image strategy (gemini-3-pro-image)")
	}
}

func TestBuildSafetySettingsTyped_NilCfg(t *testing.T) {
	// 8.5：cfg 为 nil 亦安全，恒返回固定 4×OFF
	assertFixed4OFF(t, BuildSafetySettingsTyped(nil))
}

func TestStrategies_BuildVariables_FixedSafetySettings(t *testing.T) {
	cfg := &mockConfigProvider{}

	textReq := &GeminiRequest{
		Contents: []Content{{Role: "user", Parts: []Part{{Text: "hi"}}}},
		SafetySettings: []SafetySetting{
			{Category: "HARM_CATEGORY_CIVIC_INTEGRITY", Threshold: "BLOCK_LOW_AND_ABOVE"},
			{Category: "HARM_CATEGORY_JAILBREAK", Threshold: "OFF"},
			{Category: "custom-category", Threshold: "BLOCK_NONE"},
		},
	}
	textVars := BuildGeminiVariablesTyped("gemini-2.5-flash", textReq, cfg)
	assertFixed4OFF(t, textVars.SafetySettings)

	audioReq := &GeminiRequest{
		Contents: []Content{{Role: "user", Parts: []Part{{Text: "speak"}}}},
		SafetySettings: []SafetySetting{
			{Category: "HARM_CATEGORY_JAILBREAK", Threshold: "BLOCK_NONE"},
		},
	}
	audioVars := BuildGeminiVariablesTyped("gemini-3.1-flash-tts-preview", audioReq, cfg)
	assertFixed4OFF(t, audioVars.SafetySettings)

	emptyReq := &GeminiRequest{Contents: []Content{{Role: "user", Parts: []Part{{Text: "hi"}}}}}
	emptyVars := BuildGeminiVariablesTyped("gemini-2.5-flash", emptyReq, cfg)
	assertFixed4OFF(t, emptyVars.SafetySettings)
}