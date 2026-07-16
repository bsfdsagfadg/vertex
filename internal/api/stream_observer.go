package api

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/cli"
	"github.com/bsfdsagfadg/vertex/internal/vertex"
)

type streamObserver struct {
	triggered bool
	startTime time.Time
}

func newStreamObserver(startTime time.Time) *streamObserver {
	return &streamObserver{startTime: startTime}
}

func (o *streamObserver) observe(ctx context.Context, chunk vertex.StreamChunk, sseEvents []string) {
	if o.triggered {
		return
	}
	if chunk.Err != nil {
		return
	}
	if !hasValidStreamOutput(sseEvents) {
		return
	}
	o.markTriggered(ctx)
}

func (o *streamObserver) markTriggered(ctx context.Context) {
	if o.triggered {
		return
	}
	o.triggered = true
	rid := vertex.RequestIDFromContext(ctx)
	if rid == "" {
		rid = "unknown"
	}
	elapsed := time.Since(o.startTime).Seconds()
	log.Printf("[Server] [Stream] 请求ID=%s 首个有效输出耗时: %.2fs", rid, elapsed)
	cli.UpdateReqState(rid, "流式打字中...", "\033[36m", "正在输出...")
}

func hasValidStreamOutput(events []string) bool {
	for _, ev := range events {
		if strings.Contains(ev, `"content":`) && !strings.Contains(ev, `"content":null`) && !strings.Contains(ev, `"content":""`) {
			return true
		}
		if strings.Contains(ev, `"reasoning_content":`) {
			return true
		}
		if strings.Contains(ev, `"tool_calls":`) {
			return true
		}
	}
	return false
}

func hasGeminiValidOutput(chunk map[string]any) bool {
	cands, ok := chunk["candidates"].([]any)
	if !ok || len(cands) == 0 {
		return false
	}
	for _, cRaw := range cands {
		cand, ok := cRaw.(map[string]any)
		if !ok {
			continue
		}
		content, ok := cand["content"].(map[string]any)
		if !ok {
			continue
		}
		parts, ok := content["parts"].([]any)
		if !ok || len(parts) == 0 {
			continue
		}
		for _, pRaw := range parts {
			part, ok := pRaw.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := part["text"].(string); ok && text != "" {
				return true
			}
			if thought, ok := part["thought"].(string); ok && thought != "" {
				return true
			}
			if fc, ok := part["functionCall"].(map[string]any); ok && fc != nil {
				return true
			}
		}
	}
	return false
}