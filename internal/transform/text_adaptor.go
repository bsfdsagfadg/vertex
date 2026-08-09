package transform

import "github.com/bsfdsagfadg/vertex/internal/config"

// 本文件实现文本模型家族的 ProtocolAdaptor。
// 入站支持 OpenAI ChatCompletion 与 Gemini 原生请求；出站把 Gemini 强类型
// 响应转回 OpenAI SSE / 非流式 JSON。

// TextAdaptor 是文本家族适配器。
type TextAdaptor struct{}

// NewTextAdaptor 构造文本适配器。
func NewTextAdaptor() *TextAdaptor { return &TextAdaptor{} }

// Family 返回文本家族。
func (t *TextAdaptor) Family() ModelFamily { return FamilyText }

// ToGeminiRequest 归一化入站文本请求。
func (t *TextAdaptor) ToGeminiRequest(raw any, cfg config.ConfigProvider) (*GeminiRequest, string, error) {
	switch v := raw.(type) {
	case *ChatCompletionRequest:
		return ConvertChatRequestToGemini(v, cfg)
	case *GeminiRequest:
		return v, "", nil
	case map[string]any:
		return normalizeGeminiRequestMap(v)
	default:
		return nil, "", errInvalidProtocol("无法识别的文本请求类型")
	}
}

// FromGeminiResponse 文本非流式响应 → *ChatCompletionResponse。
func (t *TextAdaptor) FromGeminiResponse(resp *GeminiResponse, model string) any {
	return BuildChatCompletion(resp, model)
}

// FromGeminiChunk 文本流式帧 → OAI SSE 事件行。
func (t *TextAdaptor) FromGeminiChunk(chunk *GeminiChunk, model, requestID string, isFirst bool, tracker *StreamToolCallTracker) []string {
	return ConvertRealtimeChunkTyped(chunk, model, requestID, isFirst, tracker)
}

// AggregateN 聚合 n 个非流式响应为多 choice。
func (t *TextAdaptor) AggregateN(resps []*GeminiResponse, model string) any {
	return AggregateChatCompletions(resps, model)
}

// normalizeGeminiRequestMap 由 raw map 直接 freeze 成 *GeminiRequest。
func normalizeGeminiRequestMap(raw map[string]any) (*GeminiRequest, string, error) {
	b, err := jsonxMarshal(raw)
	if err != nil {
		return nil, "", err
	}
	var gm GeminiRequest
	if err := jsonxUnmarshal(b, &gm); err != nil {
		return nil, "", err
	}
	return &gm, "", nil
}

// NormalizeGeminiRequestMap 导出版：由 raw map freeze 成 *GeminiRequest（供 api 层调用）。
func NormalizeGeminiRequestMap(raw map[string]any) (*GeminiRequest, error) {
	gm, _, err := normalizeGeminiRequestMap(raw)
	return gm, err
}