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

func TestApplyImageConfig(t *testing.T) {
	flashModel := "gemini-3.1-flash-image"
	liteModel := "gemini-3.1-flash-lite-image"
	proModel := "gemini-3-pro-image"

	// image_size 档位
	gp := map[string]any{}
	ApplyImageConfig(gp, map[string]any{"image_size": "2K"}, flashModel)
	if gp["generationConfig"].(map[string]any)["imageConfig"].(map[string]any)["imageSize"] != "2K" {
		t.Error("image_size=2K 未写入")
	}
	// 像素 → 档位
	gp2 := map[string]any{}
	ApplyImageConfig(gp2, map[string]any{"size": "2048x2048"}, flashModel)
	if _, has := gp2["generationConfig"].(map[string]any)["imageConfig"].(map[string]any)["imageSize"]; has {
		t.Error("size 不应推导 imageSize")
	}
	// imageConfig 顶层透传
	gp3 := map[string]any{}
	ApplyImageConfig(gp3, map[string]any{"imageConfig": map[string]any{"aspectRatio": "16:9"}}, flashModel)
	if gp3["generationConfig"].(map[string]any)["imageConfig"].(map[string]any)["aspectRatio"] != "16:9" {
		t.Error("imageConfig 透传失败")
	}
	// 不命中：不动 payload
	gp4 := map[string]any{}
	ApplyImageConfig(gp4, map[string]any{}, flashModel)
	if len(gp4) != 0 {
		t.Errorf("无分辨率参数时不应改 payload: %v", gp4)
	}
	// size="1024x1792" → aspectRatio="9:16", imageSize 不存在
	gp5 := map[string]any{}
	ApplyImageConfig(gp5, map[string]any{"size": "1024x1792"}, flashModel)
	gc5 := gp5["generationConfig"].(map[string]any)
	ic5 := gc5["imageConfig"].(map[string]any)
	if ic5["aspectRatio"] != "9:16" {
		t.Errorf("aspectRatio=%v，期望 9:16", ic5["aspectRatio"])
	}
	if _, has := ic5["imageSize"]; has {
		t.Error("size 不应推导 imageSize")
	}
	// size="2048x2048" → aspectRatio="1:1", imageSize 不存在
	gp6 := map[string]any{}
	ApplyImageConfig(gp6, map[string]any{"size": "2048x2048"}, flashModel)
	ic6 := gp6["generationConfig"].(map[string]any)["imageConfig"].(map[string]any)
	if ic6["aspectRatio"] != "1:1" {
		t.Errorf("aspectRatio=%v，期望 1:1", ic6["aspectRatio"])
	}
	if _, has := ic6["imageSize"]; has {
		t.Error("size 不应推导 imageSize")
	}
	// size="auto" → 不设 aspectRatio/imageSize
	gp7 := map[string]any{}
	ApplyImageConfig(gp7, map[string]any{"size": "auto"}, flashModel)
	if gc, ok := gp7["generationConfig"].(map[string]any); ok {
		if ic, ok := gc["imageConfig"].(map[string]any); ok {
			if _, has := ic["aspectRatio"]; has {
				t.Error("auto size 不应设 aspectRatio")
			}
			if _, has := ic["imageSize"]; has {
				t.Error("auto size 不应设 imageSize")
			}
		}
	}
	// lite-image：size="2048x2048" → aspectRatio="1:1"（支持），imageSize 不写入（size 不推导 imageSize）
	gp8 := map[string]any{}
	ApplyImageConfig(gp8, map[string]any{"size": "2048x2048"}, liteModel)
	ic8 := gp8["generationConfig"].(map[string]any)["imageConfig"].(map[string]any)
	if ic8["aspectRatio"] != "1:1" {
		t.Errorf("aspectRatio=%v，期望 1:1", ic8["aspectRatio"])
	}
	if _, has := ic8["imageSize"]; has {
		t.Error("lite-image 的 2K 应被过滤丢弃")
	}
	// lite-image：image_size="4K" → imageSize 不写入（4K 被过滤）
	gp9 := map[string]any{}
	ApplyImageConfig(gp9, map[string]any{"image_size": "4K"}, liteModel)
	if gc, ok := gp9["generationConfig"].(map[string]any); ok {
		if _, ok := gc["imageConfig"]; ok {
			t.Error("lite-image 的 4K 应被过滤丢弃，不应设 imageConfig")
		}
	}
	// pro-image：imageConfig.aspectRatio="auto" → passthrough 时 auto 被过滤丢弃
	gp10 := map[string]any{}
	ApplyImageConfig(gp10, map[string]any{"imageConfig": map[string]any{"aspectRatio": "auto"}}, proModel)
	if gc10, ok := gp10["generationConfig"].(map[string]any); ok {
		if ic10, ok := gc10["imageConfig"].(map[string]any); ok {
			if _, has := ic10["aspectRatio"]; has {
				t.Error("pro-image 不支持 auto，应被过滤")
			}
		}
	}
	// flash-image：imageConfig.aspectRatio="16:9" → passthrough 保留
	gp11 := map[string]any{}
	ApplyImageConfig(gp11, map[string]any{"imageConfig": map[string]any{"aspectRatio": "16:9"}}, flashModel)
	ic11 := gp11["generationConfig"].(map[string]any)["imageConfig"].(map[string]any)
	if ic11["aspectRatio"] != "16:9" {
		t.Errorf("aspectRatio=%v，期望 16:9", ic11["aspectRatio"])
	}
	// flash-image：imageConfig.imageSize="8K" → passthrough 被过滤丢弃
	gp12 := map[string]any{}
	ApplyImageConfig(gp12, map[string]any{"imageConfig": map[string]any{"imageSize": "8K"}}, flashModel)
	if gc12, ok := gp12["generationConfig"].(map[string]any); ok {
		if ic12, ok := gc12["imageConfig"].(map[string]any); ok {
			if _, has := ic12["imageSize"]; has {
				t.Error("8K 不应被透传")
			}
		}
	}
}
