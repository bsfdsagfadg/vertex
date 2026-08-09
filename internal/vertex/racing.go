package vertex

import (
	"context"
	"fmt"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
)

func StreamParallel(ctx context.Context, cfg config.ConfigProvider,
	op func(context.Context, string) <-chan StreamChunk,
	yield func(StreamChunk) bool,
) {
	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()

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
			if chunk.Data != nil && isValidContentChunkTyped(chunk.Data) {
				hasValidContent = true
				break
			}
		}

		if firstErr != nil {
			return nil, firstErr
		}
		if !hasValidContent || len(buffered) == 0 {
			return nil, NewEmptyResponseError(fmt.Sprintf("stream: %s closed with no valid content", nodes.GetNodeName(uri)), nil)
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

	winnerCh, err := RunRace(streamCtx, cfg, wrappedOp, WithPreserveRaceCtxOnWin[<-chan StreamChunk](), WithFailFastOnHardError[<-chan StreamChunk]())
	if err != nil {
		vertexErr, ok := err.(*VertexError)
		if ok {
			yield(StreamChunk{Err: vertexErr})
		} else {
			yield(StreamChunk{Err: NewInternalError(err.Error(), nil)})
		}
		return
	}
	for chunk := range winnerCh {
		if !yield(chunk) {
			return
		}
	}
}
