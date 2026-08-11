package transform

import "testing"

func TestImageSizeAllowedFor(t *testing.T) {
	cases := []struct {
		name  string
		model string
		tier  string
		want  bool
	}{
		{"lite-2K unsupported", "gemini-3.1-flash-lite-image", "2K", false},
		{"lite-1K supported", "gemini-3.1-flash-lite-image", "1K", true},
		{"flash-4K supported", "gemini-3.1-flash-image", "4K", true},
		{"flash-512 supported", "gemini-3.1-flash-image", "512", true},
		{"pro-512 unsupported", "gemini-3-pro-image", "512", false},
		{"unknown-1K default", "unknown-model", "1K", true},
		{"unknown-4K unsupported default", "unknown-model", "4K", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ImageSizeAllowedFor(c.model, c.tier); got != c.want {
				t.Errorf("ImageSizeAllowedFor(%q,%q)=%v，期望 %v", c.model, c.tier, got, c.want)
			}
		})
	}
}

func TestAspectRatioAllowedFor(t *testing.T) {
	cases := []struct {
		name  string
		model string
		ratio string
		want  bool
	}{
		{"pro-auto unsupported", "gemini-3-pro-image", "auto", false},
		{"flash-auto supported", "gemini-3.1-flash-image", "auto", true},
		{"flash-21:9 supported", "gemini-3.1-flash-image", "21:9", true},
		{"pro-21:9 supported", "gemini-3-pro-image", "21:9", true},
		{"lite-auto supported", "gemini-3.1-flash-lite-image", "auto", true},
		{"unknown-1:1 default", "unknown-model", "1:1", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := aspectRatioAllowedFor(c.model, c.ratio); got != c.want {
				t.Errorf("aspectRatioAllowedFor(%q,%q)=%v，期望 %v", c.model, c.ratio, got, c.want)
			}
		})
	}
}

// ============ 图像分辨率 ApplyImageConfig ============

func TestResolveImageConfig(t *testing.T) {
	flashModel := "gemini-3.1-flash-image"
	liteModel := "gemini-3.1-flash-lite-image"
	proModel := "gemini-3-pro-image"

	// image_size 档位
	ic := ResolveImageConfigPassthrough(map[string]any{"image_size": "2K"}, flashModel)
	if ic == nil || ic.ImageSize != "2K" {
		t.Error("image_size=2K 未解析")
	}

	// imageConfig 顶层透传
	ic3 := ResolveImageConfigPassthrough(map[string]any{"aspectRatio": "16:9"}, flashModel)
	if ic3 == nil || ic3.AspectRatio != "16:9" {
		t.Error("imageConfig 透传失败")
	}

	// 不命中
	ic4 := ResolveImageConfigPassthrough(map[string]any{}, flashModel)
	if ic4 != nil {
		t.Errorf("无分辨率参数时不应返回 config: %v", ic4)
	}

	// lite-image：image_size="4K" → imageSize 不写入（4K 被过滤）
	ic9 := ResolveImageConfigPassthrough(map[string]any{"image_size": "4K"}, liteModel)
	if ic9 != nil && ic9.ImageSize != "" {
		t.Error("lite-image 的 4K 应被过滤丢弃")
	}

	// pro-image：imageConfig.aspectRatio="auto" → passthrough 时 auto 降级为 1:1
	ic10 := ResolveImageConfigPassthrough(map[string]any{"aspectRatio": "auto"}, proModel)
	if ic10 == nil || ic10.AspectRatio != "1:1" {
		t.Errorf("pro-image 不支持 auto，应自动降级为 1:1，得到: %v", ic10)
	}

	// flash-image：imageConfig.imageSize="8K" → passthrough 被过滤丢弃
	ic12 := ResolveImageConfigPassthrough(map[string]any{"imageSize": "8K"}, flashModel)
	if ic12 != nil && ic12.ImageSize != "" {
		t.Error("8K 不应被透传")
	}
}

func TestAudioAdaptor_VoiceMapping(t *testing.T) {
	adaptor := NewAudioAdaptor()

	cases := []struct {
		name      string
		voice     string
		wantVoice string
	}{
		{"alloy maps to Kore", "alloy", "Kore"},
		{"nova maps to Aoede", "nova", "Aoede"},
		{"Puck remains Puck", "Puck", "Puck"},
		{"empty string defaults to Kore", "", "Kore"},
		{"unknown voice defaults to Kore", "unknown_voice", "Kore"},
		{"lowercase mapped voice", "echo", "Puck"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &SpeechRequest{
				Model: "gemini-3.1-flash-tts-preview",
				Input: "Hello world",
				Voice: c.voice,
			}
			geminiReq, _, err := adaptor.ToGeminiRequest(req, nil)
			if err != nil {
				t.Fatalf("ToGeminiRequest error: %v", err)
			}
			if geminiReq.GenerationConfig == nil || geminiReq.GenerationConfig.SpeechConfig == nil ||
				geminiReq.GenerationConfig.SpeechConfig.VoiceConfig == nil ||
				geminiReq.GenerationConfig.SpeechConfig.VoiceConfig.PrebuiltVoiceConfig == nil {
				t.Fatalf("missing voiceConfig in GeminiRequest: %+v", geminiReq)
			}
			gotVoice := geminiReq.GenerationConfig.SpeechConfig.VoiceConfig.PrebuiltVoiceConfig.VoiceName
			if gotVoice != c.wantVoice {
				t.Errorf("got voice %q, want %q", gotVoice, c.wantVoice)
			}
		})
	}
}

