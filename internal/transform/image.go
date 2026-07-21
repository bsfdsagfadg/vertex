package transform

import (
	"log"
	"strconv"
	"strings"
	"sync"
)

// 本文件实现 OpenAI Images API → Gemini 图片请求转换，及其辅助（追加 negative
// prompt / 构建 image prompt / size→aspectRatio / size→imageSize）+ 从 Gemini
// 响应抽取 OpenAI Images data。
// 供 /v1/images/generations、/v1/images/edits、/v1/images/variations 端点用。

// DefaultImageModel 取生产在用的 GA 图模型；OpenAI 图模型别名统一回退到它。
const DefaultImageModel = "gemini-3.1-flash-image"

// openaiImageModelAliases 是会被回退到 DefaultImageModel 的 OpenAI 图模型名。
//
//nolint:gochecknoglobals // Read-only alias map
var openaiImageModelAliases = map[string]bool{
	"gpt-image-1": true, "dall-e-2": true, "dall-e-3": true,
}

// imageCapability 描述一个图模型支持的 imageSize 档位与 aspectRatio 集合。
type imageCapability struct {
	sizes  map[string]bool
	ratios map[string]bool
}

// modelImageCapabilities 是已知图模型的能力矩阵（源自 Google 反代页调研）。
// 未命中表项的模型按保守默认：sizes={1K}，ratios=空（让 ApplyImageConfig 走推导）。
//
//nolint:gochecknoglobals // Read-only capability table
var modelImageCapabilities = map[string]imageCapability{
	"gemini-3.1-flash-lite-image": {
		sizes:  map[string]bool{"1K": true},
		ratios: map[string]bool{"auto": true, "1:1": true, "3:2": true, "2:3": true, "3:4": true, "4:3": true, "4:5": true, "5:4": true, "9:16": true, "16:9": true, "1:4": true, "4:1": true, "1:8": true, "8:1": true, "21:9": true},
	},
	"gemini-3.1-flash-image": {
		sizes:  map[string]bool{"512": true, "1K": true, "2K": true, "4K": true},
		ratios: map[string]bool{"auto": true, "1:1": true, "3:2": true, "2:3": true, "3:4": true, "4:3": true, "4:5": true, "5:4": true, "9:16": true, "16:9": true, "1:4": true, "4:1": true, "1:8": true, "8:1": true, "21:9": true},
	},
	"gemini-3-pro-image": {
		sizes:  map[string]bool{"1K": true, "2K": true, "4K": true},
		ratios: map[string]bool{"1:1": true, "3:2": true, "2:3": true, "3:4": true, "4:3": true, "4:5": true, "5:4": true, "9:16": true, "16:9": true, "21:9": true},
	},
	"gemini-2.5-flash-image": {
		sizes:  map[string]bool{"1K": true, "2K": true, "4K": true},
		ratios: map[string]bool{"1:1": true, "3:2": true, "2:3": true, "3:4": true, "4:3": true, "4:5": true, "5:4": true, "9:16": true, "16:9": true, "21:9": true},
	},
}

// unknownModelWarned 避免为同一未知模型重复打日志。
//
//nolint:gochecknoglobals // Once-per-model dedup
var unknownModelWarned sync.Map

// imageCapabilityFor 返回模型能力；未知模型 sizes 保守为仅 1K，ratios 默认全允许。
func imageCapabilityFor(model string) imageCapability {
	if c, ok := modelImageCapabilities[model]; ok {
		return c
	}
	if _, loaded := unknownModelWarned.LoadOrStore(model, true); !loaded {
		log.Printf("[Image] 未知图模型 %q，sizes 按保守默认（仅 1K）处理，ratios 全允许", model)
	}
	return imageCapability{sizes: map[string]bool{"1K": true}, ratios: imageAspectRatioSupported}
}

// ImageSizeAllowedFor 模型是否支持某档位。
func ImageSizeAllowedFor(model, tier string) bool {
	return imageCapabilityFor(model).sizes[tier]
}

// aspectRatioAllowedFor 模型是否支持某比例。
func aspectRatioAllowedFor(model, ratio string) bool {
	return imageCapabilityFor(model).ratios[ratio]
}

// ResolveImageSize 返回模型可用的 imageSize：优先使用配置值，不合法则回退到 1K。
func ResolveImageSize(defaultSize, model string) string {
	if ImageSizeAllowedFor(model, defaultSize) {
		return defaultSize
	}
	return "1K"
}

