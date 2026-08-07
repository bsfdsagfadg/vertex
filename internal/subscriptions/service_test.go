package subscriptions

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/db"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
)

func TestServiceDiscardsStaleResultsAndKeepsDeletedNodesAsManual(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VPROXY_CONFIG", filepath.Join(dir, "config.json"))
	if err := config.LoadSubscriptions(); err != nil {
		t.Fatal(err)
	}
	if err := db.InitDB(filepath.Join(dir, "data.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.CloseDB)

	sub := config.Subscription{ID: "sub-a", Name: "A", URL: "https://example.com/original", UserAgent: "Chrome"}
	if err := config.UpdateSubscription(sub); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	service := New(func(_ context.Context, _, _ string) ([]nodes.Node, error) {
		close(started)
		<-release
		return []nodes.Node{{RawURI: "vless://id@example.com:443?security=tls#stale", Name: "stale"}}, nil
	})
	errCh := make(chan error, 1)
	go func() { errCh <- service.Update(context.Background(), sub.ID) }()
	<-started
	current, _ := config.GetSubscription(sub.ID)
	current.URL = "https://example.com/edited"
	if err := service.SaveSubscription(current); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-errCh; !errors.Is(err, ErrStaleResult) {
		t.Fatalf("expected stale result, got %v", err)
	}
	if got := len(nodes.LoadNodes()); got != 0 {
		t.Fatalf("stale update must not import nodes, got %d", got)
	}

	service = New(func(_ context.Context, _, _ string) ([]nodes.Node, error) {
		return []nodes.Node{{RawURI: "vless://id@example.com:443?security=tls#current", Name: "current"}}, nil
	})
	if err := service.Update(context.Background(), sub.ID); err != nil {
		t.Fatal(err)
	}
	list := nodes.LoadNodes()
	if len(list) != 1 {
		t.Fatalf("successful update should import one node: %+v", list)
	}
	if err := service.DeleteSubscription(sub.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := config.GetSubscription(sub.ID); ok {
		t.Fatal("subscription should be deleted")
	}
	sources := nodes.GetNodeSources(list[0].RawURI)
	if len(sources) != 1 || sources[0] != (nodes.NodeSource{Type: nodes.SourceManual}) {
		t.Fatalf("kept subscription nodes should become manual: %+v", sources)
	}
}

func TestCustomUAEditInvalidatesInFlightUpdate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VPROXY_CONFIG", filepath.Join(dir, "config.json"))
	if err := config.LoadSubscriptions(); err != nil {
		t.Fatal(err)
	}
	ua, err := config.SaveCustomUA(config.CustomUA{Name: "Primary", UserAgent: "agent-a"})
	if err != nil {
		t.Fatal(err)
	}
	sub := config.Subscription{ID: "sub-ua", Name: "UA", URL: "https://example.com/ua", CustomUAID: ua.ID}
	if err = config.UpdateSubscription(sub); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	service := New(func(_ context.Context, _ string, userAgent string) ([]nodes.Node, error) {
		if userAgent != "agent-a" {
			t.Errorf("unexpected initial UA: %s", userAgent)
		}
		close(started)
		<-release
		return []nodes.Node{{RawURI: "vless://id@example.com:8443#ua", Name: "ua"}}, nil
	})
	errCh := make(chan error, 1)
	go func() { errCh <- service.Update(context.Background(), sub.ID) }()
	<-started
	ua.UserAgent = "agent-b"
	if _, err = service.SaveCustomUA(ua); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err = <-errCh; !errors.Is(err, ErrStaleResult) {
		t.Fatalf("UA edit must invalidate in-flight result, got %v", err)
	}
}
