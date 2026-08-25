package vertex

import (
	"context"
	"fmt"
	"log"

	"github.com/bsfdsagfadg/vertex/internal/engine/transform"
	"github.com/bsfdsagfadg/vertex/internal/infra/config"
)

// StreamParallel 是真流式路径的窗口竞速交付层：
// 单次调用恒满窗口引擎（补位、预算、FailFast 早退均由 RunRace 内部自治），
// 胜出后 drain winnerCh 逐帧 yield；无胜出者时收敛错误帧透传。
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

	log.Printf("[Vertex] [Dispatch] 发起窗口竞速请求, 模型=%s, 请求ID=%s", model, RequestIDFromContext(ctx))
	winnerCh, err := RunRace(streamCtx, pool, cfg, wrappedOp,
		WithPreserveRaceCtxOnWin[<-chan StreamChunk](),
		WithFailFastOnHardError[<-chan StreamChunk]())
	if err != nil {
		// 客户端取消静默早退，不产错误帧
		if streamCtx.Err() != nil {
			return
		}
		yield(StreamChunk{Err: NormalizeError(err)})
		log.Printf("[Vertex] [Dispatch] 窗口竞速失败收敛, 模型=%s, 请求ID=%s", model, RequestIDFromContext(ctx))
		return
	}
	for chunk := range winnerCh {
		if !yield(chunk) {
			streamCancel()
			return
		}
	}
}