// imageAspectRatioSupported 是 Gemini imageConfig.aspectRatio 接受的比例集合。
//
//nolint:gochecknoglobals // Read-only set
var imageAspectRatioSupported = map[string]bool{
	"auto": true, "1:1": true, "3:2": true, "2:3": true, "3:4": true, "4:3": true,
	"4:5": true, "5:4": true, "9:16": true, "16:9": true,
	"1:4": true, "4:1": true, "1:8": true, "8:1": true, "21:9": true,
}

// InlineImage 是一张上传图片的 inlineData 结构（mimeType + base64 data）。
// 内联图片上传的返回结构。
type InlineImage struct {
	MimeType string
	Data     string
}

// ResolveImageModel 解析图片模型名：空/OpenAI 别名 → DefaultImageModel，其余原样。
func ResolveImageModel(model string) string {
	if model == "" {
		return DefaultImageModel
	}
	if openaiImageModelAliases[model] {
		return DefaultImageModel
	}
	return model
}

// BuildImagePayload 构建图片生成/编辑/变体的 Gemini payload。
//
//   - prompt 经 buildImagePrompt 拼接尺寸/质量/风格/背景等自然语言约束。
//   - images（编辑/变体的输入图）与 mask（编辑遮罩）以 inlineData 追加（base64 规范化）。
//   - generationConfig.responseModalities=["TEXT","IMAGE"]；按 size 推 aspectRatio / imageSize
//     （imageSize 仅 gemini-3 系列设置）。
func BuildImagePayload(model, prompt string, images []InlineImage, mask *InlineImage, size, quality, style, background, mode string) map[string]any {
	promptText := buildImagePrompt(prompt, size, quality, style, background, mode, mask != nil)

	parts := []any{map[string]any{"text": promptText}}
	for _, img := range images {
		if img.Data != "" && img.MimeType != "" {
			parts = append(parts, map[string]any{"inlineData": map[string]any{
				"mimeType": img.MimeType, "data": NormalizeBase64(img.Data),
			}})
		}
	}
	if mask != nil && mask.Data != "" && mask.MimeType != "" {
		parts = append(parts, map[string]any{"text": "Use the following image as the edit mask when applying the requested change."})
		parts = append(parts, map[string]any{"inlineData": map[string]any{
			"mimeType": mask.MimeType, "data": NormalizeBase64(mask.Data),
		}})
	}

	genCfg := map[string]any{"responseModalities": []any{"TEXT", "IMAGE"}}
	imageConfig := map[string]any{}
	if ar := sizeToAspectRatio(size); ar != "" {
		imageConfig["aspectRatio"] = ar
	}
	if is := sizeToImageSize(size); is != "" && strings.Contains(model, "gemini-3") {
		imageConfig["imageSize"] = is
	}
	if len(imageConfig) > 0 {
		genCfg["imageConfig"] = imageConfig
	}

	return map[string]any{
		"contents":         []any{map[string]any{"role": "user", "parts": parts}},
		"generationConfig": genCfg,
	}
}

// AppendNegativePrompt 把 negative_prompt 追加成 "Avoid: ..." 行。
func AppendNegativePrompt(prompt string, negative any) string {
	neg := toString(negative)
	if strings.TrimSpace(neg) == "" {
		return prompt
	}
	return strings.TrimSpace(strings.TrimSpace(prompt) + "\nAvoid: " + neg)
}

