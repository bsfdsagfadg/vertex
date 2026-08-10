package vertex

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/recaptcha"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

func TestPrepareRequestDoesNotPrefetchOrShareToken(t *testing.T) {
	var fetches atomic.Int32
	pool := recaptcha.NewTokenPoolCustomContext(func(context.Context, string) (string, error) {
		fetches.Add(1)
		return "unexpected", nil
	})
	cfg := config.StaticProvider(config.DefaultConfig())
	client := &VertexAIClient{pool: pool, cfg: cfg} //nolint:exhaustruct

	ctx := context.WithValue(context.Background(), RequestIDKey{}, "request-id")
	routedCtx := client.prepareRequest(ctx)
	if got := fetches.Load(); got != 0 {
		t.Fatalf("prepareRequest 不应预取或创建请求级 token，实际 fetch=%d", got)
	}
	if got := transport.RequestIDFromContext(routedCtx); got != "request-id" {
		t.Fatalf("请求路由上下文丢失 request-id: %q", got)
	}
}
