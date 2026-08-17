package transform

import (
	"log"
	"strconv"
	"strings"
	"sync"
)

// DefaultImageModel 取生产在用的 GA 图模型。
const DefaultImageModel = "gemini-3.1-flash-image"

// ImageModelSpec 描述单个生图模型的静态白名单能力规格。
type ImageModelSpec struct {
	MaxOutputTokens    int               // 输出 token 上限（全模型 32768）
	AllowTemperature   bool              // 是否支持 temperature（全模型 true）
	MaxTemperature     float64           // 温度上限（lite-image=1.0，其余 2.0）
	DefaultTemperature float64           // 温度缺省注入值（全模型 1.0）
	AllowTopP          bool              // 是否支持 topP（全模型 true）
	MaxTopP            float64           // topP 上限（全模型 1.0）
	DefaultTopP        float64           // topP 缺省注入值（全模型 0.95）
	SupportedSizes     map[string]bool
	SupportedRatios    map[string]bool
	SupportedMimes     map[string]bool
	DefaultSize        string
	DefaultRatio       string
	AllowAutoRatio     bool
	AllowSearch        bool
	ThinkingLevels     map[string]bool
	SupportsThinking   bool
}

// imageModelSpecs 是 4 大生图模型权威能力规格白名单矩阵（源自 logs/image模型参数清单.txt）。
//
//nolint:gochecknoglobals // Read-only capability specs
var imageModelSpecs = map[string]ImageModelSpec{
	"gemini-3.1-flash-lite-image": {
		MaxOutputTokens:    32768,
		AllowTemperature:   true,
		MaxTemperature:     1.0,
		DefaultTemperature: 1.0,
		AllowTopP:          true,
		MaxTopP:            1.0,
		DefaultTopP:        0.95,
		SupportedSizes:     map[string]bool{"1K": true},
		SupportedRatios:    map[string]bool{"auto": true, "1:1": true, "3:2": true, "2:3": true, "3:4": true, "4:3": true, "4:5": true, "5:4": true, "9:16": true, "16:9": true, "1:4": true, "4:1": true, "1:8": true, "8:1": true, "21:9": true},
		SupportedMimes:     map[string]bool{"image/png": true, "image/jpeg": true},
		DefaultSize:        "1K",
		DefaultRatio:       "auto",
		AllowAutoRatio:     true,
		AllowSearch:        false,
		ThinkingLevels:     map[string]bool{"MINIMAL": true, "HIGH": true},
		SupportsThinking:   true,
	},
	"gemini-3.1-flash-image": {
		MaxOutputTokens:    32768,
		AllowTemperature:   true,
		MaxTemperature:     2.0,
		DefaultTemperature: 1.0,
		AllowTopP:          true,
		MaxTopP:            1.0,
		DefaultTopP:        0.95,
		SupportedSizes:     map[string]bool{"512": true, "1K": true, "2K": true, "4K": true},
		SupportedRatios:    map[string]bool{"auto": true, "1:1": true, "3:2": true, "2:3": true, "3:4": true, "4:3": true, "4:5": true, "5:4": true, "9:16": true, "16:9": true, "1:4": true, "4:1": true, "1:8": true, "8:1": true, "21:9": true},
		SupportedMimes:     map[string]bool{"image/png": true, "image/jpeg": true},
		DefaultSize:        "1K",
		DefaultRatio:       "auto",
		AllowAutoRatio:     true,
		AllowSearch:        true,
		ThinkingLevels:     map[string]bool{"MINIMAL": true, "HIGH": true},
		SupportsThinking:   true,
	},
	"gemini-3-pro-image": {
		MaxOutputTokens:    32768,
		AllowTemperature:   true,
		MaxTemperature:     2.0,
		DefaultTemperature: 1.0,
		AllowTopP:          true,
		MaxTopP:            1.0,
		DefaultTopP:        0.95,
		SupportedSizes:     map[string]bool{"1K": true, "2K": true, "4K": true},
		SupportedRatios:    map[string]bool{"1:1": true, "3:2": true, "2:3": true, "3:4": true, "4:3": true, "4:5": true, "5:4": true, "9:16": true, "16:9": true, "21:9": true},
		SupportedMimes:     map[string]bool{"image/png": true, "image/jpeg": true},
		DefaultSize:        "1K",
		DefaultRatio:       "1:1",
		AllowAutoRatio:     false,
		AllowSearch:        true,
		ThinkingLevels:     nil,
		SupportsThinking:   false,
	},
	"gemini-2.5-flash-image": {
		MaxOutputTokens:    32768,
		AllowTemperature:   true,
		MaxTemperature:     2.0,
		DefaultTemperature: 1.0,
		AllowTopP:          true,
		MaxTopP:            1.0,
		DefaultTopP:        0.95,
		SupportedSizes:     map[string]bool{"1K": true, "2K": true, "4K": true},
		SupportedRatios:    map[string]bool{"1:1": true, "3:2": true, "2:3": true, "3:4": true, "4:3": true, "4:5": true, "5:4": true, "9:16": true, "16:9": true, "21:9": true},
		SupportedMimes:     map[string]bool{"image/png": true, "image/jpeg": true},
		DefaultSize:        "1K",
		DefaultRatio:       "1:1",
		AllowAutoRatio:     false,
		AllowSearch:        false,
		ThinkingLevels:     nil,
		SupportsThinking:   false,
	},
}

