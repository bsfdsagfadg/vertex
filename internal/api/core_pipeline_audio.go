package api

import (
	"context"

	"github.com/bsfdsagfadg/vertex/internal/engine/transform"
	"github.com/bsfdsagfadg/vertex/internal/engine/vertex"
	"github.com/bsfdsagfadg/vertex/internal/infra/cli"
)

// ExecuteAudioSpeech 执行语音/TTS 模型 Speech 生成。
func (h *handler) ExecuteAudioSpeech(ctx context.Context, resolved *transform.ResolvedModel, req *transform.GeminiRequest) (*transform.GeminiResponse, *vertex.VertexError) {
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
