package subscriptions

import (
	"context"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/repository"
)

func newTestSubscriptionStore(t *testing.T) *RepositoryStore {
	t.Helper()
	repo, err := repository.Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	store, err := NewRepositoryStore(repo)
	if err != nil {
		t.Fatalf("new subscription store: %v", err)
	}
	return store
}

func TestRepositoryStoreSavePreservesRuntimeStatusAndBumpsRevision(t *testing.T) {
	store := newTestSubscriptionStore(t)
	ctx := context.Background()
	initial := config.Subscription{ID: "sub-a", Name: "A", URL: "https://example.com/a", UserAgent: "Chrome"}
	if err := store.Save(ctx, initial); err != nil {
		t.Fatalf("save initial subscription: %v", err)
	}
	created, ok, err := store.Get(ctx, initial.ID)
	if err != nil || !ok {
		t.Fatalf("get initial subscription: value=%+v ok=%v err=%v", created, ok, err)
	}
	if created.Revision != 1 || created.Generation == "" {
		t.Fatalf("initial tokens were not generated: %+v", created)
	}
	if updated, err := store.UpdateStatus(ctx, created.ID, created.Generation, created.Revision, 1234, "timeout"); err != nil || !updated {
		t.Fatalf("update runtime status: updated=%v err=%v", updated, err)
	}
	created.Name = "renamed"
	created.URL = "https://example.com/renamed"
	created.LastUpdateTime = 0
	created.LastError = ""
	if err := store.Save(ctx, created); err != nil {
		t.Fatalf("edit subscription: %v", err)
	}
	edited, ok, err := store.Get(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("get edited subscription: value=%+v ok=%v err=%v", edited, ok, err)
	}
	if edited.Revision != created.Revision+1 || edited.Generation != created.Generation {
		t.Fatalf("edit changed concurrency tokens incorrectly: %+v", edited)
	}
	if edited.LastUpdateTime != 1234 || edited.LastError != "timeout" {
		t.Fatalf("edit overwrote runtime status: %+v", edited)
	}
}

func TestRepositoryStoreUpdateStatusRejectsStaleTokens(t *testing.T) {
	store := newTestSubscriptionStore(t)
	ctx := context.Background()
	if err := store.Save(ctx, config.Subscription{ID: "sub-stale", Name: "stale", URL: "https://example.com/stale"}); err != nil {
		t.Fatalf("save subscription: %v", err)
	}
	current, _, err := store.Get(ctx, "sub-stale")
	if err != nil {
		t.Fatalf("get subscription: %v", err)
	}
	for _, tokens := range []struct {
		generation string
		revision   uint64
	}{
		{generation: "wrong", revision: current.Revision},
		{generation: current.Generation, revision: current.Revision + 1},
	} {
		updated, updateErr := store.UpdateStatus(ctx, current.ID, tokens.generation, tokens.revision, 99, "stale")
		if updateErr != nil || updated {
			t.Fatalf("stale status update was accepted: tokens=%+v updated=%v err=%v", tokens, updated, updateErr)
		}
	}
	after, _, err := store.Get(ctx, current.ID)
	if err != nil {
		t.Fatalf("get subscription after stale updates: %v", err)
	}
	if after.LastUpdateTime != 0 || after.LastError != "" {
		t.Fatalf("stale update changed runtime status: %+v", after)
	}
}
