package vertex

import (
	"context"
	"errors"
	"fmt"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

func StreamParallel(ctx context.Context, cfg config.ConfigProvider,
	op func(context.Context, string) <-chan StreamChunk,
	yield func(StreamChunk) bool,
) {
	streamParallelWithDependencies(ctx, cfg, op, yield, legacyRaceDependencies())
}

func streamParallelWithDependencies(ctx context.Context, cfg config.ConfigProvider,
	op func(context.Context, string) <-chan StreamChunk,
	yield func(StreamChunk) bool,
	dependencies raceDependencies,
) {
	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()

	wrappedOp := func(ctx context.Context, uri string) (<-chan StreamChunk, error) {
		ch := op(ctx, uri)
		const maxPreludeChunks = 64
		prelude := make([]StreamChunk, 0, 4)
		var firstSemantic StreamChunk
		for {
			select {
			case chunk, ok := <-ch:
				if !ok {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					return nil, NewEmptyResponseError(fmt.Sprintf("stream: %s closed with no semantic data", dependencies.nodes.NodeName(uri)))
				}
				if chunk.Err != nil {
					return nil, chunk.Err
				}
				if isValidContentChunk(chunk.Data) {
					firstSemantic = chunk
					goto committed
				}
				if len(prelude) >= maxPreludeChunks {
					return nil, NewEmptyResponseError(fmt.Sprintf("stream: %s produced too many non-semantic frames", dependencies.nodes.NodeName(uri)))
				}
				prelude = append(prelude, chunk)
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

	committed:
		rest := make(chan StreamChunk, 64+len(prelude))
		for _, chunk := range prelude {
			rest <- chunk
		}
		rest <- firstSemantic
		go func() {
			defer close(rest)
			for chunk := range ch {
				select {
				case rest <- chunk:
				case <-ctx.Done():
					return
				}
			}
		}()
		return rest, nil
	}

	winnerCh, err := runRaceWithDependencies(streamCtx, cfg, wrappedOp, dependencies, WithNoCancelOnSuccess[<-chan StreamChunk]())
	if err != nil {
		var vertexErr *VertexError
		if errors.As(err, &vertexErr) {
			yield(StreamChunk{Err: vertexErr})
		} else {
			yield(StreamChunk{Err: NewInternalError(err.Error())})
		}
		return
	}
	for chunk := range winnerCh {
		if !yield(chunk) {
			return
		}
	}
}
