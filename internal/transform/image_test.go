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

	// pro-image：imageConfig.aspectRatio="auto" → passthrough 时 auto 被过滤丢弃
	ic10 := ResolveImageConfigPassthrough(map[string]any{"aspectRatio": "auto"}, proModel)
	if ic10 != nil && ic10.AspectRatio != "" {
		t.Error("pro-image 不支持 auto，应被过滤")
	}

	// flash-image：imageConfig.imageSize="8K" → passthrough 被过滤丢弃
	ic12 := ResolveImageConfigPassthrough(map[string]any{"imageSize": "8K"}, flashModel)
	if ic12 != nil && ic12.ImageSize != "" {
		t.Error("8K 不应被透传")
	}
}
