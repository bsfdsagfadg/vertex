package vertex

import (
	"context"
	"errors"
	"log"
	"sort"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/nodes"
)

type candidateResult struct {
	proxyURI string
	resp     map[string]any
	err      error
}

func (c *VertexAIClient) CompleteChat(ctx context.Context, model string, geminiPayload map[string]any) (map[string]any, error) {
	cands := nodes.SelectForParallel(c.cfg.ParallelPoolSize(), c.cfg.ParallelNodeTopK(), c.cfg.DebugMode(), c.cfg.StickyNodePriority())

	if !c.cfg.ParallelPoolEnabled() || len(cands) == 0 {
		proxy := c.cfg.ActiveNodeURI()
		if proxy == "" {
			proxy = c.cfg.ProxyURL()
		}
		log.Printf("[Vertex] [CompleteChat] 降级为单节点运行: %s", nodes.GetNodeName(proxy))
		return c.runSingleCandidate(ctx, model, geminiPayload, proxy)
	}

	if c.cfg.DebugMode() {
		log.Printf("[Vertex] [CompleteChat] 启动所有候选, %d 个节点参与", len(cands))
		for _, cand := range cands {
			log.Printf("[Vertex] [CompleteChat] 参与节点: %s", cand.Name)
		}
	}

	ctxRace, cancel := context.WithCancel(ctx)
	defer cancel()

	resCh := make(chan candidateResult, len(cands))
	for _, cand := range cands {
		uri := cand.RawURI
		go func() {
			resp, err := c.runSingleCandidate(ctxRace, model, geminiPayload, uri)
			select {
			case resCh <- candidateResult{proxyURI: uri, resp: resp, err: err}:
			case <-ctxRace.Done():
			}
		}()
	}

	remaining := len(cands)
	var successes []candidateResult
	var lastError error

	maxWait := time.NewTimer(120 * time.Second)
	defer maxWait.Stop()

	for remaining > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-maxWait.C:
			cancel()
			if len(successes) > 0 {
				return pickBestResult(successes)
			}
			if lastError != nil {
				return nil, lastError
			}
			return nil, NewInternalError("parallel pool timed out waiting for candidates")
		case res := <-resCh:
			remaining--
			if res.err != nil {
				if res.err != context.Canceled && !errors.Is(res.err, context.Canceled) {
					lastError = res.err
					ve := asVertexError(res.err)
					if ve != nil && ve.Kind == "ratelimit" {
						if c.cfg.DebugMode() {
							log.Printf("[Vertex] [CompleteChat] 节点 %s 触发 429 API 限制，进入 30 秒短时歇息", nodes.GetNodeName(res.proxyURI))
						}
						nodes.RecordRateLimit(res.proxyURI, 30)
						nodes.GetStickyPool().Evict(res.proxyURI)
					} else {
						if c.cfg.DebugMode() {
							log.Printf("[Vertex] [CompleteChat] 节点 %s 失败: %s", nodes.GetNodeName(res.proxyURI), res.err.Error())
						}
						nodes.RecordTest(res.proxyURI, false, 0, res.err.Error())
						nodes.GetStickyPool().Evict(res.proxyURI)
					}
				}
				continue
			}
			nodes.RecordTest(res.proxyURI, true, 50, "")
			nodes.GetStickyPool().Add(res.proxyURI)
			if candidateFinish(res.resp) == "STOP" {
				cancel()
				return res.resp, nil
			}
			successes = append(successes, res)
		}
	}

	if len(successes) == 0 {
		if lastError != nil {
			return nil, lastError
		}
		return nil, NewEmptyResponseError("所有候选节点均失败")
	}

	return pickBestResult(successes)
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
		return nil, NewEmptyResponseError("Upstream returned no data")
	}

	result := collectChunksToParseResult(chunks)
	resp, err := c.buildCompleteResponse(result)
	if err != nil {
		return nil, err
	}

	if _, hasSafety := geminiPayload["safetySettings"]; candidateFinish(resp) == "SAFETY" && !hasSafety {
		retryPayload := shallowCopy(geminiPayload)
		retryPayload["safetySettings"] = defaultSafetySettings
		return c.runSingleCandidate(ctx, model, retryPayload, proxyURI)
	}

	return resp, nil
}

func pickBestResult(results []candidateResult) (map[string]any, error) {
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
	return results[0].resp, nil
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
