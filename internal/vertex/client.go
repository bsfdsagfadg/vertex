package vertex

import (
	"context"
	"errors"
	"math"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/recaptcha"
	"github.com/bsfdsagfadg/vertex/internal/transform"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

const (
	anonBaseURL      = "https://cloudconsole-pa.clients6.google.com"
	batchGraphqlPath = "/v3/entityServices/AiplatformEntityService/schemas/AIPLATFORM_GRAPHQL:batchGraphql"
	anonAPIKey       = "AIzaSyCI-zsRP85UVOi0DjtiCwWBwQ1djDy741g"
)

var batchGraphqlURL = anonBaseURL + batchGraphqlPath + "?key=" + anonAPIKey + "&prettyPrint=false" //nolint:gochecknoglobals

var defaultSafetySettings = []any{ //nolint:gochecknoglobals
	map[string]any{"category": "HARM_CATEGORY_HARASSMENT", "threshold": "BLOCK_NONE"},
	map[string]any{"category": "HARM_CATEGORY_HATE_SPEECH", "threshold": "BLOCK_NONE"},
	map[string]any{"category": "HARM_CATEGORY_SEXUALLY_EXPLICIT", "threshold": "BLOCK_NONE"},
	map[string]any{"category": "HARM_CATEGORY_DANGEROUS_CONTENT", "threshold": "BLOCK_NONE"},
	map[string]any{"category": "HARM_CATEGORY_CIVIC_INTEGRITY", "threshold": "BLOCK_NONE"},
}

// RequestIDKey 是 context 中存储 reqID 的键类型。
type RequestIDKey struct{}

// RequestIDFromContext 取请求上下文里的 request-id（无则空串）。
func RequestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(RequestIDKey{}).(string); ok {
		return v
	}
	return ""
}

type VertexAIClient struct {
	net  *transport.NetworkClient
	pool *recaptcha.TokenPool
	cfg  config.ConfigProvider
}

func NewVertexAIClient(cfg config.ConfigProvider, net *transport.NetworkClient) *VertexAIClient {
	return &VertexAIClient{
		net:  net,
		pool: recaptcha.NewTokenPool(net, cfg),
		cfg:  cfg,
	}
}

func (c *VertexAIClient) getBatchGraphqlURL() string {
	if !strings.HasPrefix(batchGraphqlURL, anonBaseURL) {
		return batchGraphqlURL
	}
	key := c.cfg.VertexAPIKey()
	if key == "" {
		key = anonAPIKey
	}
	return anonBaseURL + batchGraphqlPath + "?key=" + key + "&prettyPrint=false"
}

// candidateCollector 在 collectChunksToParseResult 内部使用，按 candidate index 分组收集流式 chunk 属性。
type candidateCollector struct {
	index             int
	parts             []map[string]any
	finishReason      string
	finishMessage     any
	safetyRatings     any
	citationMetadata  any
	groundingMetadata any
	tokenCount        any
	avgLogprobs       any
	logprobsResult    any
}

