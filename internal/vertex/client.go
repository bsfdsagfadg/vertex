package vertex

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/jsonx"
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

var defaultSafetySettingsTyped = []transform.SafetySetting{ //nolint:gochecknoglobals
	{Category: "HARM_CATEGORY_HARASSMENT", Threshold: "BLOCK_NONE"},
	{Category: "HARM_CATEGORY_HATE_SPEECH", Threshold: "BLOCK_NONE"},
	{Category: "HARM_CATEGORY_SEXUALLY_EXPLICIT", Threshold: "BLOCK_NONE"},
	{Category: "HARM_CATEGORY_DANGEROUS_CONTENT", Threshold: "BLOCK_NONE"},
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

type ParseResultTyped struct {
	Candidates     []*transform.Candidate
	PromptFeedback *transform.PromptFeedback
	UsageMetadata  *transform.UsageMetadata
	ModelVersion   string
	HasError       bool
	ErrorMessage   string
}

func (c *VertexAIClient) buildCompleteResponseTyped(r *ParseResultTyped) (*transform.GeminiResponse, error) {
	if r.HasError {
		return nil, NewInternalError("upstream parse error: "+r.ErrorMessage, nil)
	}
	resp := &transform.GeminiResponse{
		Candidates:     r.Candidates,
		PromptFeedback: r.PromptFeedback,
		UsageMetadata:  r.UsageMetadata,
		ModelVersion:   r.ModelVersion,
	}
	if len(resp.Candidates) == 0 && resp.PromptFeedback == nil {
		return nil, NewEmptyResponseError("Upstream returned empty response (no content)", nil)
	}
	return resp, nil
}

func collectChunksToParseResultTyped(chunks []*transform.GeminiChunk) *ParseResultTyped {
	s := &ParseResultTyped{}
	candsMap := map[int]*transform.Candidate{}

	for _, chunk := range chunks {
		if chunk == nil {
			continue
		}
		for _, cand := range chunk.Candidates {
			if cand == nil {
				continue
			}
			idx := cand.Index
			existing, ok := candsMap[idx]
			if !ok {
				cCopy := *cand
				if cCopy.Content == nil {
					cCopy.Content = &transform.Content{Role: "model"}
				}
				candsMap[idx] = &cCopy
			} else {
				if cand.FinishReason != "" {
					existing.FinishReason = cand.FinishReason
				}
				if cand.Content != nil && len(cand.Content.Parts) > 0 {
					if existing.Content == nil {
						existing.Content = &transform.Content{Role: "model"}
					}
					existing.Content.Parts = append(existing.Content.Parts, cand.Content.Parts...)
				}
			}
		}
		if chunk.PromptFeedback != nil && s.PromptFeedback == nil {
			s.PromptFeedback = chunk.PromptFeedback
		}
		if chunk.UsageMetadata != nil {
			s.UsageMetadata = chunk.UsageMetadata
		}
		if chunk.ModelVersion != "" {
			s.ModelVersion = chunk.ModelVersion
		}
	}

	var idxs []int
	for idx := range candsMap {
		idxs = append(idxs, idx)
	}
	sort.Ints(idxs)

	for _, idx := range idxs {
		c := candsMap[idx]
		if c.Content != nil && len(c.Content.Parts) > 0 {
			c.Content.Parts = mergeStreamPartsTyped(c.Content.Parts)
		}
		s.Candidates = append(s.Candidates, c)
	}
	return s
}

func mergeStreamPartsTyped(parts []transform.Part) []transform.Part {
	if len(parts) == 0 {
		return parts
	}
	merged := make([]transform.Part, 0, len(parts))
	var current *transform.Part

	for _, p := range parts {
		if p.Text == "" {
			merged = append(merged, p)
			current = nil
			continue
		}
		if current != nil && current.Thought == p.Thought && current.Text != "" {
			current.Text += p.Text
			if p.ThoughtSignature != "" && current.ThoughtSignature == "" {
				current.ThoughtSignature = p.ThoughtSignature
			}
		} else {
			pCopy := p
			merged = append(merged, pCopy)
			current = &merged[len(merged)-1]
		}
	}
	return merged
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
		mergedParts := mergeStreamParts(cc.parts)
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

// mergeStreamParts 合并相邻同类型文本块（thought+thought、text+text），
// 语义对齐旧 transform.MergeContentBlocks（本地化实现，解除跨包依赖）。
func mergeStreamParts(parts []map[string]any) []map[string]any {
	cleaned := make([]map[string]any, 0, len(parts))
	for _, p := range parts {
		if c := cleanStreamSimple(p); c != nil {
			cleaned = append(cleaned, c)
		}
	}
	if len(cleaned) == 0 {
		return []map[string]any{}
	}

	merged := make([]map[string]any, 0, len(cleaned))
	var current map[string]any

	for _, part := range cleaned {
		isText := isNonEmptyString(part["text"])
		if !isText {
			merged = append(merged, part)
			current = nil
			continue
		}
		isThought := isTruthyAny(part["thought"])
		if current != nil && isTruthyAny(current["thought"]) == isThought {
			current["text"] = toStr(current["text"]) + toStr(part["text"])
			if sig, ok := part["thoughtSignature"]; ok {
				if _, exists := current["thoughtSignature"]; !exists {
					current["thoughtSignature"] = sig
				}
			}
		} else {
			np := map[string]any{"text": toStr(part["text"])}
			if isThought {
				np["thought"] = true
				if sig, ok := part["thoughtSignature"]; ok {
					np["thoughtSignature"] = sig
				}
			}
			merged = append(merged, np)
			current = np
		}
	}
	return merged
}

// cleanStreamSimple 是用于内容块合并的轻量清洗（本地版）。
func cleanStreamSimple(part map[string]any) map[string]any {
	cleaned := shallowCopy(part)
	if t, ok := cleaned["text"]; ok {
		if toStr(t) == "" {
			delete(cleaned, "text")
		}
	}
	if fcRaw, ok := cleaned["functionCall"]; ok {
		if fc, ok := fcRaw.(map[string]any); ok {
			if !isNonEmptyString(fc["name"]) {
				delete(cleaned, "functionCall")
			}
		}
	}
	if len(cleaned) == 0 {
		return nil
	}
	return cleaned
}

// isNonEmptyString 判断 any 是否为非空字符串。
func isNonEmptyString(v any) bool {
	s, ok := v.(string)
	return ok && strings.TrimSpace(s) != ""
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

// NormalizeError 将任意 error 统一归一化为 *VertexError。
//
// 1. 若 err 已是 *VertexError，直接返回避免双重包装；
// 2. 若包含 context.Canceled / context.DeadlineExceeded，包装为保留 cause 的 ContextError；
// 3. 若为 net.Error，包装为 502 NetworkError；
// 4. 其他未知错误包装为 500 InternalError。
func NormalizeError(err error) *VertexError {
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

// classifyNetworkError 将网络原生 error 统一包装为 *VertexError（内部复用 NormalizeError）。
func classifyNetworkError(err error) *VertexError {
	return NormalizeError(err)
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

func mapToGeminiChunk(m map[string]any) *transform.GeminiChunk {
	if m == nil {
		return &transform.GeminiChunk{}
	}
	b, err := jsonx.Marshal(m)
	if err != nil {
		return &transform.GeminiChunk{}
	}
	var chunk transform.GeminiChunk
	if err := json.Unmarshal(b, &chunk); err != nil {
		return &transform.GeminiChunk{}
	}
	return &chunk
}
