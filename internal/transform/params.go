package transform

import (
	"strconv"
	"strings"
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

// ApplyImageConfig 原地把客户端分辨率/imageConfig 写入 geminiPayload.generationConfig.imageConfig。
// model 用于过滤该模型不支持的清晰度档位和长宽比。
func ApplyImageConfig(geminiPayload, body map[string]any, model string) {
	var imageSize string
	var aspectRatio string

	if raw, ok := body["imageConfig"].(map[string]any); ok && len(raw) > 0 {
		mergeImageConfig(geminiPayload, filterImageConfig(raw, model))
		return
	}

	for _, key := range []string{"image_size", "imageSize"} {
		if v, ok := body[key]; ok && v != nil {
			imageSize = normalizeImageSize(v)
			break
		}
	}

	if v, ok := body["size"]; ok && v != nil {
		if imageSize == "" {
			imageSize = sizeToImageSize(toString(v))
		}
		if ratio := sizeToAspectRatio(toString(v)); aspectRatioAllowedFor(model, ratio) {
			aspectRatio = ratio
		}
	}

	if imageSize != "" && !ImageSizeAllowedFor(model, imageSize) {
		imageSize = ""
	}
	if imageSize == "" && aspectRatio == "" {
		return
	}

	imageConfig := map[string]any{}
	if imageSize != "" {
		imageConfig["imageSize"] = imageSize
	}
	if aspectRatio != "" {
		imageConfig["aspectRatio"] = aspectRatio
	}
	mergeImageConfig(geminiPayload, imageConfig)
}

func filterImageConfig(raw map[string]any, model string) map[string]any {
	filtered := make(map[string]any, len(raw))
	for key, value := range raw {
		switch key {
		case "imageSize", "image_size":
			if tier := normalizeImageSize(value); ImageSizeAllowedFor(model, tier) {
				filtered["imageSize"] = tier
			}
		case "aspectRatio", "aspect_ratio":
			ratio := strings.TrimSpace(toString(value))
			if aspectRatioAllowedFor(model, ratio) {
				filtered["aspectRatio"] = ratio
			}
		default:
			filtered[key] = value
		}
	}
	return filtered
}

func mergeImageConfig(payload map[string]any, values map[string]any) {
	if len(values) == 0 {
		return
	}
	genCfg, ok := payload["generationConfig"].(map[string]any)
	if !ok {
		genCfg = map[string]any{}
		payload["generationConfig"] = genCfg
	}
	imageConfig, ok := genCfg["imageConfig"].(map[string]any)
	if !ok {
		imageConfig = map[string]any{}
		genCfg["imageConfig"] = imageConfig
	}
	for key, value := range values {
		imageConfig[key] = value
	}
}

// ApplyImageDefaults 仅对图模型补齐客户端没有显式指定的默认清晰度和输出模态。
func ApplyImageDefaults(payload map[string]any, model, defaultSize, defaultModalities string) {
	if !IsImageModel(model) {
		return
	}
	genCfg, ok := payload["generationConfig"].(map[string]any)
	if !ok {
		genCfg = map[string]any{}
		payload["generationConfig"] = genCfg
	}
	if !hasResponseModalities(genCfg["responseModalities"]) {
		if defaultModalities == "仅图片" {
			genCfg["responseModalities"] = []any{"IMAGE"}
		} else {
			genCfg["responseModalities"] = []any{"TEXT", "IMAGE"}
		}
	}
	imageConfig, ok := genCfg["imageConfig"].(map[string]any)
	if !ok {
		imageConfig = map[string]any{}
		genCfg["imageConfig"] = imageConfig
	}
	if size, _ := imageConfig["imageSize"].(string); strings.TrimSpace(size) == "" {
		imageConfig["imageSize"] = ResolveImageSize(defaultSize, model)
	}
}

func hasResponseModalities(value any) bool {
	switch modalities := value.(type) {
	case []any:
		return len(modalities) > 0
	case []string:
		return len(modalities) > 0
	default:
		return false
	}
}
