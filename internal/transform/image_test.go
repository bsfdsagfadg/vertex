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
			if got := AspectRatioAllowedFor(c.model, c.ratio); got != c.want {
				t.Errorf("AspectRatioAllowedFor(%q,%q)=%v，期望 %v", c.model, c.ratio, got, c.want)
			}
		})
	}
}

func TestOutputMimeTypeAllowedFor(t *testing.T) {
	cases := []struct {
		name  string
		model string
		mime  string
		want  bool
	}{
		{"flash-png", "gemini-3.1-flash-image", "image/png", true},
		{"flash-jpeg", "gemini-3.1-flash-image", "image/jpeg", true},
		{"flash-webp unsupported", "gemini-3.1-flash-image", "image/webp", false},
		{"pro-png", "gemini-3-pro-image", "image/png", true},
		{"pro-jpeg", "gemini-3-pro-image", "image/jpeg", true},
		{"unknown-png", "unknown-model", "image/png", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := OutputMimeTypeAllowedFor(c.model, c.mime); got != c.want {
				t.Errorf("OutputMimeTypeAllowedFor(%q,%q)=%v, want %v", c.model, c.mime, got, c.want)
			}
		})
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

		typedReq := BuildTypedImageRequest("gemini-2.5-flash-image", "A futuristic city", nil, nil, "1024x1024", "", "", "", "")
		if typedReq.GenerationConfig == nil || typedReq.GenerationConfig.ImageConfig == nil {
			t.Fatalf("expected generationConfig.imageConfig in BuildTypedImageRequest result")
		}
		if typedReq.GenerationConfig.ImageConfig.ImageSize != "1K" {
			t.Errorf("expected typedReq imageSize 1K for gemini-2.5-flash-image, got %v", typedReq.GenerationConfig.ImageConfig.ImageSize)
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

func TestSplitResponseParts_ImageMimeAndFallback(t *testing.T) {
	cases := []struct {
		name       string
		parts      []Part
		wantText   string
		wantImages int
	}{
		{
			name: "uppercase image mime",
			parts: []Part{
				{InlineData: &InlineData{MimeType: "IMAGE/JPEG", Data: "abc123"}},
			},
			wantText:   "\n![image](data:image/jpeg;base64,abc123)",
			wantImages: 1,
		},
		{
			name: "empty mime defaults to image/png",
			parts: []Part{
				{InlineData: &InlineData{MimeType: "", Data: "xyz789"}},
			},
			wantText:   "\n![image](data:image/png;base64,xyz789)",
			wantImages: 1,
		},
		{
			name: "non-image mime ignored",
			parts: []Part{
				{InlineData: &InlineData{MimeType: "audio/mp3", Data: "audio123"}},
			},
			wantText:   "",
			wantImages: 0,
		},
		{
			name: "mixed text and uppercase image",
			parts: []Part{
				{Text: "Here is your picture:"},
				{InlineData: &InlineData{MimeType: " Image/WebP ", Data: "webp456"}},
			},
			wantText:   "Here is your picture:\n![image](data:image/webp;base64,webp456)",
			wantImages: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text, _, _, images := SplitResponseParts(tc.parts)
			if text != tc.wantText {
				t.Errorf("text = %q, want %q", text, tc.wantText)
			}
			if len(images) != tc.wantImages {
				t.Errorf("len(images) = %d, want %d", len(images), tc.wantImages)
			}
		})
	}
}
