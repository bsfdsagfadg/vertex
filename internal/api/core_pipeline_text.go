package api

import (
	"context"

	"github.com/bsfdsagfadg/vertex/internal/cli"
	"github.com/bsfdsagfadg/vertex/internal/transform"
	"github.com/bsfdsagfadg/vertex/internal/vertex"
)

// ExecuteTextComplete 执行文本/语言模型非流式 Complete 生成。
func (h *handler) ExecuteTextComplete(ctx context.Context, resolved *transform.ResolvedModel, req *transform.GeminiRequest) (*transform.GeminiResponse, *vertex.VertexError) {
	strategy := resolved.Strategy
	strategy.Enhance(req, h.cfg)
	if err := strategy.Validate(req); err != nil {
		return nil, vertex.NewInvalidArgumentError(err.Error(), nil)
	}
	strategy.Prepare(req)

	cli.UpdateReqModel(vertex.RequestIDFromContext(ctx), resolved.ActualModel)
	resp, err := h.vc.CompleteChatTyped(ctx, resolved.ActualModel, req, strategy)
	if err != nil {
		return nil, toVertexError(err)
	}
	transform.CleanFinishReasonUnspecified(resp)
	return resp, nil
}

// ExecuteTextStream 执行文本/语言模型原生真流式 Stream 生成。
func (h *handler) ExecuteTextStream(ctx context.Context, resolved *transform.ResolvedModel, req *transform.GeminiRequest, onChunk func(chunk *transform.GeminiChunk, err *vertex.VertexError) bool) {
	strategy := resolved.Strategy
	strategy.Enhance(req, h.cfg)
	if err := strategy.Validate(req); err != nil {
		onChunk(nil, vertex.NewInvalidArgumentError(err.Error(), nil))
		return
	}
	strategy.Prepare(req)

	cli.UpdateReqModel(vertex.RequestIDFromContext(ctx), resolved.ActualModel)
	h.vc.StreamChatTyped(ctx, resolved.ActualModel, req, func(ch vertex.StreamChunkTyped) bool {
		if ch.Err != nil {
			return onChunk(nil, ch.Err)
		}
		transform.CleanFinishReasonUnspecified(ch.Data)
		return onChunk(ch.Data, nil)
	}, strategy)
}
