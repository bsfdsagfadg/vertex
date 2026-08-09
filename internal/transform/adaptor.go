package transform

import "github.com/bsfdsagfadg/vertex/internal/config"

// ModelFamily 是模型家族枚举，用于汇聚点按家族分发载荷构建与响应解析。
type ModelFamily int

const (
	FamilyText ModelFamily = iota
	FamilyImage
	FamilyAudio
)

// String 返回家族名（供日志/观测用）。
func (f ModelFamily) String() string {
	switch f {
	case FamilyImage:
		return "image"
	case FamilyAudio:
		return "audio"
	default:
		return "text"
	}
}

// ProtocolAdaptor 按入口协议（OpenAI / Gemini 原生）与模型家族分发转换。
//
// 入站：ToGeminiRequest 把任意客户端协议请求归一成强类型 GeminiRequest
// （raw 可为 *ChatCompletionRequest 或 *GeminiRequest，由实现类型断言）。
// 出站：FromGeminiResponse / FromGeminiChunk 把强类型响应转回客户端协议
// （OpenAI 形态或 Gemini 原生形态，取决于适配器类型）。
type ProtocolAdaptor interface {
	// Family 返回该适配器负责的模型家族。
	Family() ModelFamily
	// ToGeminiRequest 归一化入站请求。返回 (标准请求, 实际模型名, 错误)。
	ToGeminiRequest(raw any, cfg config.ConfigProvider) (*GeminiRequest, string, error)
	// FromGeminiResponse 非流式响应 → 客户端协议响应对象。
	FromGeminiResponse(resp *GeminiResponse, model string) any
	// FromGeminiChunk 流式单帧 → 客户端协议 SSE 事件字符串列表。
	FromGeminiChunk(chunk *GeminiChunk, model, requestID string, isFirst bool, tracker *StreamToolCallTracker) []string
	// Aggregate 批处理多个非流式响应 → 客户端协议对象。
	AggregateN(resps []*GeminiResponse, model string) any
}