// buildImagePrompt 把尺寸/质量/风格/背景/模式约束拼进 prompt。
func buildImagePrompt(prompt, size, quality, style, background, mode string, hasMask bool) string {
	lines := []string{strings.TrimSpace(prompt)}
	switch mode {
	case "edit":
		lines = append(lines, "Edit the provided image according to the prompt while preserving unaffected details.")
	case "variation":
		lines = append(lines, "Create a faithful variation of the provided image.")
	}
	if hasMask {
		lines = append(lines, "Respect the provided mask as the editable region.")
	}
	if appendable(size) {
		lines = append(lines, "Target output size/aspect: "+size+".")
	}
	if appendable(quality) {
		lines = append(lines, "Quality preference: "+quality+".")
	}
	if appendable(style) {
		lines = append(lines, "Style preference: "+style+".")
	}
	if appendable(background) {
		lines = append(lines, "Background preference: "+background+".")
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// appendable 判断一个可选参数是否应拼进 prompt（非空且非 "auto"）。
func appendable(v string) bool {
	return v != "" && strings.ToLower(v) != "auto"
}

// sizeToAspectRatio 把 OpenAI size（WxH 或常见预设）映射到 Gemini aspectRatio：
// 先匹配预设，再用约分 GCD 推比例，不在支持集合内返回 ""。
func sizeToAspectRatio(size string) string {
	if size == "" {
		return ""
	}
	value := strings.ToLower(strings.TrimSpace(size))
	if value == "auto" || value == "" {
		return ""
	}
	switch value {
	case "1024x1024", "1536x1536":
		return "1:1"
	case "1536x1024":
		return "3:2"
	case "1792x1024":
		return "16:9"
	case "1024x1536":
		return "2:3"
	case "1024x1792":
		return "9:16"
	}
	w, h, ok := parseWxH(value)
	if !ok || w <= 0 || h <= 0 {
		return ""
	}
	g := gcd(w, h)
	ratio := strconv.Itoa(w/g) + ":" + strconv.Itoa(h/g)
	if imageAspectRatioSupported[ratio] {
		return ratio
	}
	return ""
}

// sizeToImageSize 把 size 的较大边映射到 1K/2K/4K。
func sizeToImageSize(size string) string {
	if size == "" {
		return ""
	}
	value := strings.ToLower(strings.TrimSpace(size))
	w, h, ok := parseWxH(value)
	if !ok {
		return ""
	}
	maxSide := maxInt(w, h)
	switch {
	case maxSide >= 3000:
		return "4K"
	case maxSide >= 1500:
		return "2K"
	default:
		return "1K"
	}
}

// parseWxH 解析 "WxH" 字符串，返回 (w, h, ok)。
func parseWxH(value string) (int, int, bool) {
	parts := strings.SplitN(value, "x", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	w, errW := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, errH := strconv.Atoi(strings.TrimSpace(parts[1]))
	if errW != nil || errH != nil {
		return 0, 0, false
	}
	return w, h, true
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	if a < 0 {
		return -a
	}
	return a
}

// GeminiToImageData 从 Gemini 响应抽取 OpenAI Images API 的 data 数组。
// response_format=="url" 时输出 {"url":"data:mime;base64,..."}，否则 {"b64_json":...}。
func GeminiToImageData(geminiResp map[string]any, responseFormat string) []any {
	var items []any
	for _, part := range iterResponseParts(geminiResp) {
		mime, b64, ok := extractImageFromPart(part)
		if !ok {
			continue
		}
		if responseFormat == "url" {
			items = append(items, map[string]any{"url": "data:" + mime + ";base64," + b64})
		} else {
			items = append(items, map[string]any{"b64_json": b64})
		}
	}
	return items
}

// iterResponseParts 收集所有 candidate.content.parts。
func iterResponseParts(resp map[string]any) []map[string]any {
	var parts []map[string]any
	cands, _ := resp["candidates"].([]any)
	for _, cRaw := range cands {
		c, ok := cRaw.(map[string]any)
		if !ok {
			continue
		}
		content, ok := c["content"].(map[string]any)
		if !ok {
			continue
		}
		ps, _ := content["parts"].([]any)
		for _, pRaw := range ps {
			if p, ok := pRaw.(map[string]any); ok {
				parts = append(parts, p)
			}
		}
	}
	return parts
}

// extractImageFromPart 从一个 part 抽取 (mime, base64data)：
// 优先 inlineData，其次 text 里嵌的 data:image/ markdown 链接。
func extractImageFromPart(part map[string]any) (string, string, bool) {
	if inline, ok := firstMap(part["inlineData"], part["inline_data"]); ok {
		mime := toString(firstNonEmpty(inline["mimeType"], inline["mime_type"]))
		if mime == "" {
			mime = "image/png"
		}
		if data, ok := inline["data"].(string); ok && strings.TrimSpace(data) != "" {
			return mime, NormalizeBase64(data), true
		}
	}
	if text, ok := part["text"].(string); ok && strings.Contains(text, "data:image/") {
		marker := "data:image/"
		start := strings.Index(text, marker)
		end := strings.Index(text[start:], ")")
		var dataURL string
		if end == -1 {
			dataURL = text[start:]
		} else {
			dataURL = text[start : start+end]
		}
		mime, b64 := parseDataURI(dataURL)
		if mime != "" && b64 != "" {
			return mime, NormalizeBase64(b64), true
		}
	}
	return "", "", false
}

// firstMap 返回第一个非空 map（用于 inlineData/inline_data 兼容）。
func firstMap(vals ...any) (map[string]any, bool) {
	for _, v := range vals {
		if m, ok := v.(map[string]any); ok && len(m) > 0 {
			return m, true
		}
	}
	return nil, false
}
