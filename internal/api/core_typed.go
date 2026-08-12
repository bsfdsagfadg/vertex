package api

import (
	"context"

	"github.com/bsfdsagfadg/vertex/internal/cli"
	"github.com/bsfdsagfadg/vertex/internal/transform"
	"github.com/bsfdsagfadg/vertex/internal/vertex"
)

// 本文件实现汇聚点的强类型核心链路：家族路由 + 策略增强/校验 +
// typed 提交通道 + typed 响应返回。
// 语义与旧 map 版 coreGenerate/coreStreamGenerate 严格一致，仅边界类型化。

// coreGenerateTyped 统一非流式 typed 调用：剥离 fake 前缀、家族路由、
// 策略增强、更新模型追踪、调用 CompleteChatTyped、错误转换、清洗 finishReason。
func (h *handler) coreGenerateTyped(ctx context.Context, rawModel string, req *transform.GeminiRequest) (*transform.GeminiResponse, *vertex.VertexError) {
	actualModel, _ := stripFakePrefix(rawModel, h.cfg.FakePrefixes())
	strategy := transform.NewModelFamilyRouter().For(actualModel)
	strategy.Enhance(req, h.cfg)
	if err := strategy.Validate(req); err != nil {
		return nil, vertex.NewInvalidArgumentError(err.Error(), nil)
	}
	strategy.Prepare(req)
	cli.UpdateReqModel(vertex.RequestIDFromContext(ctx), actualModel)
	resp, err := h.vc.CompleteChatTyped(ctx, actualModel, req)
	if err != nil {
		return nil, toVertexError(err)
	}
	cleanGeminiFinishReasonTyped(resp)
	return resp, nil
}

// coreStreamGenerateTyped 统一真流式 typed 调用：回调中自动清洗 finishReason。
func (h *handler) coreStreamGenerateTyped(ctx context.Context, rawModel string, req *transform.GeminiRequest, onChunk func(chunk *transform.GeminiChunk, err *vertex.VertexError) bool) {
	actualModel, _ := stripFakePrefix(rawModel, h.cfg.FakePrefixes())
	strategy := transform.NewModelFamilyRouter().For(actualModel)
	strategy.Enhance(req, h.cfg)
	if err := strategy.Validate(req); err != nil {
		onChunk(nil, vertex.NewInvalidArgumentError(err.Error(), nil))
		return
	}
	strategy.Prepare(req)
	cli.UpdateReqModel(vertex.RequestIDFromContext(ctx), actualModel)
	h.vc.StreamChatTyped(ctx, actualModel, req, func(ch vertex.StreamChunkTyped) bool {
		if ch.Err != nil {
			return onChunk(nil, ch.Err)
		}
		cleanGeminiFinishReasonTyped(ch.Data)
		return onChunk(ch.Data, nil)
	})
}

// cleanGeminiFinishReasonTyped 清洗 typed 候选的 FINISH_REASON_UNSPECIFIED，
// 返回首个真实 finishReason（语义对齐旧 map 版 cleanGeminiFinishReason）。
func cleanGeminiFinishReasonTyped(resp *transform.GeminiResponse) string {
	if resp == nil {
		return ""
	}
	var realFR string
	for _, cand := range resp.Candidates {
		if cand == nil {
			continue
		}
		if cand.FinishReason == "FINISH_REASON_UNSPECIFIED" {
			cand.FinishReason = ""
		} else if cand.FinishReason != "" && realFR == "" {
			realFR = cand.FinishReason
		}
	}
	return realFR
}