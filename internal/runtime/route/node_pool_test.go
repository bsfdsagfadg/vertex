package route

import (
	"context"
	"testing"
	"time"

	runtimenodes "github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/repository"
)

func newTestNodePool(t *testing.T) (*NodePool, *repository.SQLite) {
	t.Helper()
	store, err := repository.Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	pool, err := NewNodePool(store)
	if err != nil {
		t.Fatalf("new node pool: %v", err)
	}
	return pool, store
}

func TestReplaceSubscriptionNodesPreservesOrAdoptsManualOwnership(t *testing.T) {
	pool, store := newTestNodePool(t)
	ctx := context.Background()
	uri := "socks5://127.0.0.1:2080#shared"
	manual := runtimenodes.Node{Type: "socks5", Name: "manual", RawURI: uri}
	subscription := runtimenodes.Node{Type: "socks5", Name: "subscription", RawURI: uri}
	if err := pool.ImportManualNodes(ctx, []runtimenodes.Node{manual}, false); err != nil {
		t.Fatalf("import manual node: %v", err)
	}
	if err := pool.ReplaceSubscriptionNodes(ctx, "sub-a", []runtimenodes.Node{subscription}, false); err != nil {
		t.Fatalf("merge subscription source: %v", err)
	}
	records, sources, _, err := store.LoadNodeState(ctx)
	if err != nil {
		t.Fatalf("load merged node state: %v", err)
	}
	if len(records) != 1 || records[0].Name != "manual" || len(sources) != 2 {
		t.Fatalf("manual ownership was overwritten: records=%+v sources=%+v", records, sources)
	}
	if err := pool.ReplaceSubscriptionNodes(ctx, "sub-a", []runtimenodes.Node{subscription}, true); err != nil {
		t.Fatalf("adopt manual node: %v", err)
	}
	records, sources, _, err = store.LoadNodeState(ctx)
	if err != nil {
		t.Fatalf("load adopted node state: %v", err)
	}
	if len(records) != 1 || records[0].Name != "subscription" || len(sources) != 1 || sources[0].SourceType != runtimenodes.SourceSubscription {
		t.Fatalf("subscription did not adopt manual ownership: records=%+v sources=%+v", records, sources)
	}
	if err := pool.RemoveSubscriptionSource(ctx, "sub-a", false); err != nil {
		t.Fatalf("convert subscription node to manual: %v", err)
	}
	records, sources, _, err = store.LoadNodeState(ctx)
	if err != nil {
		t.Fatalf("load converted node state: %v", err)
	}
	if len(records) != 1 || len(sources) != 1 || sources[0].SourceType != runtimenodes.SourceManual {
		t.Fatalf("subscription node was not converted to manual ownership: records=%+v sources=%+v", records, sources)
	}
}

func TestNodePoolDeleteCallbackRunsAfterPersistenceAndOutsideLock(t *testing.T) {
	pool, store := newTestNodePool(t)
	ctx := context.Background()
	uri := "socks5://127.0.0.1:2081#callback"
	if err := pool.ReplaceSubscriptionNodes(ctx, "sub-delete", []runtimenodes.Node{{Type: "socks5", Name: "callback", RawURI: uri}}, false); err != nil {
		t.Fatalf("add subscription node: %v", err)
	}
	callback := make(chan error, 1)
	pool.SetDeleteCallback(func(gotURI string) {
		if gotURI != uri {
			callback <- &unexpectedCallbackURI{got: gotURI, want: uri}
			return
		}
		records, _, _, err := store.LoadNodeState(ctx)
		if err == nil && len(records) != 0 {
			err = &callbackBeforePersistence{count: len(records)}
		}
		if err == nil {
			_, _, err = pool.List(ctx)
		}
		callback <- err
	})
	if err := pool.RemoveSubscriptionSource(ctx, "sub-delete", true); err != nil {
		t.Fatalf("remove subscription source: %v", err)
	}
	select {
	case err := <-callback:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("delete callback blocked while re-entering the node pool")
	}
}

type unexpectedCallbackURI struct{ got, want string }

func (e *unexpectedCallbackURI) Error() string {
	return "delete callback received an unexpected URI: got " + e.got + ", want " + e.want
}

type callbackBeforePersistence struct{ count int }

func (e *callbackBeforePersistence) Error() string {
	return "delete callback ran before persistence completed"
}
