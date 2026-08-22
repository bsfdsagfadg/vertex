package api

import (
	"context"

	"github.com/bsfdsagfadg/vertex/internal/engine/transform"
	"github.com/bsfdsagfadg/vertex/internal/engine/vertex"
)

// ExecuteAudioSpeech 执行语音/TTS 模型 Speech 生成。
func (h *handler) ExecuteAudioSpeech(ctx context.Context, resolved *transform.ResolvedModel, req *transform.GeminiRequest) (*transform.GeminiResponse, *vertex.VertexError) {
	return h.executePipelineChat(ctx, resolved, req)
}
