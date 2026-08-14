package vertex

import (
	"context"
	"sort"

	"github.com/bsfdsagfadg/vertex/internal/transform"
)

type candidateResult struct {
	proxyURI string
	resp     *transform.GeminiResponse
	err      error
}

func (c *VertexAIClient) CompleteChat(ctx context.Context, model string, req *transform.GeminiRequest) (*transform.GeminiResponse, error) {
	strategy := transform.NewModelFamilyRouter().For(model)
	run := func(ctx context.Context, proxyURI string) (*transform.GeminiResponse, error) {
		return c.runSingleCandidate(ctx, model, req, proxyURI)
	}
	return RunRace(ctx, c.cfg, run, WithWinningCheck(func(resp *transform.GeminiResponse) bool {
		return candidateFinishTyped(resp) == "STOP" && strategy.IsValidResponse(resp)
	}), WithCollectedFinalizer(func(results []raceResult[*transform.GeminiResponse]) (*transform.GeminiResponse, error) {
		cr := make([]candidateResult, len(results))
		for i, r := range results {
			cr[i] = candidateResult{proxyURI: r.uri, resp: r.val, err: r.err}
		}
		return pickBestResult(cr, strategy)
	}))
}

func (c *VertexAIClient) runSingleCandidate(ctx context.Context, model string, req *transform.GeminiRequest, proxyURI string) (*transform.GeminiResponse, error) {
	var chunks []*transform.GeminiChunk
	var firstErr *VertexError

	c.executeStreamingWithRetries(ctx, model, req, proxyURI, func(chunk StreamChunk) bool {
		if chunk.Err != nil {
			if firstErr == nil {
				firstErr = chunk.Err
			}
			return false
		}
		if chunk.Data != nil {
			chunks = append(chunks, chunk.Data)
		}
		return true
	})

	if firstErr != nil {
		return nil, firstErr
	}
	if len(chunks) == 0 {
		return nil, NewEmptyResponseError("Upstream returned no data", nil)
	}

	result := collectChunksToParseResultTyped(chunks)
	resp, err := c.buildCompleteResponseTyped(result)
	if err != nil {
		return nil, err
	}

	if req.SafetySettings == nil && candidateFinishTyped(resp) == "SAFETY" {
		if ctx.Err() != nil {
			return nil, context.Canceled
		}
		retryReq := *req
		retryReq.SafetySettings = defaultSafetySettingsTyped
		return c.runSingleCandidate(ctx, model, &retryReq, proxyURI)
	}

	return resp, nil
}

func pickBestResult(results []candidateResult, strategy transform.ModelStrategy) (*transform.GeminiResponse, error) {
	if len(results) == 0 {
		return nil, NewEmptyResponseError("no viable candidate results", nil)
	}
	sort.Slice(results, func(i, j int) bool {
		fi := candidateFinishTyped(results[i].resp)
		fj := candidateFinishTyped(results[j].resp)
		if fi == "MAX_TOKENS" && fj != "MAX_TOKENS" {
			return true
		}
		if fj == "MAX_TOKENS" && fi != "MAX_TOKENS" {
			return false
		}
		return responseContentLengthTyped(results[i].resp) > responseContentLengthTyped(results[j].resp)
	})
	for _, r := range results {
		if isSafetyBlockedResponse(r.resp) {
			continue
		}
		if strategy != nil {
			if strategy.IsValidResponse(r.resp) {
				return r.resp, nil
			}
		} else if hasViableResponseTyped(r.resp) {
			return r.resp, nil
		}
	}
	// 所有候选均无有效内容时：若任一候选被安全审查拦截（finishReason=SAFETY 或 BlockReason），
	// 返回 safety 错误而非退化为 502/500 内部错误，避免网关误判为服务故障。
	for _, r := range results {
		if isSafetyBlockedResponse(r.resp) {
			return nil, NewSafetyError("Blocked by safety filter", "SAFETY", nil)
		}
	}
	return nil, NewEmptyResponseError("no viable candidate results", nil)
}

func isSafetyBlockedResponse(resp *transform.GeminiResponse) bool {
	if resp == nil {
		return false
	}
	if resp.PromptFeedback != nil && resp.PromptFeedback.BlockReason != "" && resp.PromptFeedback.BlockReason != "BLOCKED_REASON_UNSPECIFIED" {
		return true
	}
	for _, cand := range resp.Candidates {
		if cand != nil && cand.FinishReason == "SAFETY" {
			return true
		}
	}
	return false
}

func candidateFinishTyped(resp *transform.GeminiResponse) string {
	if resp == nil || len(resp.Candidates) == 0 {
		return ""
	}
	return resp.Candidates[0].FinishReason
}

func hasViableResponseTyped(resp *transform.GeminiResponse) bool {
	if resp == nil || len(resp.Candidates) == 0 {
		return false
	}
	c := resp.Candidates[0]
	if c.Content == nil {
		return false
	}
	return len(c.Content.Parts) > 0
}

func responseContentLengthTyped(resp *transform.GeminiResponse) int {
	if resp == nil || len(resp.Candidates) == 0 {
		return 0
	}
	c := resp.Candidates[0]
	if c.Content == nil {
		return 0
	}
	total := 0
	for _, p := range c.Content.Parts {
		total += len(p.Text)
	}
	return total
}
