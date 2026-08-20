package scheduler

import (
	"context"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/repository"
)

func newTestRoutePlanner(t *testing.T) (*RoutePlanner, *repository.SQLite) {
	t.Helper()
	store, err := repository.Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	planner, err := NewRoutePlanner(store, NewHealthTracker())
	if err != nil {
		t.Fatalf("new route planner: %v", err)
	}
	return planner, store
}

func TestAddGlobalProxyDuplicateMergesPinAndSourcesAtomically(t *testing.T) {
	planner, _ := newTestRoutePlanner(t)
	ctx := context.Background()
	uri := "socks5://127.0.0.1:1180#first"
	if _, err := planner.AddGlobalProxy(ctx, uri, "manual", "", false, false); err != nil {
		t.Fatalf("add global proxy: %v", err)
	}
	got, err := planner.AddGlobalProxy(ctx, "SOCKS5://127.0.0.1:1180#renamed", "request_node", "rn-1", true, false)
	if err != nil {
		t.Fatalf("merge global proxy: %v", err)
	}
	if !got.Pinned || len(got.Sources) != 2 {
		t.Fatalf("returned candidate does not reflect merged state: %+v", got)
	}
	if _, err := planner.AddGlobalProxy(ctx, uri, "request_node", "rn-1", true, false); err != nil {
		t.Fatalf("repeat global proxy source: %v", err)
	}
	all, err := planner.ListGlobalProxies(ctx)
	if err != nil {
		t.Fatalf("list global proxies: %v", err)
	}
	if len(all) != 1 || !all[0].Pinned || len(all[0].Sources) != 2 {
		t.Fatalf("unexpected persisted merge: %+v", all)
	}
}

func TestSetGlobalProxyEnabledManualRetryResetsAdmissionGates(t *testing.T) {
	planner, _ := newTestRoutePlanner(t)
	ctx := context.Background()
	uri := "socks5://127.0.0.1:1181#retry"
	if _, err := planner.AddGlobalProxy(ctx, uri, "manual", "", false, false); err != nil {
		t.Fatalf("add global proxy: %v", err)
	}
	autoDisabled, err := planner.UpdateGlobalProxyResult(ctx, uri, false, 17, "timeout", 120, true, true, 1)
	if err != nil || !autoDisabled {
		t.Fatalf("auto disable: disabled=%v err=%v", autoDisabled, err)
	}
	if err := planner.SetGlobalProxyEnabled(ctx, uri, true); err != nil {
		t.Fatalf("manual enable: %v", err)
	}
	all, err := planner.ListGlobalProxies(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("list global proxies: values=%+v err=%v", all, err)
	}
	got := all[0]
	if got.Disabled || got.CooldownUntil != 0 || got.ConsecutiveFailures != 0 {
		t.Fatalf("manual retry did not clear admission gates: %+v", got)
	}
	if got.LastTestError != "timeout" || got.LastTestMs != 17 || got.LastTestAt == 0 {
		t.Fatalf("manual retry lost last diagnostics: %+v", got)
	}
}
