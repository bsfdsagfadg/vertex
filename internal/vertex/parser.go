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
	// Candidates 保存按 index 合并后的完整候选列表；顶层字段继续映射首候选以兼容旧调用方。
	Candidates []map[string]any
}

// ---- 小工具 ----

// isTruthyAny 委托 jsonx.Truthy（统一真值语义，见 jsonx.Truthy）。
func isTruthyAny(v any) bool { return jsonx.Truthy(v) }

func isEmptyContainer(v any) bool {
	switch x := v.(type) {
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

func toAnySlice(ms []map[string]any) []any {
	out := make([]any, len(ms))
	for i, m := range ms {
		out[i] = m
	}
	return out
}
