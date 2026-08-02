package transform

import "github.com/bsfdsagfadg/vertex/internal/config"

type RequestConverter interface {
	Convert(body map[string]any, cfg config.ConfigProvider) (model string, geminiPayload map[string]any, err error)
}

type ResponseConverter interface {
	ToOAI(geminiResp map[string]any, model string) map[string]any
	StreamToSSE(chunk map[string]any, model, requestID string, isFirst bool, tracker *StreamToolCallTracker) []string
	AggregateN(responses []map[string]any, model string) map[string]any
}

type defaultRequestConverter struct{}

func (defaultRequestConverter) Convert(body map[string]any, cfg config.ConfigProvider) (string, map[string]any, error) {
	return ConvertChatRequest(body, cfg)
}

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

func DefaultResponseConverter() ResponseConverter { return defaultResponseConverter{} }
