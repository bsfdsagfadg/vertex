package api

import (
	"context"

	"github.com/bsfdsagfadg/vertex/internal/cli"
	"github.com/bsfdsagfadg/vertex/internal/transform"
	"github.com/bsfdsagfadg/vertex/internal/vertex"
)

// ExecuteImageGenerate 执行生图模型生成。
func (h *handler) ExecuteImageGenerate(ctx context.Context, resolved *transform.ResolvedModel, req *transform.GeminiRequest) (*transform.GeminiResponse, *vertex.VertexError) {
	strategy := resolved.Strategy
	strategy.Enhance(req, h.cfg)
	if err := strategy.Validate(req); err != nil {
		return nil, vertex.NewInvalidArgumentError(err.Error(), nil)
	}
	strategy.Prepare(req)

	cli.UpdateReqModel(vertex.RequestIDFromContext(ctx), resolved.ActualModel)
	resp, err := h.vc.CompleteChatTyped(ctx, resolved.ActualModel, req)
	if err != nil {
		return nil, toVertexError(err)
	}
	cleanGeminiFinishReasonTyped(resp)
	if !strategy.IsValidResponse(resp) {
		return nil, vertex.NewEmptyResponseError("Upstream returned no image payload", nil)
	}
	return resp, nil
}

// ExecuteImageStream 执行生图模型流式请求的安全降级守护：先完整非流式生成，再通过 onChunk 以单包 SSE 输出。
func (h *handler) ExecuteImageStream(ctx context.Context, resolved *transform.ResolvedModel, req *transform.GeminiRequest, onChunk func(chunk *transform.GeminiChunk, err *vertex.VertexError) bool) {
	resp, ve := h.ExecuteImageGenerate(ctx, resolved, req)
	if ve != nil {
		onChunk(nil, ve)
		return
	}
	chunk := &transform.GeminiChunk{
		Candidates:     resp.Candidates,
		PromptFeedback: resp.PromptFeedback,
		UsageMetadata:  resp.UsageMetadata,
		ModelVersion:   resp.ModelVersion,
	}
	onChunk(chunk, nil)
}
