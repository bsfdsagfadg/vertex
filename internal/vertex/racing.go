package vertex

import (
	"context"
	"errors"
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
		// Anonymous streaming commonly starts with metadata/default finish frames.
		// Do not let those frames win the race: wait for real content or an error,
		// while retaining every frame so the winning stream remains lossless.
		buffered := make([]StreamChunk, 0, 4)
		hasValidContent := false
		for {
			select {
			case chunk, ok := <-ch:
				if !ok {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					return nil, NewEmptyResponseError(fmt.Sprintf("stream: %s closed with no valid content", nodes.GetNodeName(uri)))
				}
				buffered = append(buffered, chunk)
				if chunk.Err != nil {
					return nil, chunk.Err
				}
				if streamChunkHasContent(chunk) {
					hasValidContent = true
				}
				if hasValidContent {
					goto contentReady
				}
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	contentReady:
		rest := make(chan StreamChunk, 64)
		for _, chunk := range buffered {
			rest <- chunk
		}
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

	winnerCh, err := RunRace(streamCtx, cfg, wrappedOp, WithNoCancelOnSuccess[<-chan StreamChunk]())
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

// streamChunkHasContent accepts both normalized Gemini chunks and the small
// map-shaped chunks used by lower-level callers/tests. Metadata-only frames
// must never decide the winner of a streaming race.
func streamChunkHasContent(chunk StreamChunk) bool {
	if chunk.Err != nil || chunk.Data == nil {
		return false
	}
	if text, ok := chunk.Data["text"].(string); ok && text != "" {
		return true
	}
	return isValidContentChunk(chunk.Data)
}