func (c *VertexAIClient) buildCompleteResponse(r *ParseResult) (map[string]any, error) {
	if r.HasError {
		return nil, NewInternalError("upstream parse error: "+r.ErrorMessage, nil)
	}
	resp := map[string]any{}

	if len(r.Candidates) > 0 {
		resp["candidates"] = toAnySlice(r.Candidates)
	} else if len(r.Parts) > 0 {
		candidate := map[string]any{
			"index":   r.CandidateIndex,
			"content": map[string]any{"parts": toAnySlice(r.Parts), "role": "model"},
		}
		if r.FinishReason != "" {
			candidate["finishReason"] = strings.ToUpper(r.FinishReason)
		}
		setIfPresent(candidate, "finishMessage", r.FinishMessage)
		setIfPresent(candidate, "safetyRatings", r.SafetyRatings)
		setIfPresent(candidate, "citationMetadata", r.CitationMetadata)
		setIfPresent(candidate, "groundingMetadata", r.GroundingMetadata)
		setIfPresent(candidate, "tokenCount", r.TokenCount)
		setIfPresent(candidate, "avgLogprobs", r.AvgLogprobs)
		setIfPresent(candidate, "logprobsResult", r.LogprobsResult)
		resp["candidates"] = []any{candidate}
	} else {
		if len(r.PromptFeedback) == 0 {
			return nil, NewEmptyResponseError("Upstream returned empty response (no content)", nil)
		}
		allParts := []map[string]any{{"text": " "}}
		candidate := map[string]any{
			"index":   r.CandidateIndex,
			"content": map[string]any{"parts": toAnySlice(allParts), "role": "model"},
		}
		resp["candidates"] = []any{candidate}
	}

	setIfPresent(resp, "createTime", r.CreateTime)
	setIfPresent(resp, "modelVersion", r.ModelVersion)
	if len(r.PromptFeedback) > 0 {
		resp["promptFeedback"] = r.PromptFeedback
	}
	setIfPresent(resp, "responseId", r.ResponseID)
	if len(r.UsageMetadata) > 0 {
		resp["usageMetadata"] = r.UsageMetadata
	}
	setIfPresent(resp, "modelStatus", r.ModelStatus)
	return resp, nil
}

// collectChunksToParseResult 把流式收集到的 chunk 列表合并为 ParseResult。
//
// chunks 是 extractChunk 的输出：每条含 candidates（全部候选，不再局限于 cands[0]）、
// finishReason、usageMetadata、promptFeedback 等元数据。
// 按 candidate index 分组聚合 parts，对各组独立执行 MergeContentBlocks，
// 最终按 index 升序写入 ParseResult.Candidates，同时将首候选属性映射到顶层快捷字段。
func collectChunksToParseResult(chunks []map[string]any) *ParseResult {
	s := &ParseResult{
		PromptFeedback: map[string]any{},
		UsageMetadata:  map[string]any{},
	}
	candidatesMap := map[int]*candidateCollector{}

	for _, chunk := range chunks {
		if cands, ok := chunk["candidates"].([]any); ok {
			for _, cRaw := range cands {
				c, ok := cRaw.(map[string]any)
				if !ok {
					continue
				}
				idx := 0
				if v := c["index"]; v != nil {
					idx = toInt(v, 0)
				}
				if _, exists := candidatesMap[idx]; !exists {
					candidatesMap[idx] = &candidateCollector{index: idx}
				}
				cc := candidatesMap[idx]

				if fr := c["finishReason"]; isTruthyAny(fr) {
					cc.finishReason = toStr(fr)
				}
				if fm, ok := c["finishMessage"]; ok {
					cc.finishMessage = fm
				}
				if v := c["safetyRatings"]; isTruthyAny(v) {
					cc.safetyRatings = v
				}
				if v := c["citationMetadata"]; isTruthyAny(v) {
					cc.citationMetadata = v
				}
				if v := c["groundingMetadata"]; isTruthyAny(v) {
					cc.groundingMetadata = v
				}
				if v, ok := c["tokenCount"]; ok {
					cc.tokenCount = v
				}
				if v, ok := c["avgLogprobs"]; ok {
					cc.avgLogprobs = v
				}
				if v, ok := c["logprobsResult"]; ok {
					cc.logprobsResult = v
				}

				if content, ok := c["content"].(map[string]any); ok {
					if parts, ok := content["parts"].([]any); ok {
						for _, pRaw := range parts {
							if p, ok := pRaw.(map[string]any); ok {
								cc.parts = append(cc.parts, p)
							}
						}
					}
				}
			}
		}

		if pf, ok := chunk["promptFeedback"].(map[string]any); ok && len(pf) > 0 && len(s.PromptFeedback) == 0 {
			s.PromptFeedback = pf
		}
		if um, ok := chunk["usageMetadata"]; ok {
			if m := toMap(um); len(m) > 0 {
				s.UsageMetadata = m
			}
		}
		if v, ok := chunk["createTime"]; ok {
			s.CreateTime = v
		}
		if v, ok := chunk["modelVersion"]; ok {
			s.ModelVersion = v
		}
		if v, ok := chunk["responseId"]; ok {
			s.ResponseID = v
		}
	}

	// 按 index 升序构建 candidates 切片
	indices := make([]int, 0, len(candidatesMap))
	for idx := range candidatesMap {
		indices = append(indices, idx)
	}
	sort.Ints(indices)

	candidates := make([]map[string]any, 0, len(indices))
	var firstMergedParts []map[string]any
	for _, idx := range indices {
		cc := candidatesMap[idx]
		mergedParts := transform.MergeContentBlocks(cc.parts)
		if firstMergedParts == nil {
			firstMergedParts = mergedParts
		}
		candidate := map[string]any{
			"index":   cc.index,
			"content": map[string]any{"parts": toAnySlice(mergedParts), "role": "model"},
		}
		if cc.finishReason != "" {
			candidate["finishReason"] = strings.ToUpper(cc.finishReason)
		}
		setIfPresent(candidate, "finishMessage", cc.finishMessage)
		setIfPresent(candidate, "safetyRatings", cc.safetyRatings)
		setIfPresent(candidate, "citationMetadata", cc.citationMetadata)
		setIfPresent(candidate, "groundingMetadata", cc.groundingMetadata)
		setIfPresent(candidate, "tokenCount", cc.tokenCount)
		setIfPresent(candidate, "avgLogprobs", cc.avgLogprobs)
		setIfPresent(candidate, "logprobsResult", cc.logprobsResult)
		candidates = append(candidates, candidate)
	}
	s.Candidates = candidates

	// 顶层快捷字段映射到首候选（index 最小者）
	if len(candidates) > 0 {
		firstCC := candidatesMap[indices[0]]
		s.Parts = firstMergedParts
		s.FinishReason = firstCC.finishReason
		s.FinishMessage = firstCC.finishMessage
		s.SafetyRatings = firstCC.safetyRatings
		s.CitationMetadata = firstCC.citationMetadata
		s.GroundingMetadata = firstCC.groundingMetadata
		s.TokenCount = firstCC.tokenCount
		s.AvgLogprobs = firstCC.avgLogprobs
		s.LogprobsResult = firstCC.logprobsResult
		s.CandidateIndex = firstCC.index
	}

	return s
}

