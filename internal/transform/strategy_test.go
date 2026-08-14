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
		req := &GeminiRequest{
			GenerationConfig: &GenerationConfig{
				ThinkingConfig: &ThinkingConfig{ThinkingLevel: "high"},
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
		req := &GeminiRequest{
			GenerationConfig: &GenerationConfig{
				ThinkingConfig: &ThinkingConfig{ThinkingBudget: &budget, ThinkingLevel: "high"},
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
		req := &GeminiRequest{
			GenerationConfig: &GenerationConfig{
				ThinkingConfig: &ThinkingConfig{ThinkingLevel: "high"},
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
		req := &GeminiRequest{}
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
		req := &GeminiRequest{
			GenerationConfig: &GenerationConfig{
				ThinkingConfig: &ThinkingConfig{ThinkingLevel: "high"},
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
		req := &GeminiRequest{
			GenerationConfig: &GenerationConfig{
				ThinkingConfig: &ThinkingConfig{ThinkingLevel: "high"},
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
		req25 := &GeminiRequest{}
		st25.Enhance(req25, cfgWithThinking)
		if req25.GenerationConfig.ThinkingConfig != nil {
			t.Fatalf("expected nil thinkingConfig for gemini-2.5-flash-image without client input, got %v", req25.GenerationConfig.ThinkingConfig)
		}
		if err := st25.Validate(req25); err != nil {
			t.Fatalf("validation failed for gemini-2.5-flash-image: %v", err)
		}

		// 3.1-flash-image (supports MINIMAL and HIGH thinking)
		st31 := &ImageStrategy{model: "gemini-3.1-flash-image"}
		req31 := &GeminiRequest{}
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
			req := &GeminiRequest{}
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

func TestImageStrategy_EnhanceAndPrepare_DefaultSafetySettings(t *testing.T) {
	cfg := &mockConfigProvider{}
	st := &ImageStrategy{model: "gemini-3.1-flash-image"}
	req := &GeminiRequest{}

	st.Enhance(req, cfg)
	if len(req.SafetySettings) != 6 {
		t.Fatalf("expected 6 default safety settings after Enhance, got %d", len(req.SafetySettings))
	}

	st.Prepare(req)
	if len(req.SafetySettings) != 4 {
		t.Fatalf("expected 4 remaining safety settings after Prepare, got %d", len(req.SafetySettings))
	}
	for _, ss := range req.SafetySettings {
		if ss.Category == "HARM_CATEGORY_JAILBREAK" || ss.Category == "HARM_CATEGORY_CIVIC_INTEGRITY" {
			t.Fatalf("found unexpected purged category %s in req.SafetySettings", ss.Category)
		}
	}
}

func TestImageStrategy_Prepare_PreservesExplicitEmptySlice(t *testing.T) {
	st := &ImageStrategy{model: "gemini-3.1-flash-image"}
	req := &GeminiRequest{
		SafetySettings: []SafetySetting{
			{Category: "HARM_CATEGORY_JAILBREAK", Threshold: "BLOCK_NONE"},
		},
	}
	st.Prepare(req)
	if req.SafetySettings == nil {
		t.Fatalf("expected req.SafetySettings to be empty non-nil slice, got nil")
	}
	if len(req.SafetySettings) != 0 {
		t.Fatalf("expected 0 safety settings, got %d", len(req.SafetySettings))
	}
}

func TestImageStrategy_Prepare(t *testing.T) {
	models := []string{
		"gemini-2.5-flash-image",
		"gemini-3-pro-image",
		"gemini-3.1-flash-image",
		"gemini-3.1-flash-lite-image",
	}

	for _, model := range models {
		t.Run(model+" cleans jailbreak and civic integrity case-insensitively", func(t *testing.T) {
			st := &ImageStrategy{model: model}
			req := &GeminiRequest{
				SafetySettings: []SafetySetting{
					{Category: "HARM_CATEGORY_HATE_SPEECH", Threshold: "BLOCK_NONE"},
					{Category: " harm_category_jailbreak ", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
					{Category: "Harm_Category_Civic_Integrity", Threshold: "BLOCK_LOW_AND_ABOVE"},
				},
			}
			st.Prepare(req)
			if len(req.SafetySettings) != 1 || req.SafetySettings[0].Category != "HARM_CATEGORY_HATE_SPEECH" {
				t.Fatalf("expected 1 remaining safety setting HARM_CATEGORY_HATE_SPEECH for model %s, got %v", model, req.SafetySettings)
			}
		})
	}
}

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

func TestImageStrategy_IsValidResponse(t *testing.T) {
	st := &ImageStrategy{model: "gemini-3.1-flash-image"}

	// 1. 无图片无 Safety 的纯文本响应 -> 判定为无效 (非流式不能因为纯文本而误判为连通胜出)
	textResp := &GeminiResponse{
		Candidates: []*Candidate{
			{
				Content: &Content{
					Parts: []Part{{Text: "Sorry, I cannot generate an image."}},
				},
			},
		},
	}
	if st.IsValidResponse(textResp) {
		t.Error("textResp without images or safety block should NOT be valid for ImageStrategy")
	}

	// 2. 包含图片 Payload 的响应 -> 判定为有效
	imageResp := &GeminiResponse{
		Candidates: []*Candidate{
			{
				Content: &Content{
					Parts: []Part{
						{InlineData: &InlineData{MimeType: "image/png", Data: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="}},
					},
				},
			},
		},
	}
	if !st.IsValidResponse(imageResp) {
		t.Error("imageResp with image payload should be valid for ImageStrategy")
	}

	// 3. 包含 Safety 拦截的响应 -> 判定为有效
	safetyResp := &GeminiResponse{
		Candidates: []*Candidate{
			{FinishReason: "SAFETY"},
		},
	}
	if !st.IsValidResponse(safetyResp) {
		t.Error("safetyResp should be valid for ImageStrategy")
	}
}

func TestImageStrategy_BuildVariables_WhitelistAndTools(t *testing.T) {
	cfg := &mockConfigProvider{
		defaultImageSize:          "1K",
		defaultResponseModalities: "图文",
	}

	t.Run("gemini-3.1-flash-lite-image filters tools and clamps size", func(t *testing.T) {
		st := &ImageStrategy{model: "gemini-3.1-flash-lite-image"}
		req := &GeminiRequest{
			Contents: []Content{{Role: "user", Parts: []Part{{Text: "draw cat"}}}},
			Tools: []Tool{
				{GoogleSearch: map[string]any{}},
				{FunctionDeclarations: []FunctionDeclaration{{Name: "fn1"}}},
			},
			GenerationConfig: &GenerationConfig{
				ImageConfig: &ImageConfig{
					ImageSize:      "4K", // lite 不支持 4K -> 回退到 1K
					AspectRatio:    "auto",
					OutputMimeType: "image/png",
				},
				ThinkingConfig: &ThinkingConfig{ThinkingLevel: "HIGH"},
			},
		}
		vars := st.BuildVariables("gemini-3.1-flash-lite-image", req, cfg)
		outReq := vars.GeminiRequest

		// Tools 必须被清空（lite 不支持 search 也绝不允许 functions）
		if len(outReq.Tools) > 0 {
			t.Fatalf("expected 0 tools for lite-image, got %d", len(outReq.Tools))
		}
		if outReq.GenerationConfig.ImageConfig.ImageSize != "1K" {
			t.Fatalf("expected clamped 1K, got %q", outReq.GenerationConfig.ImageConfig.ImageSize)
		}
		if outReq.GenerationConfig.ThinkingConfig == nil || outReq.GenerationConfig.ThinkingConfig.ThinkingLevel != "HIGH" {
			t.Fatalf("expected HIGH thinkingLevel preserved for lite-image, got %v", outReq.GenerationConfig.ThinkingConfig)
		}
	})

	t.Run("gemini-3.1-flash-image preserves GoogleSearch and removes functions", func(t *testing.T) {
		st := &ImageStrategy{model: "gemini-3.1-flash-image"}
		req := &GeminiRequest{
			Contents: []Content{{Role: "user", Parts: []Part{{Text: "draw dog"}}}},
			Tools: []Tool{
				{GoogleSearch: map[string]any{}},
				{FunctionDeclarations: []FunctionDeclaration{{Name: "calc"}}},
			},
			GenerationConfig: &GenerationConfig{
				ImageConfig: &ImageConfig{
					ImageSize:      "4K",
					AspectRatio:    "16:9",
					OutputMimeType: "image/jpeg",
				},
				ThinkingConfig: &ThinkingConfig{ThinkingLevel: "HIGH"},
			},
		}
		vars := st.BuildVariables("gemini-3.1-flash-image", req, cfg)
		outReq := vars.GeminiRequest

		if len(outReq.Tools) != 1 || outReq.Tools[0].GoogleSearch == nil || len(outReq.Tools[0].FunctionDeclarations) > 0 {
			t.Fatalf("expected exactly 1 tool with GoogleSearch only, got %v", outReq.Tools)
		}
		if outReq.GenerationConfig.ImageConfig.ImageSize != "4K" {
			t.Fatalf("expected 4K imageSize, got %q", outReq.GenerationConfig.ImageConfig.ImageSize)
		}
	})

	t.Run("gemini-3-pro-image auto aspect ratio downgraded to 1:1 and purges thinking", func(t *testing.T) {
		st := &ImageStrategy{model: "gemini-3-pro-image"}
		req := &GeminiRequest{
			Contents: []Content{{Role: "user", Parts: []Part{{Text: "draw landscape"}}}},
			GenerationConfig: &GenerationConfig{
				ImageConfig: &ImageConfig{
					ImageSize:   "2K",
					AspectRatio: "auto", // pro 不支持 auto -> 回退到 1:1
				},
				ThinkingConfig: &ThinkingConfig{ThinkingLevel: "HIGH"}, // pro 不支持思考 -> 清空
			},
		}
		vars := st.BuildVariables("gemini-3-pro-image", req, cfg)
		outReq := vars.GeminiRequest

		if outReq.GenerationConfig.ImageConfig.AspectRatio != "1:1" {
			t.Fatalf("expected 1:1 aspect ratio fallback for pro-image, got %q", outReq.GenerationConfig.ImageConfig.AspectRatio)
		}
		if outReq.GenerationConfig.ThinkingConfig != nil {
			t.Fatalf("expected nil thinkingConfig for pro-image, got %v", outReq.GenerationConfig.ThinkingConfig)
		}
	})
}
