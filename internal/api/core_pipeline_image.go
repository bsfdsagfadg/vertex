package api

import (
	"context"

	"github.com/bsfdsagfadg/vertex/internal/engine/transform"
	"github.com/bsfdsagfadg/vertex/internal/engine/vertex"
)

// ExecuteImageGenerate 执行生图模型生成。
func (h *handler) ExecuteImageGenerate(ctx context.Context, resolved *transform.ResolvedModel, req *transform.GeminiRequest) (*transform.GeminiResponse, *vertex.VertexError) {
	return h.executePipelineChat(ctx, resolved, req)
}
