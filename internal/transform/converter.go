package transform

import (
	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/domain"
)

// RequestConverter 将客户端请求转为 Vertex AI 请求。
type RequestConverter interface {
	Convert(body map[string]any, cfg config.ConfigProvider) (model string, geminiPayload map[string]any, err error)
}

// ResponseConverter 将 Vertex AI 响应转为 OpenAI 格式。
type ResponseConverter interface {
	ToOAI(geminiResp map[string]any, model string) map[string]any
	StreamToSSE(chunk map[string]any, model, requestID string, isFirst bool, tracker *StreamToolCallTracker) []string
	AggregateN(responses []map[string]any, model string) map[string]any
}

type defaultRequestConverter struct{}

func (defaultRequestConverter) Convert(body map[string]any, cfg config.ConfigProvider) (string, map[string]any, error) {
	return ConvertChatRequestMap(body, cfg)
}

// ConvertTyped 直接转换强类型结构。
func (defaultRequestConverter) ConvertTyped(req *domain.ChatCompletionRequest, cfg config.ConfigProvider) (string, *domain.GenerateContentRequest, error) {
	return ConvertChatRequest(req, cfg)
}

// DefaultRequestConverter 返回默认请求转换器。
func DefaultRequestConverter() RequestConverter { return defaultRequestConverter{} }

type defaultResponseConverter struct{}

func (defaultResponseConverter) ToOAI(geminiResp map[string]any, model string) map[string]any {
	return GeminiJSONToOAIJSON(geminiResp, model)
}

func (defaultResponseConverter) StreamToSSE(chunk map[string]any, model, requestID string, isFirst bool, tracker *StreamToolCallTracker) []string {
	return ConvertRealtimeChunk(chunk, model, requestID, isFirst, tracker)
}

func (defaultResponseConverter) AggregateN(responses []map[string]any, model string) map[string]any {
	return GeminiResponsesToOAIJSON(responses, model)
}

// DefaultResponseConverter 返回默认响应转换器。
func DefaultResponseConverter() ResponseConverter { return defaultResponseConverter{} }
