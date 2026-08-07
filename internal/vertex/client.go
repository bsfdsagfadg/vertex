package vertex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
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

type requestRouteKey struct{}

type requestTokenState struct {
	mu         sync.Mutex
	token      string
	proxyURI   string
	refreshing bool
	wait       chan struct{}
	lastErr    error
	refreshes  int
}

type requestRoute struct {
	entryURI string
	token    *requestTokenState
}

func routeFromContext(ctx context.Context) *requestRoute {
	route, _ := ctx.Value(requestRouteKey{}).(*requestRoute)
	return route
}

func (s *requestTokenState) get(ctx context.Context, pool *recaptcha.TokenPool) (string, error) {
	for {
		s.mu.Lock()
		if s.token != "" {
			token := s.token
			s.mu.Unlock()
			return token, nil
		}
		if s.refreshing {
			wait := s.wait
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-wait:
				s.mu.Lock()
				token, err := s.token, s.lastErr
				s.mu.Unlock()
				return token, err
			}
		}
		if s.lastErr != nil {
			err := s.lastErr
			s.mu.Unlock()
			return "", err
		}
		s.refreshing = true
		s.wait = make(chan struct{})
		wait := s.wait
		proxyURI := s.proxyURI
		s.mu.Unlock()

		token, err := pool.GetTokenWithProxyContext(ctx, proxyURI)
		if err == nil && token == "" {
			err = fmt.Errorf("empty recaptcha token")
		}
		s.mu.Lock()
		if err == nil {
			s.token = token
		}
		s.lastErr = err
		s.refreshing = false
		close(wait)
		s.mu.Unlock()
		return token, err
	}
}

func (s *requestTokenState) setProxyURI(proxyURI string) {
	s.mu.Lock()
	s.proxyURI = proxyURI
	s.token = ""
	s.lastErr = nil
	s.refreshes = 0
	s.mu.Unlock()
}

// invalidate clears the token only if the caller used the current generation.
// A request may synchronously refresh at most once after its initial acquisition.
func (s *requestTokenState) invalidate(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != token {
		return true
	}
	if s.refreshes >= 1 {
		return false
	}
	s.token = ""
	s.lastErr = nil
	s.refreshes++
	return true
}

func (c *VertexAIClient) prepareRequest(ctx context.Context) (context.Context, error) {
	entryURI := config.SelectEntryProxy(c.cfg)
	acquisitionURI := entryURI
	if acquisitionURI == "" {
		if candidates := nodes.SelectForParallel(1, c.cfg.ParallelNodeTopK(), c.cfg.DebugMode(), false); len(candidates) > 0 {
			acquisitionURI = candidates[0].RawURI
		}
	}
	state := &requestTokenState{proxyURI: acquisitionURI} //nolint:exhaustruct
	token, err := state.get(ctx, c.pool)
	entrySucceeded := entryURI != "" && err == nil
	if err != nil && entryURI != "" {
		_ = config.MarkEntryProxyFailure(entryURI, err.Error())
		if fallback := selectHealthyNode(c.cfg); fallback != "" && fallback != acquisitionURI {
			state.setProxyURI(fallback)
			token, err = state.get(ctx, c.pool)
		}
	}
	if err != nil || token == "" {
		if err == nil {
			err = fmt.Errorf("empty recaptcha token")
		}
		return ctx, err
	}
	if entrySucceeded {
		_ = config.MarkEntryProxySuccess(entryURI)
	}
	route := &requestRoute{entryURI: entryURI, token: state}
	return transport.WithEntryProxy(context.WithValue(ctx, requestRouteKey{}, route), entryURI), nil
}

func selectHealthyNode(cfg config.ConfigProvider) string {
	if cfg == nil {
		return ""
	}
	selected := nodes.SelectForParallel(1, cfg.ParallelNodeTopK(), cfg.DebugMode(), false)
	if len(selected) == 0 {
		return ""
	}
	return selected[0].RawURI
}

