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

func TestBuildTypedImageRequest_All4Models(t *testing.T) {
	t.Run("gemini-2.5-flash-image - 1024x1024 generates 1K and 1:1", func(t *testing.T) {
		typedReq := BuildTypedImageRequest("gemini-2.5-flash-image", "A futuristic city", nil, nil, "1024x1024", "", "", "", "")
		if typedReq.GenerationConfig == nil || typedReq.GenerationConfig.ImageConfig == nil {
			t.Fatalf("expected generationConfig.imageConfig in BuildTypedImageRequest result")
		}
		if typedReq.GenerationConfig.ImageConfig.ImageSize != "1K" {
			t.Errorf("expected typedReq imageSize 1K for gemini-2.5-flash-image, got %v", typedReq.GenerationConfig.ImageConfig.ImageSize)
		}
		if typedReq.GenerationConfig.ImageConfig.AspectRatio != "1:1" {
			t.Errorf("expected AspectRatio 1:1, got %q", typedReq.GenerationConfig.ImageConfig.AspectRatio)
		}
	})

	t.Run("gemini-3-pro-image - 2048x2048 generates 2K", func(t *testing.T) {
		typedReq := BuildTypedImageRequest("gemini-3-pro-image", "A portrait", nil, nil, "2048x2048", "", "", "", "")
		ic := typedReq.GenerationConfig.ImageConfig
		if ic == nil || ic.ImageSize != "2K" {
			t.Errorf("expected ImageSize 2K for gemini-3-pro-image, got %v", ic)
		}
	})

	t.Run("gemini-3.1-flash-lite-image - 2048x2048 downgraded to 1K", func(t *testing.T) {
		typedReq := BuildTypedImageRequest("gemini-3.1-flash-lite-image", "A landscape", nil, nil, "2048x2048", "", "", "", "")
		ic := typedReq.GenerationConfig.ImageConfig
		if ic != nil && ic.ImageSize == "2K" {
			t.Errorf("2K should not be accepted for gemini-3.1-flash-lite-image")
		}
		size := ResolveImageSize(ic.ImageSize, "gemini-3.1-flash-lite-image")
		if size != "1K" {
			t.Errorf("expected resolved image size 1K for lite-image, got %q", size)
		}
	})

	t.Run("gemini-3.1-flash-image - 4096x4096 generates 4K", func(t *testing.T) {
		typedReq := BuildTypedImageRequest("gemini-3.1-flash-image", "A wide mountain view", nil, nil, "4096x4096", "", "", "", "")
		ic := typedReq.GenerationConfig.ImageConfig
		if ic == nil || ic.ImageSize != "4K" {
			t.Errorf("expected ImageSize 4K for gemini-3.1-flash-image, got %v", ic)
		}
	})
}