// unknownModelWarned 避免同一未知图模型重复刷日志。
//
//nolint:gochecknoglobals
var unknownModelWarned sync.Map

// ImageSpecFor 获取指定图模型的白名单规格（遵循 Go 命名规范，无 Get 前缀）。
func ImageSpecFor(model string) ImageModelSpec {
	if spec, ok := imageModelSpecs[model]; ok {
		return spec
	}
	if _, loaded := unknownModelWarned.LoadOrStore(model, true); !loaded {
		log.Printf("[Image] 未知图模型 %q，按保守默认（sizes={1K}, ratios=全允许, 不支持搜索与思考）处理", model)
	}
	return ImageModelSpec{
		MaxOutputTokens:    32768,
		AllowTemperature:   true,
		MaxTemperature:     2.0,
		DefaultTemperature: 1.0,
		AllowTopP:          true,
		MaxTopP:            1.0,
		DefaultTopP:        0.95,
		SupportedSizes:     map[string]bool{"1K": true},
		SupportedRatios:    imageAspectRatioSupported,
		SupportedMimes:     map[string]bool{"image/png": true, "image/jpeg": true},
		DefaultSize:        "1K",
		DefaultRatio:       "1:1",
		AllowAutoRatio:     false,
		AllowSearch:        false,
		ThinkingLevels:     nil,
		SupportsThinking:   false,
	}
}

// GetImageModelSpec 获取指定图模型的白名单规格（保留为 ImageSpecFor 的兼容包装函数）。
func GetImageModelSpec(model string) ImageModelSpec {
	return ImageSpecFor(model)
}

// ImageSizeAllowedFor 模型是否支持某档位。
func ImageSizeAllowedFor(model, tier string) bool {
	spec := GetImageModelSpec(model)
	return spec.SupportedSizes[tier]
}

// IsImageModel 判断模型是否为图像模型（在能力表中，或者模型名包含 "image" / "imagen"）。
func IsImageModel(model string) bool {
	if _, ok := imageModelSpecs[model]; ok {
		return true
	}
	lower := strings.ToLower(model)
	return strings.Contains(lower, "image") || strings.Contains(lower, "imagen")
}

// AspectRatioAllowedFor 模型是否支持某比例。
func AspectRatioAllowedFor(model, ratio string) bool {
	spec := GetImageModelSpec(model)
	return spec.SupportedRatios[ratio]
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

// OutputMimeTypeAllowedFor 模型是否支持某输出图片格式。
func OutputMimeTypeAllowedFor(model, mime string) bool {
	spec := GetImageModelSpec(model)
	return spec.SupportedMimes[strings.ToLower(strings.TrimSpace(mime))]
}

// InlineImage 是一张上传图片的 inlineData 结构（mimeType + base64 data）。
// 内联图片上传的返回结构。
type InlineImage struct {
	MimeType string
	Data     string
}

// BuildTypedImageRequest 构建图片生成/编辑/变体的 Gemini 强类型请求。
//
//   - prompt 经 buildImagePrompt 拼接尺寸/质量/风格/背景等自然语言约束。
//   - images（编辑/变体的输入图）与 mask（编辑遮罩）以 inlineData 追加（base64 规范化）。
//   - generationConfig.responseModalities 预置为 ["IMAGE"]；按 size 推 aspectRatio / imageSize。
func BuildTypedImageRequest(model, prompt string, images []InlineImage, mask *InlineImage, size, quality, style, background, mode string) *GeminiRequest {
	promptText := buildImagePrompt(prompt, size, quality, style, background, mode, mask != nil)

	parts := []Part{{Text: promptText}}
	for _, img := range images {
		if img.Data != "" && img.MimeType != "" {
			parts = append(parts, Part{
				InlineData: &InlineData{
					MimeType: img.MimeType,
					// DTO 层已归一（InlineData.UnmarshalJSON），此处为幂等防御
					Data: NormalizeBase64(img.Data),
				},
			})
		}
	}
	if mask != nil && mask.Data != "" && mask.MimeType != "" {
		parts = append(parts, Part{Text: "Use the following image as the edit mask when applying the requested change."})
		parts = append(parts, Part{
			InlineData: &InlineData{
				MimeType: mask.MimeType,
				// DTO 层已归一（InlineData.UnmarshalJSON），此处为幂等防御
				Data: NormalizeBase64(mask.Data),
			},
		})
	}

	req := &GeminiRequest{
		Contents: []Content{{Role: "user", Parts: parts}},
		GenerationConfig: &GenerationConfig{
			ResponseModalities: []string{"IMAGE"},
		},
	}

	ic := &ImageConfig{}
	if ar := sizeToAspectRatio(size); ar != "" {
		ic.AspectRatio = ar
	}
	if is := sizeToImageSize(size); is != "" && ImageSizeAllowedFor(model, is) {
		ic.ImageSize = is
	}
	if ic.AspectRatio != "" || ic.ImageSize != "" || ic.OutputMimeType != "" {
		req.GenerationConfig.ImageConfig = ic
	}

	return req
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

// sizeToAspectRatio 把分辨率字符串（WxH 或常见预设）映射到 Gemini aspectRatio：
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


