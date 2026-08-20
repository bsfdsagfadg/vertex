package api

import (
	"context"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/buildinfo"
	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/vertex"
)

func TestNewServerDoesNotStartSubscriptions(t *testing.T) {
	cfg := config.DefaultConfig()
	srv := NewServer(
		vertex.NewVertexAIClient(config.StaticProvider(cfg)),
		NewAPIKeyManager(),
		config.StaticProvider(cfg),
		buildinfo.BuildInfo{Version: "2.0.0-test", Source: "local"},
	)
	t.Cleanup(srv.Close)

	if srv.subscriptions == nil {
		t.Fatal("subscription service was not constructed")
	}
	if srv.subscriptions.Started() {
		t.Fatal("NewServer must not start subscription background work")
	}
}

func TestServerStartAndCloseAreExplicit(t *testing.T) {
	cfg := config.DefaultConfig()
	srv := NewServer(
		vertex.NewVertexAIClient(config.StaticProvider(cfg)),
		NewAPIKeyManager(),
		config.StaticProvider(cfg),
		buildinfo.BuildInfo{Version: "2.0.0-test", Source: "local"},
	)

	ctx, cancel := context.WithCancel(context.Background())
	if err := srv.Start(ctx); err != nil {
		cancel()
		t.Fatalf("Start: %v", err)
	}
	if !srv.subscriptions.Started() {
		t.Fatal("Start must activate subscription background work")
	}
	cancel()
	srv.Close()
	srv.Close()
}
