package vertex

import (
	"context"

	"github.com/bsfdsagfadg/vertex/internal/transform"
)

// CompleteChatTyped 强类型非流式调用：输入 typed GeminiRequest，返回 typed GeminiResponse。
func (c *VertexAIClient) CompleteChatTyped(ctx context.Context, model string, req *transform.GeminiRequest, strategy transform.ModelStrategy) (*transform.GeminiResponse, error) {
	return c.CompleteChat(ctx, model, req, strategy)
}

// StreamChunkTyped 是真流式 typed 增量：要么是 GeminiChunk，要么是错误。
type StreamChunkTyped = StreamChunk

// StreamChatTyped 强类型真流式调用：yield 回调返回 false 表示上层要求停止。
func (c *VertexAIClient) StreamChatTyped(ctx context.Context, model string, req *transform.GeminiRequest, yield func(StreamChunkTyped) bool, strategy transform.ModelStrategy) {
	c.StreamChat(ctx, model, req, yield, strategy)
}
