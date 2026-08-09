package api

import (
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/transform"
)

// ModelStrategy 是单个模型家族的载荷增强策略：每个家族只写自己该有的增强，
// 结构上杜绝跨家族污染（图像载荷绝不含 thinkingConfig、语音载荷绝不含 tools）。
type ModelStrategy interface {
	// Family 返回家族。
	Family() transform.ModelFamily
	// Enhance 按家族注入 imageConfig / responseModalities / thinkingConfig 等。
	Enhance(req *transform.GeminiRequest, cfg config.ConfigProvider)
	// Validate 校验请求：禁止图载荷含不准许的 thinking、禁止语音载荷含 tools 等。
	Validate(req *transform.GeminiRequest) error
}

// FamilyFor 按实际模型名归一模型家族。
//
// 判定优先级：TTS（含 "tts"）→ 音频；含 "image" → 图像；其余文本。
func FamilyFor(model string) transform.ModelFamily {
	lower := strings.ToLower(strings.TrimSpace(model))
	if strings.Contains(lower, "tts") {
		return transform.FamilyAudio
	}
	if strings.Contains(lower, "image") {
		return transform.FamilyImage
	}
	return transform.FamilyText
}

// ModelFamilyRouter 是汇聚点的家族路由表：按实际模型名取出对应策略。
type ModelFamilyRouter struct{}

// NewModelFamilyRouter 装配路由表（策略按模型动态构造以携带 model 上下文）。
func NewModelFamilyRouter() *ModelFamilyRouter {
	return &ModelFamilyRouter{}
}

// Text 返回文本家族策略。
func (r *ModelFamilyRouter) Text(model string) ModelStrategy { return &TextStrategy{model: model} }

// Image 返回图像家族策略。
func (r *ModelFamilyRouter) Image(model string) ModelStrategy { return &ImageStrategy{model: model} }

// Audio 返回音频家族策略。
func (r *ModelFamilyRouter) Audio(model string) ModelStrategy { return &AudioStrategy{model: model} }

// For 按模型名取策略（未知模型按文本家族处理）。
func (r *ModelFamilyRouter) For(model string) ModelStrategy {
	switch FamilyFor(model) {
	case transform.FamilyImage:
		return &ImageStrategy{model: model}
	case transform.FamilyAudio:
		return &AudioStrategy{model: model}
	default:
		return &TextStrategy{model: model}
	}
}