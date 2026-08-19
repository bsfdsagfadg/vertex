package vertex

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/transform"
)

type candidateResult struct {
	proxyURI string
	resp     *transform.GeminiResponse
	err      error
}

func (c *VertexAIClient) CompleteChat(ctx context.Context, model string, req *transform.GeminiRequest, strategy transform.ModelStrategy) (*transform.GeminiResponse, error) {
	if strategy == nil {
		strategy = transform.NewModelFamilyRouter().For(model)
	}
	// L3 预算循环：整批重试 max_retries 次（总轮数 max_retries+1），仅 Transient 退避重试。
	// 非流式无部分交付（不标 Truncated），中途断流只是候选失败，交预算循环处置。
	totalRounds := c.cfg.MaxRetries() + 1
	var bestErr error
	run := func(ctx context.Context, proxyURI string) (*transform.GeminiResponse, error) {
		return c.runSingleCandidate(ctx, model, req, proxyURI, strategy)
	}
	for round := 1; round <= totalRounds; round++ {
		timeoutSec := c.cfg.RequestTimeoutSeconds()
		if timeoutSec <= 0 {
			timeoutSec = 180
		}
		roundTimeout := time.Duration(timeoutSec) * time.Second
		roundCtx, roundCancel := context.WithTimeout(ctx, roundTimeout)

		resp, err := RunRace(roundCtx, c.cfg, run, WithWinningCheck(func(resp *transform.GeminiResponse) bool {
			return candidateFinishTyped(resp) == "STOP" && strategy.IsValidResponse(resp)
		}), WithCollectedFinalizer(func(results []raceResult[*transform.GeminiResponse]) (*transform.GeminiResponse, error) {
			cr := make([]candidateResult, len(results))
			for i, r := range results {
				cr[i] = candidateResult{proxyURI: r.uri, resp: r.val, err: r.err}
			}
			return pickBestResult(cr, strategy)
		}))
		if err == nil {
			roundCancel()
			return resp, nil
		}
		roundCancel()
		if ctx.Err() != nil {
			return nil, NormalizeError(ctx.Err())
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			err = NewNetworkError(err)
		}
		bestErr = pickBestError([]error{bestErr, err})
		retry, backoff := retryableAndBudgetLeft(err, round, totalRounds, ctx)
		if !retry {
			break
		}
		if sleepErr := sleepCtx(ctx, backoff); sleepErr != nil {
			break
		}
	}
	return nil, NormalizeError(bestErr)
}

// runSingleCandidate 执行单候选单次尝试（L1 透传层非流式版）：
// 单次建连 + 取 token + 单次 attempt，原样上报真实错误。
func (c *VertexAIClient) runSingleCandidate(ctx context.Context, model string, req *transform.GeminiRequest, proxyURI string, strategy transform.ModelStrategy) (*transform.GeminiResponse, error) {
	var chunks []*transform.GeminiChunk
	var validChunkCount int
	sess, err := c.net.CreateSession(sessionTimeoutFromContext(ctx, 180), proxyURI, RequestIDFromContext(ctx))
	if err != nil {
		return nil, NewInternalError("create session: "+err.Error(), nil)
	}
	defer sess.Close()
	tok, err := c.pool.GetTokenShared(ctx)
	if err != nil || tok == "" {
		return nil, NewRecaptchaUnavailableError("Could not fetch recaptcha token", err)
	}
	err = withRTFirstTryCompensation(ctx, func() error {
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
	if err != nil {
		return nil, NormalizeError(err)
	}
	if validChunkCount == 0 {
		return nil, NewEmptyResponseError("Upstream returned no valid content", nil)
	}

	result := collectChunksToParseResultTyped(chunks)
	resp, err := c.buildCompleteResponseTyped(result)
	if err != nil {
		return nil, err
	}

	// 防御性回退：直接调用方（未经 transform.BuildVariables 装配）若未带 SafetySettings
	// 且上游返回 SAFETY，则以统一固定 4×OFF 基座重试一次。生产管线请求恒带 4×OFF，此分支不触发。
	if req.SafetySettings == nil && candidateFinishTyped(resp) == "SAFETY" {
		if ctx.Err() != nil {
			return nil, NormalizeError(ctx.Err())
		}
		retryReq := *req
		retryReq.SafetySettings = transform.BuildSafetySettingsTyped(nil)
		return c.runSingleCandidate(ctx, model, &retryReq, proxyURI, strategy)
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
