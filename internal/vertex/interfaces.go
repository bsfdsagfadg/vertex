package vertex

import "context"

type ChatCompleter interface {
	CompleteChat(ctx context.Context, model string, geminiPayload map[string]any) (map[string]any, error)
	StreamChat(ctx context.Context, model string, geminiPayload map[string]any, yield func(StreamChunk) bool)
}