func NewVertexAIClient(cfg config.ConfigProvider) *VertexAIClient {
	net := transport.NewNetworkClient(cfg.DebugMode(), cfg.ProxyURL)
	return &VertexAIClient{
		net:  net,
		pool: recaptcha.NewTokenPool(net, cfg.ProxyURL, cfg.DebugMode()),
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
	routedCtx, err := c.prepareRequest(ctx)
	if err != nil {
		return nil, NewAuthenticationError("Could not fetch recaptcha token: "+err.Error(), err)
	}
	return c.completeChatNWithRoute(routedCtx, model, geminiPayload, n)
}

func (c *VertexAIClient) completeChatNWithRoute(ctx context.Context, model string, geminiPayload map[string]any, n int) ([]map[string]any, error) {
	if n > 1 {
		if b, err := json.Marshal(geminiPayload); err == nil && len(b) > largePayloadThreshold {
			log.Printf("[Vertex] [CompleteChatN] 大 payload (%d bytes) 降级为串行", len(b))
			return c.completeChatNSerialWithRoute(ctx, model, geminiPayload, n)
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
			r, err := c.completeChatWithRoute(ctx, model, geminiPayload)
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

func (c *VertexAIClient) completeChatNSerialWithRoute(ctx context.Context, model string, geminiPayload map[string]any, n int) ([]map[string]any, error) {
	var ok []map[string]any
	var firstErr error
	for i := 0; i < n; i++ {
		r, err := c.completeChatWithRoute(ctx, model, geminiPayload)
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
		return nil, NewInternalError("upstream parse error: " + r.ErrorMessage)
	}

	resp := map[string]any{}
	switch {
	case len(r.Candidates) > 0:
		resp["candidates"] = toAnySlice(r.Candidates)
	case len(r.Parts) > 0:
		resp["candidates"] = []any{buildCandidate(r.CandidateIndex, r.Parts, r)}
	case len(r.PromptFeedback) > 0:
		resp["candidates"] = []any{buildCandidate(r.CandidateIndex, []map[string]any{{"text": " "}}, r)}
	default:
		return nil, NewEmptyResponseError("Upstream returned empty response (no content)")
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

func buildCandidate(index int, parts []map[string]any, r *ParseResult) map[string]any {
	candidate := map[string]any{
		"index":   index,
		"content": map[string]any{"parts": toAnySlice(parts), "role": "model"},
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
	return candidate
}

// collectChunksToParseResult 按 candidate index 独立合并流式 parts，并保留所有候选。
func collectChunksToParseResult(chunks []map[string]any) *ParseResult {
	s := &ParseResult{
		PromptFeedback: map[string]any{},
		UsageMetadata:  map[string]any{},
	}
	candidatesMap := map[int]*candidateCollector{}

	for _, chunk := range chunks {
		if candidates, ok := chunk["candidates"].([]any); ok {
			for position, rawCandidate := range candidates {
				candidate, ok := rawCandidate.(map[string]any)
				if !ok {
					continue
				}
				index := position
				if candidate["index"] != nil {
					index = toInt(candidate["index"], position)
				}
				collector, exists := candidatesMap[index]
				if !exists {
					collector = &candidateCollector{index: index} //nolint:exhaustruct
					candidatesMap[index] = collector
				}

				if value := candidate["finishReason"]; isTruthyAny(value) {
					collector.finishReason = toStr(value)
				}
				if value, exists := candidate["finishMessage"]; exists {
					collector.finishMessage = value
				}
				if value := candidate["safetyRatings"]; isTruthyAny(value) {
					collector.safetyRatings = value
				}
				if value := candidate["citationMetadata"]; isTruthyAny(value) {
					collector.citationMetadata = value
				}
				if value := candidate["groundingMetadata"]; isTruthyAny(value) {
					collector.groundingMetadata = value
				}
				if value, exists := candidate["tokenCount"]; exists {
					collector.tokenCount = value
				}
				if value, exists := candidate["avgLogprobs"]; exists {
					collector.avgLogprobs = value
				}
				if value, exists := candidate["logprobsResult"]; exists {
					collector.logprobsResult = value
				}
				if content, ok := candidate["content"].(map[string]any); ok {
					if parts, ok := content["parts"].([]any); ok {
						for _, rawPart := range parts {
							if part, ok := rawPart.(map[string]any); ok {
								collector.parts = append(collector.parts, part)
							}
						}
					}
				}
			}
		}

		if feedback, ok := chunk["promptFeedback"].(map[string]any); ok && len(feedback) > 0 && len(s.PromptFeedback) == 0 {
			s.PromptFeedback = feedback
		}
		if usage, ok := chunk["usageMetadata"]; ok {
			if usageMap := toMap(usage); len(usageMap) > 0 {
				s.UsageMetadata = usageMap
			}
		}
		if value, ok := chunk["createTime"]; ok {
			s.CreateTime = value
		}
		if value, ok := chunk["modelVersion"]; ok {
			s.ModelVersion = value
		}
		if value, ok := chunk["responseId"]; ok {
			s.ResponseID = value
		}
	}

	indices := make([]int, 0, len(candidatesMap))
	for index := range candidatesMap {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	for _, index := range indices {
		collector := candidatesMap[index]
		parts := transform.MergeContentBlocks(collector.parts)
		candidate := map[string]any{
			"index":   index,
			"content": map[string]any{"parts": toAnySlice(parts), "role": "model"},
		}
		if collector.finishReason != "" {
			candidate["finishReason"] = strings.ToUpper(collector.finishReason)
		}
		setIfPresent(candidate, "finishMessage", collector.finishMessage)
		setIfPresent(candidate, "safetyRatings", collector.safetyRatings)
		setIfPresent(candidate, "citationMetadata", collector.citationMetadata)
		setIfPresent(candidate, "groundingMetadata", collector.groundingMetadata)
		setIfPresent(candidate, "tokenCount", collector.tokenCount)
		setIfPresent(candidate, "avgLogprobs", collector.avgLogprobs)
		setIfPresent(candidate, "logprobsResult", collector.logprobsResult)
		s.Candidates = append(s.Candidates, candidate)
	}

	if len(indices) > 0 {
		first := candidatesMap[indices[0]]
		s.Parts = transform.MergeContentBlocks(first.parts)
		s.FinishReason = first.finishReason
		s.FinishMessage = first.finishMessage
		s.SafetyRatings = first.safetyRatings
		s.CitationMetadata = first.citationMetadata
		s.GroundingMetadata = first.groundingMetadata
		s.TokenCount = first.tokenCount
		s.AvgLogprobs = first.avgLogprobs
		s.LogprobsResult = first.logprobsResult
		s.CandidateIndex = first.index
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

func deepCopyAny(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = deepCopyAny(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = deepCopyAny(item)
		}
		return out
	default:
		return v
	}
}

func asVertexError(err error) *VertexError {
	var ve *VertexError
	if errors.As(err, &ve) {
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