func candidateFinish(result map[string]any) string {
	if cands, ok := result["candidates"].([]any); ok && len(cands) > 0 {
		if c, ok := cands[0].(map[string]any); ok {
			return toStr(c["finishReason"])
		}
	}
	return ""
}

func shallowCopy(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func asVertexError(err error) *VertexError {
	var ve *VertexError
	if errors.As(err, &ve) {
		return ve
	}
	return nil
}

// classifyNetworkError 将网络原生 error 统一包装为 *VertexError。
//
// 返回的 VertexError 保留原始 cause，供 errors.Is/As 穿透。
// 若 err 已是 *VertexError，直接返回避免双重包装。
func classifyNetworkError(err error) *VertexError {
	if err == nil {
		return nil
	}
	var ve *VertexError
	if errors.As(err, &ve) {
		return ve
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return NewContextError(err)
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return NewNetworkError(err)
	}

	return NewInternalError(err.Error(), err)
}

func setIfPresent(m map[string]any, key string, v any) {
	if v == nil {
		return
	}
	switch x := v.(type) {
	case string:
		if x == "" {
			return
		}
	case []any:
		if len(x) == 0 {
			return
		}
	case map[string]any:
		if len(x) == 0 {
			return
		}
	}
	m[key] = v
}

func backoff(attempt int) time.Duration {
	v := math.Pow(1.5, float64(attempt))
	if v > 15 {
		v = 15
	}
	return time.Duration(v * float64(time.Second))
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err() //nolint:wrapcheck
	case <-t.C:
		return nil
	}
}
