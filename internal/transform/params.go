package transform

import (
	"log"
	"strconv"
	"strings"
	"sync"
)

// reasoningEffortToThinkingLevel 把 OpenAI reasoning_effort 映射到 Gemini 3.x
// thinkingConfig.thinkingLevel。
//
//nolint:gochecknoglobals // Read-only mapping
var reasoningEffortToThinkingLevel = map[string]string{
	"none":    "NONE",
	"minimal": "MINIMAL",
	"low":     "LOW",
	"medium":  "MEDIUM",
	"high":    "HIGH",
	"xhigh":   "HIGH",
}

// audioFormatMIME 把 input_audio.format 映射到 Gemini inlineData mimeType。
//
//nolint:gochecknoglobals // Read-only mapping
var audioFormatMIME = map[string]string{
	"wav":  "audio/wav",
	"mp3":  "audio/mpeg",
	"mpeg": "audio/mpeg",
	"mpga": "audio/mpeg",
	"m4a":  "audio/aac",
	"aac":  "audio/aac",
	"ogg":  "audio/ogg",
	"oga":  "audio/ogg",
	"opus": "audio/ogg",
	"flac": "audio/flac",
	"webm": "audio/webm",
	"pcm":  "audio/L16",
	"l16":  "audio/L16",
}

// imageSizeAllowed 是 Gemini imageConfig.imageSize 接受的档位。
//
//nolint:gochecknoglobals // Read-only set
var imageSizeAllowed = map[string]bool{"512": true, "1K": true, "2K": true, "4K": true}

// pixelToImageSize 把像素长边映射到档位。
//
//nolint:gochecknoglobals // Read-only mapping
var pixelToImageSize = []struct { //nolint:govet
	threshold int
	tier      string
}{
	{4096, "4K"},
	{2048, "2K"},
	{1024, "1K"},
	{512, "512"},
}

// mediaResolutionAllowed 是 generationConfig.mediaResolution 的完整枚举集合。
//
//nolint:gochecknoglobals // Read-only set
var mediaResolutionAllowed = map[string]bool{
	"MEDIA_RESOLUTION_UNSPECIFIED": true,
	"MEDIA_RESOLUTION_LOW":         true,
	"MEDIA_RESOLUTION_MEDIUM":      true,
	"MEDIA_RESOLUTION_HIGH":        true,
	"MEDIA_RESOLUTION_ULTRA_HIGH":  true,
}

// mediaResolutionShorthand 接受简写并归一到完整枚举。
//
//nolint:gochecknoglobals // Read-only mapping
var mediaResolutionShorthand = map[string]string{
	"low":         "MEDIA_RESOLUTION_LOW",
	"medium":      "MEDIA_RESOLUTION_MEDIUM",
	"med":         "MEDIA_RESOLUTION_MEDIUM",
	"high":        "MEDIA_RESOLUTION_HIGH",
	"unspecified": "MEDIA_RESOLUTION_UNSPECIFIED",
	"default":     "MEDIA_RESOLUTION_UNSPECIFIED",
	"ultra_high":  "MEDIA_RESOLUTION_ULTRA_HIGH",
	"ultra-high":  "MEDIA_RESOLUTION_ULTRA_HIGH",
	"ultrahigh":   "MEDIA_RESOLUTION_ULTRA_HIGH",
}

// normalizeMediaResolution 把任意写法规范成 Gemini 枚举，无法识别返回 ""。
func normalizeMediaResolution(value any) string {
	s, ok := value.(string)
	if !ok {
		return ""
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	upper := strings.ToUpper(s)
	if mediaResolutionAllowed[upper] {
		return upper
	}
	if strings.HasPrefix(upper, "MEDIA_RESOLUTION_") {
		return upper
	}
	return mediaResolutionShorthand[strings.ToLower(s)]
}

// normalizeImageSize 把任意分辨率输入规范成档位字符串（512/1K/2K/4K）或 ""。
func normalizeImageSize(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case float64:
		return pixelsToTier(int(v))
	case int:
		return pixelsToTier(v)
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return ""
		}
		if imageSizeAllowed[strings.ToUpper(s)] {
			return strings.ToUpper(s)
		}
		low := strings.ToLower(s)
		if strings.Contains(low, "x") {
			parts := strings.SplitN(low, "x", 2)
			if len(parts) >= 2 {
				w, errW := strconv.Atoi(strings.TrimSpace(parts[0]))
				h, errH := strconv.Atoi(strings.TrimSpace(parts[1]))
				if errW != nil || errH != nil {
					return ""
				}
				return pixelsToTier(maxInt(w, h))
			}
		}
		if isAllDigits(s) {
			n, err := strconv.Atoi(s)
			if err != nil {
				return ""
			}
			return pixelsToTier(n)
		}
		return ""
	default:
		return ""
	}
}

