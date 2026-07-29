package vertex

import (
	"github.com/bsfdsagfadg/vertex/internal/jsonx"
)

// ParseResult 是 batchGraphql 响应的解析结果（解析状态）。
type ParseResult struct { //nolint:govet
	Parts             []map[string]any
	FinishReason      string
	FinishMessage     any
	SafetyRatings     any
	CitationMetadata  any
	GroundingMetadata any
	TokenCount        any
	AvgLogprobs       any
	LogprobsResult    any
	CandidateIndex    int
	PromptFeedback    map[string]any
	UsageMetadata     map[string]any
	CreateTime        any
	ModelVersion      any
	ResponseID        any
	ModelStatus       any
	HasError          bool
	ErrorMessage      string
	ErrorObj          *VertexError

	// Candidates 是按 index 分组合并后的完整候选列表。
	// 每个元素是 Gemini candidate dict（含 index / content.parts / finishReason 等）。
	// 当上游返回多候选（candidateCount>1）时，所有候选均保留于此。
	// 顶层快捷字段（Parts、FinishReason 等）始终映射 Candidates[0]，
	// 确保对单候选调用方零侵入。
	Candidates []map[string]any
}

// ---- 小工具 ----

// isTruthyAny 委托 jsonx.Truthy（统一真值语义，见 jsonx.Truthy）。
func isTruthyAny(v any) bool { return jsonx.Truthy(v) }

func toAnySlice(ms []map[string]any) []any {
	out := make([]any, len(ms))
	for i, m := range ms {
		out[i] = m
	}
	return out
}
