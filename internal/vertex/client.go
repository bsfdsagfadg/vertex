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