// pixelsToTier 把像素长边映射到不超过它的最大档位；<512 返回 ""。
func pixelsToTier(px int) string {
	for _, p := range pixelToImageSize {
		if px >= p.threshold {
			return p.tier
		}
	}
	return ""
}

// sizeDeprecationOnce 确保 size→imageSize 弃用日志只打印一次。
//
//nolint:gochecknoglobals // One-time deprecation warning
var sizeDeprecationOnce sync.Once

// ApplyResponseModalities 处理 generationConfig.responseModalities。
//
// 仅对图像模型生效：若客户端已设置 responseModalities（非空）则跳过；
// 否则根据 defaultModalities 注入默认值。
// defaultModalities == "IMAGE_ONLY" 时注入 ["IMAGE"]，否则注入 ["TEXT", "IMAGE"]。
func ApplyResponseModalities(geminiPayload map[string]any, defaultModalities, model string) {
	if !strings.Contains(strings.ToLower(model), "image") {
		return
	}

	genCfg, ok := geminiPayload["generationConfig"].(map[string]any)
	if ok {
		if rm, exists := genCfg["responseModalities"]; exists && rm != nil {
			if arr, isArr := rm.([]any); isArr && len(arr) > 0 {
				return
			}
			if arr, isStr := rm.([]string); isStr && len(arr) > 0 {
				return
			}
		}
	}

	if !ok {
		genCfg = map[string]any{}
		geminiPayload["generationConfig"] = genCfg
	}

	if defaultModalities == "仅图片" {
		genCfg["responseModalities"] = []any{"IMAGE"}
	} else {
		genCfg["responseModalities"] = []any{"TEXT", "IMAGE"}
	}
}

// ApplyImageConfig 原地把客户端分辨率/imageConfig 写入 geminiPayload.generationConfig.imageConfig。
// model 用于按模型能力过滤不支持的档位/比例。
func ApplyImageConfig(geminiPayload, body map[string]any, model string) {
	var imageSize string
	var passthrough map[string]any

	if raw, ok := body["imageConfig"].(map[string]any); ok && len(raw) > 0 {
		passthrough = raw
	}

	if passthrough == nil {
		for _, key := range []string{"image_size", "imageSize"} {
			if v, ok := body[key]; ok && v != nil {
				if tier := normalizeImageSize(v); tier != "" {
					imageSize = tier
					break
				}
			}
		}
		_, hasSize := body["size"]
		if imageSize == "" && !hasSize {
			return
		}
		if imageSize == "" && hasSize {
			if v, ok := body["size"]; ok && v != nil {
				sizeDeprecationOnce.Do(func() {
					log.Printf("[WARN] size=%q 映射已弃用，请改用 image_size 或配置 default_image_size 获得清晰度控制", toString(v))
				})
			}
		}
	}

	genCfg, ok := geminiPayload["generationConfig"].(map[string]any)
	if !ok {
		genCfg = map[string]any{}
		geminiPayload["generationConfig"] = genCfg
	}

	if passthrough != nil {
		if existing, ok := genCfg["imageConfig"].(map[string]any); ok {
			for k, v := range filterPassthroughByModel(passthrough, model) {
				existing[k] = v
			}
		} else {
			filtered := filterPassthroughByModel(passthrough, model)
			if len(filtered) > 0 {
				genCfg["imageConfig"] = filtered
			}
		}
		return
	}

	// 按模型能力过滤 imageSize
	if !ImageSizeAllowedFor(model, imageSize) {
		imageSize = ""
	}

	// 从 size 推导 aspectRatio（不覆盖客户端已设值）
	var aspectRatio string
	if v, ok := body["size"]; ok && v != nil {
		if ar := sizeToAspectRatio(toString(v)); ar != "" && aspectRatioAllowedFor(model, ar) {
			aspectRatio = ar
		}
	}

	if imageSize == "" && aspectRatio == "" {
		return
	}

	imgCfg, ok := genCfg["imageConfig"].(map[string]any)
	if !ok {
		imgCfg = map[string]any{}
		genCfg["imageConfig"] = imgCfg
	}

	if imageSize != "" {
		imgCfg["imageSize"] = imageSize
	}
	if aspectRatio != "" {
		if _, has := imgCfg["aspectRatio"]; !has {
			imgCfg["aspectRatio"] = aspectRatio
		}
	}
}

// filterPassthroughByModel 按模型能力过滤 passthrough 中不支持的档位/比例键。
func filterPassthroughByModel(passthrough map[string]any, model string) map[string]any {
	filtered := copyMap(passthrough)
	for k, v := range passthrough {
		switch k {
		case "imageSize", "image_size":
			if s, _ := v.(string); s != "" && !ImageSizeAllowedFor(model, s) {
				delete(filtered, k)
			}
		case "aspectRatio", "aspect_ratio":
			if s, _ := v.(string); s != "" && !aspectRatioAllowedFor(model, s) {
				delete(filtered, k)
			}
		}
	}
	return filtered
}
