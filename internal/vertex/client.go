package vertex

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
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
		pool: recaptcha.NewTokenPool(net, cfg.ProxyURL(), cfg.DebugMode()),
		cfg:  cfg,
	}
}

func (c *VertexAIClient) StartTokenPool()                  { c.pool.Start() }
func (c *VertexAIClient) StopTokenPool()                   { c.pool.Stop() }
func (c *VertexAIClient) TokenPoolStats() (size, fill int) { return c.pool.Stats() }

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

const largePayloadThreshold = 1 << 20 // 1MB

func (c *VertexAIClient) CompleteChatN(ctx context.Context, model string, geminiPayload map[string]any, n int) ([]map[string]any, error) {
	if n > 1 {
		if b, err := json.Marshal(geminiPayload); err == nil && len(b) > largePayloadThreshold {
			log.Printf("[Vertex] [CompleteChatN] 大 payload (%d bytes) 降级为串行", len(b))
			return c.completeChatNSerial(ctx, model, geminiPayload, n)
		}
	}

	type res struct {
		resp map[string]any
		err  error
	}
	results := make([]res, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			defer func() {
				if rec := recover(); rec != nil {
					results[idx] = res{err: NewInternalError(fmt.Sprintf("candidate panic: %v", rec))} //nolint:exhaustruct
				}
			}()
			r, err := c.CompleteChat(ctx, model, geminiPayload)
			results[idx] = res{resp: r, err: err}
		}(i)
	}
	wg.Wait()

	var ok []map[string]any
	var firstErr error
	for _, r := range results {
		if r.err != nil {
			if firstErr == nil {
				firstErr = r.err
			}
			continue
		}
		ok = append(ok, r.resp)
	}
	if len(ok) == 0 {
		if firstErr == nil {
			firstErr = NewInternalError("All candidates failed")
		}
		return nil, firstErr
	}
	return ok, nil
}

func (c *VertexAIClient) completeChatNSerial(ctx context.Context, model string, geminiPayload map[string]any, n int) ([]map[string]any, error) {
	var ok []map[string]any
	var firstErr error
	for i := 0; i < n; i++ {
		r, err := c.CompleteChat(ctx, model, geminiPayload)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		ok = append(ok, r)
	}
	if len(ok) == 0 {
		if firstErr == nil {
			firstErr = NewInternalError("All candidates failed")
		}
		return nil, firstErr
	}
	return ok, nil
}

func (c *VertexAIClient) buildCompleteResponse(r *ParseResult) (map[string]any, error) {
	if len(r.Parts) == 0 {
		if r.HasError {
			return nil, NewInternalError("upstream parse error: " + r.ErrorMessage)
		}
		if len(r.PromptFeedback) == 0 {
			return nil, NewEmptyResponseError("Upstream returned empty response (no content)")
		}
	}

	allParts := r.Parts
	if len(allParts) == 0 {
		allParts = []map[string]any{{"text": " "}}
	}
	candidate := map[string]any{
		"index":   r.CandidateIndex,
		"content": map[string]any{"parts": toAnySlice(allParts), "role": "model"},
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

	resp := map[string]any{"candidates": []any{candidate}}
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
// chunks 是 extractChunk 的输出：每条含 candidates[0].content.parts（已清洗）、
// finishReason、usageMetadata、promptFeedback 等元数据。
// parts 经 MergeContentBlocks 合并相邻 text 后写入 result。
func collectChunksToParseResult(chunks []map[string]any) *ParseResult {
	s := &ParseResult{
		PromptFeedback: map[string]any{},
		UsageMetadata:  map[string]any{},
	}
	var allParts []map[string]any

	for _, chunk := range chunks {
		if cands, ok := chunk["candidates"].([]any); ok && len(cands) > 0 {
			if c, ok := cands[0].(map[string]any); ok {
				if fr := c["finishReason"]; isTruthyAny(fr) {
					s.FinishReason = toStr(fr)
				}
				if fm, ok := c["finishMessage"]; ok {
					s.FinishMessage = fm
				}
				if v := c["safetyRatings"]; isTruthyAny(v) {
					s.SafetyRatings = v
				}
				if v := c["citationMetadata"]; isTruthyAny(v) {
					s.CitationMetadata = v
				}
				if v := c["groundingMetadata"]; isTruthyAny(v) {
					s.GroundingMetadata = v
				}
				if v, ok := c["tokenCount"]; ok {
					s.TokenCount = v
				}
				if v, ok := c["avgLogprobs"]; ok {
					s.AvgLogprobs = v
				}
				if v, ok := c["logprobsResult"]; ok {
					s.LogprobsResult = v
				}
				if v := c["index"]; v != nil {
					s.CandidateIndex = toInt(v, 0)
				}

				if content, ok := c["content"].(map[string]any); ok {
					if parts, ok := content["parts"].([]any); ok {
						for _, pRaw := range parts {
							if p, ok := pRaw.(map[string]any); ok {
								allParts = append(allParts, p)
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

	s.Parts = transform.MergeContentBlocks(allParts)
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
	if ve, ok := err.(*VertexError); ok {
		return ve
	}
	return nil
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
