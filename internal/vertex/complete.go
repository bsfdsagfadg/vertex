package vertex

import (
	"context"
	"sort"

	"github.com/bsfdsagfadg/vertex/internal/transform"
)

type candidateResult struct {
	proxyURI string
	resp     map[string]any
	err      error
}

func (c *VertexAIClient) CompleteChat(ctx context.Context, model string, geminiPayload map[string]any) (map[string]any, error) {
	run := func(ctx context.Context, proxyURI string) (map[string]any, error) {
		payloadCopy := transform.DeepCopyAny(geminiPayload).(map[string]any)
		return c.runSingleCandidate(ctx, model, payloadCopy, proxyURI)
	}
	return RunRace(ctx, c.cfg, run, WithWinningCheck(func(resp map[string]any) bool {
		return candidateFinish(resp) == "STOP"
	}), WithCollectedFinalizer(func(results []raceResult[map[string]any]) (map[string]any, error) {
		cr := make([]candidateResult, len(results))
		for i, r := range results {
			cr[i] = candidateResult{proxyURI: r.uri, resp: r.val, err: r.err}
		}
		return pickBestResult(cr)
	}))
}

func (c *VertexAIClient) runSingleCandidate(ctx context.Context, model string, geminiPayload map[string]any, proxyURI string) (map[string]any, error) {
	var chunks []map[string]any
	var firstErr *VertexError

	c.executeStreamingWithRetries(ctx, model, geminiPayload, proxyURI, func(chunk StreamChunk) bool {
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

	result := collectChunksToParseResult(chunks)
	resp, err := c.buildCompleteResponse(result)
	if err != nil {
		return nil, err
	}

	if _, hasSafety := geminiPayload["safetySettings"]; candidateFinish(resp) == "SAFETY" && !hasSafety {
		if ctx.Err() != nil {
			return nil, context.Canceled
		}
		retryPayload := shallowCopy(geminiPayload)
		retryPayload["safetySettings"] = defaultSafetySettings
		return c.runSingleCandidate(ctx, model, retryPayload, proxyURI)
	}

	return resp, nil
}

func pickBestResult(results []candidateResult) (map[string]any, error) {
	if len(results) == 0 {
		return nil, NewInternalError("no viable candidate results", nil)
	}
	sort.Slice(results, func(i, j int) bool {
		fi := candidateFinish(results[i].resp)
		fj := candidateFinish(results[j].resp)
		if fi == "MAX_TOKENS" && fj != "MAX_TOKENS" {
			return true
		}
		if fj == "MAX_TOKENS" && fi != "MAX_TOKENS" {
			return false
		}
		return responseContentLength(results[i].resp) > responseContentLength(results[j].resp)
	})
	for _, r := range results {
		if hasViableResponse(r.resp) {
			return r.resp, nil
		}
	}
	return nil, NewInternalError("no viable candidate results", nil)
}

func hasViableResponse(resp map[string]any) bool {
	cands, ok := resp["candidates"].([]any)
	if !ok || len(cands) == 0 {
		return false
	}
	c, ok := cands[0].(map[string]any)
	if !ok {
		return false
	}
	content, ok := c["content"].(map[string]any)
	if !ok {
		return false
	}
	parts, ok := content["parts"].([]any)
	return ok && len(parts) > 0
}

func responseContentLength(resp map[string]any) int {
	cands, ok := resp["candidates"].([]any)
	if !ok || len(cands) == 0 {
		return 0
	}
	c, ok := cands[0].(map[string]any)
	if !ok {
		return 0
	}
	content, ok := c["content"].(map[string]any)
	if !ok {
		return 0
	}
	parts, ok := content["parts"].([]any)
	if !ok {
		return 0
	}
	total := 0
	for _, pRaw := range parts {
		p, ok := pRaw.(map[string]any)
		if !ok {
			continue
		}
		total += len(toStr(p["text"]))
	}
	return total
}
