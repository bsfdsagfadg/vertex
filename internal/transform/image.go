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

type imageCapability struct {
	sizes  map[string]bool
	ratios map[string]bool
}

//nolint:gochecknoglobals // Read-only model capability table
var modelImageCapabilities = map[string]imageCapability{
	"gemini-3.1-flash-lite-image": {
		sizes:  map[string]bool{"1K": true},
		ratios: allImageRatios(),
	},
	"gemini-3.1-flash-image": {
		sizes:  map[string]bool{"512": true, "1K": true, "2K": true, "4K": true},
		ratios: allImageRatios(),
	},
	"gemini-3-pro-image": {
		sizes:  map[string]bool{"1K": true, "2K": true, "4K": true},
		ratios: standardImageRatios(),
	},
	"gemini-2.5-flash-image": {
		sizes:  map[string]bool{"1K": true, "2K": true, "4K": true},
		ratios: standardImageRatios(),
	},
}

//nolint:gochecknoglobals // Once-per-model diagnostic deduplication
var unknownImageModelWarned sync.Map

func allImageRatios() map[string]bool {
	return map[string]bool{
		"auto": true, "1:1": true, "3:2": true, "2:3": true, "3:4": true, "4:3": true,
		"4:5": true, "5:4": true, "9:16": true, "16:9": true, "1:4": true, "4:1": true,
		"1:8": true, "8:1": true, "21:9": true,
	}
}

func standardImageRatios() map[string]bool {
	return map[string]bool{
		"1:1": true, "3:2": true, "2:3": true, "3:4": true, "4:3": true,
		"4:5": true, "5:4": true, "9:16": true, "16:9": true, "21:9": true,
	}
}

func imageCapabilityFor(model string) imageCapability {
	if capability, ok := modelImageCapabilities[model]; ok {
		return capability
	}
	if _, loaded := unknownImageModelWarned.LoadOrStore(model, true); !loaded {
		log.Printf("[Image] 未知图模型 %q，清晰度按 1K、长宽比按通用集合处理", model)
	}
	return imageCapability{sizes: map[string]bool{"1K": true}, ratios: imageAspectRatioSupported}
}

func IsImageModel(model string) bool {
	if _, ok := modelImageCapabilities[model]; ok {
		return true
	}
	return strings.Contains(strings.ToLower(model), "image")
}

func ImageSizeAllowedFor(model, tier string) bool {
	return imageCapabilityFor(model).sizes[strings.ToUpper(strings.TrimSpace(tier))]
}

func aspectRatioAllowedFor(model, ratio string) bool {
	return imageCapabilityFor(model).ratios[strings.TrimSpace(ratio)]
}

func ResolveImageSize(defaultSize, model string) string {
	normalized := strings.ToUpper(strings.TrimSpace(defaultSize))
	if ImageSizeAllowedFor(model, normalized) {
		return normalized
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
//   - 按模型能力从 size 推导 aspectRatio / imageSize；默认输出模态由调用方按配置补齐。
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

	genCfg := map[string]any{}
	imageConfig := map[string]any{}
	if ar := sizeToAspectRatio(size); ar != "" && aspectRatioAllowedFor(model, ar) {
		imageConfig["aspectRatio"] = ar
	}
	if is := sizeToImageSize(size); is != "" && ImageSizeAllowedFor(model, is) {
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
