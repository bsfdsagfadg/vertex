package subscriptions

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

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

func TestDeleteAndRecreateRejectsOldInFlightResult(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VPROXY_CONFIG", filepath.Join(dir, "config.json"))
	if err := config.LoadSubscriptions(); err != nil {
		t.Fatal(err)
	}
	if err := db.InitDB(filepath.Join(dir, "data.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.CloseDB)

	sub := config.Subscription{ID: "sub-reused", Name: "Old", URL: "https://example.com/old"}
	if err := config.UpdateSubscription(sub); err != nil {
		t.Fatal(err)
	}
	old, _ := config.GetSubscription(sub.ID)
	started := make(chan struct{})
	release := make(chan struct{})
	staleURI := "vless://stale@example.com:443#stale-recreate"
	service := New(func(_ context.Context, _, _ string) ([]nodes.Node, error) {
		close(started)
		<-release
		return []nodes.Node{{RawURI: staleURI, Name: "stale"}}, nil
	})
	errCh := make(chan error, 1)
	go func() { errCh <- service.Update(context.Background(), sub.ID) }()
	<-started
	if err := service.DeleteSubscription(sub.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := service.SaveSubscription(config.Subscription{ID: sub.ID, Name: "New", URL: "https://example.com/new"}); err != nil {
		t.Fatal(err)
	}
	current, _ := config.GetSubscription(sub.ID)
	if current.Generation == old.Generation {
		t.Fatal("recreated subscription generation must change")
	}
	close(release)
	if err := <-errCh; !errors.Is(err, ErrStaleResult) {
		t.Fatalf("old in-flight result must be stale after recreation: %v", err)
	}
	for _, node := range nodes.LoadNodes() {
		if node.RawURI == staleURI {
			t.Fatal("old in-flight result imported a node into the recreated subscription")
		}
	}
}

func TestStopRejectsRestartUntilWorkersExit(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VPROXY_CONFIG", filepath.Join(dir, "config.json"))
	if err := config.LoadSubscriptions(); err != nil {
		t.Fatal(err)
	}
	if err := config.UpdateSubscription(config.Subscription{ID: "sub-stop", Name: "Stop", URL: "https://example.com/stop"}); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	service := New(func(ctx context.Context, _, _ string) ([]nodes.Node, error) {
		close(started)
		<-release
		return nil, ctx.Err()
	})
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !service.Trigger("sub-stop") {
		t.Fatal("expected update trigger")
	}
	<-started
	stopped := make(chan struct{})
	go func() {
		service.Stop()
		close(stopped)
	}()
	deadline := time.Now().Add(time.Second)
	for {
		service.lifecycleMu.Lock()
		stopping := service.stopping
		service.lifecycleMu.Unlock()
		if stopping {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("service did not enter stopping state")
		}
		time.Sleep(time.Millisecond)
	}
	if err := service.Start(context.Background()); !errors.Is(err, ErrServiceStopping) {
		t.Fatalf("restart during stop must fail with ErrServiceStopping: %v", err)
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("service stop did not finish")
	}
	if err := service.Start(context.Background()); err != nil {
		t.Fatalf("service must restart after stop completes: %v", err)
	}
	service.Stop()
}

func TestRunningIDsReportsActiveUpdates(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VPROXY_CONFIG", filepath.Join(dir, "config.json"))
	if err := config.LoadSubscriptions(); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []config.Subscription{
		{ID: "sub-b", Name: "B", URL: "https://example.com/b"},
		{ID: "sub-a", Name: "A", URL: "https://example.com/a"},
	} {
		if err := config.UpdateSubscription(sub); err != nil {
			t.Fatal(err)
		}
	}
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	service := New(func(_ context.Context, _, _ string) ([]nodes.Node, error) {
		started <- struct{}{}
		<-release
		return nil, errors.New("done")
	})
	if !service.Trigger("sub-b") || !service.Trigger("sub-a") {
		t.Fatal("expected both updates to start")
	}
	<-started
	<-started
	ids := service.RunningIDs()
	if len(ids) != 2 || ids[0] != "sub-a" || ids[1] != "sub-b" {
		t.Fatalf("unexpected running IDs: %v", ids)
	}
	close(release)
	service.Stop()
	if ids = service.RunningIDs(); len(ids) != 0 {
		t.Fatalf("completed updates must be removed: %v", ids)
	}
}

func TestDeleteRestoresSubscriptionWhenNodeCleanupFails(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VPROXY_CONFIG", filepath.Join(dir, "config.json"))
	if err := config.LoadSubscriptions(); err != nil {
		t.Fatal(err)
	}
	if err := db.InitDB(filepath.Join(dir, "data.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.CloseDB)

	sub := config.Subscription{ID: "sub-restore", Name: "Restore", URL: "https://example.com/restore"}
	if err := config.UpdateSubscription(sub); err != nil {
		t.Fatal(err)
	}
	before, _ := config.GetSubscription(sub.ID)
	if err := nodes.ReplaceSubscriptionNodes(sub.ID, []nodes.Node{{RawURI: "vless://restore@example.com:443#restore", Name: "restore"}}, false); err != nil {
		t.Fatal(err)
	}
	if err := db.GlobalDB.Close(); err != nil {
		t.Fatal(err)
	}
	service := New(nil)
	if err := service.DeleteSubscription(sub.ID, true); err == nil {
		t.Fatal("closed database must make node cleanup fail")
	}
	restored, ok := config.GetSubscription(sub.ID)
	if !ok {
		t.Fatal("subscription config must be restored after node cleanup failure")
	}
	if restored.Generation == before.Generation {
		t.Fatal("restored subscription must use a new generation to invalidate in-flight work")
	}
}
