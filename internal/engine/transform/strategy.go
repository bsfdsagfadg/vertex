package transform

import (
	"strings"
	"sync"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/infra/config"
)

// ModelStrategy 是单个模型家族的载荷增强与响应断言策略：
// 每个家族独占实施自己上行与下行两端的家族特化逻辑，结构上杜绝跨家族污染。
type ModelStrategy interface {
	// ── 上行请求侧 ──
	// Family 返回家族枚举。
	Family() ModelFamily
	// Enhance 按家族注入 imageConfig / responseModalities / thinkingConfig 等。
	Enhance(req *GeminiRequest, cfg config.ConfigProvider)
	// Validate 校验请求：禁止图载荷含不准许的 thinking、禁止语音载荷含 tools 等。
	Validate(req *GeminiRequest) error
	// Prepare 在通过校验后做出口前的家族特化数据清洗与整理。
	Prepare(req *GeminiRequest)

	// ── 传输模式管控与上行变量构建 ──
	// FamilyStreamMode 返回家族支持的流式传输模式。
	FamilyStreamMode() StreamMode
	// BuildVariables 独立构建家族特化的上行 GeminiVariables 载荷。
	BuildVariables(model string, req *GeminiRequest, cfg config.ConfigProvider) *GeminiVariables

	// ── 下行响应侧 ──
	// CalculateIdleTimeouts 根据基础配置秒数，计算该家族特化的首包超时 (preTimeout) 与包间超时 (postTimeout)。
	CalculateIdleTimeouts(baseSeconds int) (preTimeout, postTimeout time.Duration)

	// IsValidChunk 判定单个 SSE Chunk 是否包含该家族的实质有效增量（用于竞速胜出与流式解锁）。
	IsValidChunk(chunk *GeminiChunk) bool

	// IsValidResponse 判定非流式完整响应是否包含该家族的实质有效交付内容（用于非流式对冲重试胜出）。
	IsValidResponse(resp *GeminiResponse) bool
}

// FamilyFor 按实际模型名归一模型家族。
//
// 判定优先级：TTS（含 "tts"）→ 音频；生图（含 "image" 或 "imagen"）→ 图像；其余文本。
func FamilyFor(model string) ModelFamily {
	lower := strings.ToLower(strings.TrimSpace(model))
	if strings.Contains(lower, "tts") {
		return FamilyAudio
	}
	if strings.Contains(lower, "image") || strings.Contains(lower, "imagen") {
		return FamilyImage
	}
	return FamilyText
}

// ModelFamilyRouter 是汇聚点的家族路由表：按实际模型名取出对应策略。
type ModelFamilyRouter struct{}

// NewModelFamilyRouter 装配路由表（策略按模型动态构造以携带 model 上下文）。
func NewModelFamilyRouter() *ModelFamilyRouter {
	return &ModelFamilyRouter{}
}

// sharedRouter 是进程级路由表单例的惰性初始化钩子（路由表无状态，可安全共享）。
var sharedRouter = sync.OnceValue(NewModelFamilyRouter) //nolint:gochecknoglobals

// SharedModelFamilyRouter 返回进程级共享路由表单例（零分配、消除逐请求构造）。
func SharedModelFamilyRouter() *ModelFamilyRouter {
	return sharedRouter()
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
	case FamilyImage:
		return &ImageStrategy{model: model}
	case FamilyAudio:
		return &AudioStrategy{model: model}
	default:
		return &TextStrategy{model: model}
	}
}