func TestImageAdaptor_All4Models(t *testing.T) {
	adaptor := NewImageAdaptor()

	t.Run("gemini-2.5-flash-image - 1024x1024 generates 1K and 1:1", func(t *testing.T) {
		req := &ImagesRequest{
			Model:  "gemini-2.5-flash-image",
			Prompt: "A futuristic city",
			Size:   "1024x1024",
		}
		geminiReq, model, err := adaptor.ToGeminiRequest(req, nil)
		if err != nil {
			t.Fatalf("ToGeminiRequest error: %v", err)
		}
		if model != "gemini-2.5-flash-image" {
			t.Errorf("expected model gemini-2.5-flash-image, got %q", model)
		}
		ic := geminiReq.GenerationConfig.ImageConfig
		if ic == nil {
			t.Fatalf("expected ImageConfig, got nil")
		}
		if ic.ImageSize != "1K" {
			t.Errorf("expected ImageSize 1K for gemini-2.5-flash-image, got %q", ic.ImageSize)
		}
		if ic.AspectRatio != "1:1" {
			t.Errorf("expected AspectRatio 1:1, got %q", ic.AspectRatio)
		}

		payload := BuildImagePayload("gemini-2.5-flash-image", "A futuristic city", nil, nil, "1024x1024", "", "", "", "")
		genCfg, ok := payload["generationConfig"].(map[string]any)
		if !ok {
			t.Fatalf("expected generationConfig map in BuildImagePayload result")
		}
		imgCfg, ok := genCfg["imageConfig"].(map[string]any)
		if !ok {
			t.Fatalf("expected imageConfig map in BuildImagePayload result")
		}
		if imgCfg["imageSize"] != "1K" {
			t.Errorf("expected payload imageSize 1K for gemini-2.5-flash-image, got %v", imgCfg["imageSize"])
		}
	})

	t.Run("gemini-3-pro-image - 2048x2048 generates 2K", func(t *testing.T) {
		req := &ImagesRequest{
			Model:  "gemini-3-pro-image",
			Prompt: "A portrait",
			Size:   "2048x2048",
		}
		geminiReq, _, err := adaptor.ToGeminiRequest(req, nil)
		if err != nil {
			t.Fatalf("ToGeminiRequest error: %v", err)
		}
		ic := geminiReq.GenerationConfig.ImageConfig
		if ic == nil || ic.ImageSize != "2K" {
			t.Errorf("expected ImageSize 2K for gemini-3-pro-image, got %v", ic)
		}
	})

	t.Run("gemini-3.1-flash-lite-image - 2048x2048 downgraded to 1K", func(t *testing.T) {
		req := &ImagesRequest{
			Model:  "gemini-3.1-flash-lite-image",
			Prompt: "A landscape",
			Size:   "2048x2048",
		}
		geminiReq, model, err := adaptor.ToGeminiRequest(req, nil)
		if err != nil {
			t.Fatalf("ToGeminiRequest error: %v", err)
		}
		// 2K is disallowed for lite-image, so ToGeminiRequest leaves ImageSize empty
		ic := geminiReq.GenerationConfig.ImageConfig
		if ic != nil && ic.ImageSize == "2K" {
			t.Errorf("2K should not be accepted for gemini-3.1-flash-lite-image")
		}
		// ResolveImageSize resolves fallback to 1K
		size := ResolveImageSize(ic.ImageSize, model)
		if size != "1K" {
			t.Errorf("expected resolved image size 1K for lite-image, got %q", size)
		}
	})

	t.Run("gemini-3.1-flash-image - 4096x4096 generates 4K", func(t *testing.T) {
		req := &ImagesRequest{
			Model:  "gemini-3.1-flash-image",
			Prompt: "A wide mountain view",
			Size:   "4096x4096",
		}
		geminiReq, _, err := adaptor.ToGeminiRequest(req, nil)
		if err != nil {
			t.Fatalf("ToGeminiRequest error: %v", err)
		}
		ic := geminiReq.GenerationConfig.ImageConfig
		if ic == nil || ic.ImageSize != "4K" {
			t.Errorf("expected ImageSize 4K for gemini-3.1-flash-image, got %v", ic)
		}
	})
}
