package vertex

import (
	"context"
	"sort"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/engine/transform"
)

type candidateResult struct {
	proxyURI string
	resp     *transform.GeminiResponse
	err      error
}

func (c *VertexAIClient) CompleteChat(ctx context.Context, model string, req *transform.GeminiRequest, strategy transform.ModelStrategy) (*transform.GeminiResponse, error) {
	if strategy == nil {
		strategy = transform.SharedModelFamilyRouter().For(model)
	}
	run := func(ctx context.Context, proxyURI string) (*transform.GeminiResponse, error) {
		return c.runSingleCandidate(ctx, model, req, proxyURI, strategy)
	}
	// 非流式无部分交付（不标 Truncated），中途断流只是候选失败，由窗口引擎补位承接；
	// 补位、发射预算与 FailFast 门禁均由 RunRace 内部自治。
	resp, err := RunRace(ctx, c.nodePool(), c.cfg, run,
		WithWinningCheck(func(resp *transform.GeminiResponse) bool {
			return candidateFinishTyped(resp) == "STOP" && strategy.IsValidResponse(resp)
		}), WithCollectedFinalizer(func(results []raceResult[*transform.GeminiResponse]) (*transform.GeminiResponse, error) {
			cr := make([]candidateResult, len(results))
			for i, r := range results {
				cr[i] = candidateResult{proxyURI: r.uri, resp: r.val, err: r.err}
			}
			return pickBestResult(cr, strategy)
		}), WithLatencyLabel[*transform.GeminiResponse]("总耗时"))
	if err != nil {
		return nil, NormalizeError(err)
	}
	return resp, nil
}

// runSingleCandidate 执行单候选单次尝试（L1 透传层非流式版）：
// 并行建联 + 取 token（见 prepareCandidate）+ 单次 attempt，原样上报真实错误。
func (c *VertexAIClient) runSingleCandidate(ctx context.Context, model string, req *transform.GeminiRequest, proxyURI string, strategy transform.ModelStrategy) (*transform.GeminiResponse, error) {
	var chunks []*transform.GeminiChunk
	var validChunkCount int
	sess, tok, err := c.prepareCandidate(ctx, proxyURI)
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	attemptErr := withRTFirstTryCompensation(ctx, func() error {
		return c.executeStreamingAttempt(ctx, sess, model, req, tok, func(chunk *transform.GeminiChunk) bool {
			if chunk == nil {
				return true
			}
			chunks = append(chunks, chunk)
			if strategy.IsValidChunk(chunk) {
				validChunkCount++
			}
			return true
		}, strategy)
	})
	if attemptErr != nil {
		return nil, NormalizeError(attemptErr)
	}
	if validChunkCount == 0 {
		return nil, NewEmptyResponseError("Upstream returned no valid content", nil)
	}

	result := collectChunksToParseResultTyped(chunks)
	resp, buildErr := c.buildCompleteResponseTyped(result)
	if buildErr != nil {
		return nil, buildErr
	}

	// 发往上游的 payload 恒由三家族 BuildVariables 经 BuildSafetySettingsTyped 注入 4×OFF
	// （无视 req.SafetySettings），故原"req.SafetySettings==nil 且上游返回 SAFETY 时以 4×OFF 重试"
	// 分支即便命中，重试载荷也与首次逐字节一致、必得相同结果，属确定无效的重复请求；已删除。
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
	// 所有候选均无有效内容时：若任一候选被安全审查拦截（finishReason=SAFETY/RECITATION 等
	// 或 BlockReason 拦截），返回 safety 错误（携带真实拦截原因）而非退化为 502/500 内部错误，
	// 避免网关误判为服务故障。
	for _, r := range results {
		if isSafetyBlockedResponse(r.resp) {
			reason := "SAFETY"
			if r.resp.PromptFeedback != nil && transform.IsBlockReason(r.resp.PromptFeedback.BlockReason) {
				reason = strings.ToUpper(strings.TrimSpace(r.resp.PromptFeedback.BlockReason))
			} else if fr := strings.ToUpper(strings.TrimSpace(candidateFinishTyped(r.resp))); transform.IsSafetyFinishReason(fr) {
				reason = fr
			}
			return nil, NewSafetyError("Blocked by safety filter", reason, nil)
		}
	}
	return nil, NewEmptyResponseError("no viable candidate results", nil)
}

func isSafetyBlockedResponse(resp *transform.GeminiResponse) bool {
	return transform.IsSafetyResponse(resp)
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
