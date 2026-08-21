package vertex

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/engine/transform"
	"github.com/bsfdsagfadg/vertex/internal/infra/config"
)

// retryableAndBudgetLeft 是预算循环的共享重试决策：Transient 且预算未耗尽才退避重试。
// ctx 取消、非 Transient、预算耗尽均返回不重试；退避统一 backoff(round)，不区分候选数。
func retryableAndBudgetLeft(err error, round, totalRounds int, ctx context.Context) (bool, time.Duration) {
	if ctx.Err() != nil {
		return false, 0
	}
	ve := asVertexError(err)
	if ve == nil || ve.ClassifyBatch() != Transient {
		return false, 0
	}
	if round >= totalRounds {
		return false, 0
	}
	return true, backoff(round)
}

func StreamParallel(ctx context.Context, pool NodePool, cfg config.ConfigProvider, model string,
	op func(context.Context, string) <-chan StreamChunk,
	yield func(StreamChunk) bool,
	strategy transform.ModelStrategy,
) {
	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()

	if strategy == nil {
		strategy = transform.SharedModelFamilyRouter().For(model)
	}

	wrappedOp := func(ctx context.Context, uri string) (<-chan StreamChunk, error) {
		ch := op(ctx, uri)

		// 必须读到至少一个包含真正有效 Content/ToolCall 的 Chunk，或者包含错误的 Chunk，
		// 才能判定本节点连通并可以作为竞速胜出者。如果读到的全是元数据包且通道随即关闭，
		// 视作 EmptyResponseError 并继续对冲。
		var buffered []StreamChunk
		var hasValidContent bool
		var firstErr error

		for chunk := range ch {
			buffered = append(buffered, chunk)
			if chunk.Err != nil {
				firstErr = chunk.Err
				break
			}
			if chunk.Data != nil && strategy.IsValidChunk(chunk.Data) {
				hasValidContent = true
				break
			}
		}

		if firstErr != nil {
			return nil, firstErr
		}
		if !hasValidContent || len(buffered) == 0 {
			return nil, NewEmptyResponseError(fmt.Sprintf("stream: %s closed with no valid content", pool.NodeName(uri)), nil)
		}

		rest := make(chan StreamChunk, 64)
		for _, b := range buffered {
			rest <- b
		}
		go func() {
			defer close(rest)
			for chunk := range ch {
				select {
				case rest <- chunk:
				case <-streamCtx.Done():
					return
				}
			}
		}()
		return rest, nil
	}

	// L3 预算循环：整批重试 max_retries 次（总轮数 max_retries+1），仅 Transient 退避重试。
	// 胜出路径（含 Truncated 错误帧）直透传，绝不重试；FailFast/Terminal 立即 break。
	totalRounds := cfg.MaxRetries() + 1
	var bestErr error
	for round := 1; round <= totalRounds; round++ {
		log.Printf("[Vertex] [Dispatch] 发起请求 (Round %d/%d), 模型=%s, 请求ID=%s", round, totalRounds, model, RequestIDFromContext(ctx))
		timeoutSec := cfg.RequestTimeoutSeconds()
		if timeoutSec <= 0 {
			timeoutSec = 180
		}
		roundTimeout := time.Duration(timeoutSec) * time.Second
		roundCtx, roundCancel := context.WithTimeout(streamCtx, roundTimeout)

		winnerCh, err := RunRace(roundCtx, pool, cfg, wrappedOp,
			WithPreserveRaceCtxOnWin[<-chan StreamChunk](),
			WithFailFastOnHardError[<-chan StreamChunk]())
		if err == nil {
			// 胜出（winnerCh 非 nil）：提交，不再重试
			for chunk := range winnerCh {
				if !yield(chunk) {
					roundCancel()
					streamCancel()
					return
				}
			}
			roundCancel()
			return
		}
		roundCancel()
		// 客户端取消/上下文超时静默早退，不产错误帧
		if streamCtx.Err() != nil {
			return
		}
		// 本轮 roundCtx 超时或取消（但客户端未断）：归一化为 NetworkError (Transient)，以便下轮重试
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			err = NewNetworkError(err)
		}
		// RunRace 返回错误（无胜出者）
		bestErr = pickBestError([]error{bestErr, err})
		retry, backoff := retryableAndBudgetLeft(err, round, totalRounds, streamCtx)
		if !retry {
			break
		}
		if sleepErr := sleepCtx(streamCtx, backoff); sleepErr != nil {
			break
		}
	}
	// 预算耗尽或不可重试
	yield(StreamChunk{Err: NormalizeError(bestErr)})
	log.Printf("[Vertex] [Dispatch] 重试轮次耗尽 (%d 轮均失败), 请求ID=%s", totalRounds, RequestIDFromContext(ctx))
}
