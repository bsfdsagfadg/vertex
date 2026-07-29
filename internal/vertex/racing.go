package vertex

import (
	"context"
	"fmt"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
)

func RunParallel[T any](ctx context.Context, cfg config.ConfigProvider, run func(context.Context, string) (T, error)) (T, error) {
	return RunRace(ctx, cfg, run)
}

func StreamParallel(ctx context.Context, cfg config.ConfigProvider,
	op func(context.Context, string) <-chan StreamChunk,
	yield func(StreamChunk) bool,
) {
	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()

	wrappedOp := func(ctx context.Context, uri string) (<-chan StreamChunk, error) {
		ch := op(ctx, uri)
		first, ok := <-ch
		if !ok {
			return nil, NewEmptyResponseError(fmt.Sprintf("stream: %s closed with no data", nodes.GetNodeName(uri)), nil)
		}
		if first.Err != nil {
			return nil, first.Err
		}
		rest := make(chan StreamChunk, 64)
		rest <- first
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
