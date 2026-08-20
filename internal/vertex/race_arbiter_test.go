package vertex

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
)

func TestCandidateScheduler_DynamicAndFixedDelay(t *testing.T) {
	// Fixed delay
	cfgFixed := config.StaticProvider(config.AppConfig{
		ParallelPoolEnabled:      true,
		ParallelPoolSize:         3,
		ParallelNodeTopK:         80,
		ParallelPoolDelayDynamic: false,
		ParallelPoolDelayMs:      200,
	})
	schedFixed := NewCandidateScheduler(cfgFixed)
	schedFixed.PrepareRound([]nodes.Node{
		{Name: "n1", RawURI: "http://n1"},
		{Name: "n2", RawURI: "http://n2"},
	})
	if schedFixed.delay != 200*time.Millisecond {
		t.Fatalf("Expected 200ms delay, got %v", schedFixed.delay)
	}
	if !schedFixed.HasMore() {
		t.Fatal("Expected more candidates")
	}
	c1, ok1 := schedFixed.NextCandidate()
	if !ok1 || c1.Name != "n1" {
		t.Fatalf("Expected n1, got %v", c1)
	}
	schedFixed.StopTimer()

	// Dynamic delay
	cfgDynamic := config.StaticProvider(config.AppConfig{
		ParallelPoolEnabled:      true,
		ParallelPoolSize:         3,
		ParallelNodeTopK:         80,
		ParallelPoolDelayDynamic: true,
	})
	schedDynamic := NewCandidateScheduler(cfgDynamic)
	schedDynamic.PrepareRound([]nodes.Node{
		{Name: "n1", RawURI: "http://n1"},
	})
	schedDynamic.StopTimer()
}

func TestCandidateScheduler_RoundProgression(t *testing.T) {
	nodes.MergeNodes([]nodes.Node{
		{Type: "http", Name: "n1", RawURI: "http://sched_node1:8080"},
		{Type: "http", Name: "n2", RawURI: "http://sched_node2:8080"},
		{Type: "http", Name: "n3", RawURI: "http://sched_node3:8080"},
		{Type: "http", Name: "n4", RawURI: "http://sched_node4:8080"},
	})
	t.Cleanup(func() {
		nodes.DeleteNode("http://sched_node1:8080")
		nodes.DeleteNode("http://sched_node2:8080")
		nodes.DeleteNode("http://sched_node3:8080")
		nodes.DeleteNode("http://sched_node4:8080")
	})

	cfg := config.StaticProvider(config.AppConfig{
		ParallelPoolEnabled:      true,
		ParallelPoolSize:         2,
		ParallelNodeTopK:         80,
		ParallelPoolRetryEnabled: false,
		MaxRetries:               1,
	})

	sched := NewCandidateScheduler(cfg)
	cands, ok := sched.SelectInitialCandidates()
	if !ok || len(cands) == 0 {
		t.Fatalf("Expected initial candidates, got %v", cands)
	}
	sched.PrepareRound(cands)
	for sched.HasMore() {
		_, _ = sched.NextCandidate()
	}

	nextCands, hasNext := sched.NextRound()
	if !hasNext || len(nextCands) == 0 {
		t.Fatalf("Expected next round candidates, got %v", nextCands)
	}
	sched.StopTimer()
}

func TestResultArbiter_WinnerAndCollected(t *testing.T) {
	var rc raceConfig[string]
	WithWinningCheck[string](func(v string) bool {
		return v == "WIN"
	})(&rc)
	WithCollectedFinalizer[string](func(results []raceResult[string]) (string, error) {
		if len(results) == 0 {
			return "", errors.New("empty")
		}
		return results[len(results)-1].val, nil
	})(&rc)

	pool := nodes.GetStickyPool()
	arbiter := NewResultArbiter[string](rc, pool)

	if !arbiter.IsWinning("WIN") {
		t.Fatal("Expected WIN to be winning")
	}
	if arbiter.IsWinning("PARTIAL") {
		t.Fatal("Expected PARTIAL not to be immediate winner")
	}

	arbiter.RecordNonWinning(raceResult[string]{uri: "http://node1", val: "PARTIAL1"})
	arbiter.RecordNonWinning(raceResult[string]{uri: "http://node2", val: "PARTIAL2"})

	if !arbiter.HasCollected() {
		t.Fatal("Expected collected results")
	}

	final, err := arbiter.FinalizeCollected()
	if err != nil {
		t.Fatalf("FinalizeCollected error: %v", err)
	}
	if final != "PARTIAL2" {
		t.Fatalf("Expected PARTIAL2 (from custom finalizer), got %s", final)
	}
}

func TestResultArbiter_ErrorPrioritization(t *testing.T) {
	arbiter := NewResultArbiter[string](raceConfig[string]{}, nil)

	arbiter.RecordFailure(raceResult[string]{uri: "http://n1", err: NewInternalError("internal error")})
	arbiter.RecordFailure(raceResult[string]{uri: "http://n2", err: NewRateLimitError("too many requests", 60)})
	arbiter.RecordFailure(raceResult[string]{uri: "http://n3", err: NewInvalidArgumentError("invalid argument")})

	best := arbiter.PickBestError()
	var ve *VertexError
	if !errors.As(best, &ve) || ve.Kind != "ratelimit" {
		t.Fatalf("Expected ratelimit error to have highest priority, got %v", best)
	}
}

func TestResultArbiter_DrainBackgroundResults(t *testing.T) {
	resCh := make(chan raceResult[string], 2)
	launcher := NewCandidateLauncher[string](context.Background(), 10, func(ctx context.Context, proxyURI string) (string, error) {
		return "done", nil
	})

	launcher.Launch(nodes.Node{Name: "n1", RawURI: "http://n1"}, 0, resCh)
	launcher.Launch(nodes.Node{Name: "n2", RawURI: "http://n2"}, 0, resCh)

	arbiter := NewResultArbiter[string](raceConfig[string]{}, nil)
	canceled := false
	cancelFn := func() {
		canceled = true
	}

	arbiter.DrainBackgroundResults(launcher, resCh, cancelFn, 2)

	deadline := time.Now().Add(2 * time.Second)
	for launcher.ActiveCount() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if launcher.ActiveCount() != 0 {
		t.Fatalf("Expected active candidates to reach 0, got %d", launcher.ActiveCount())
	}
	if !canceled {
		t.Fatal("Expected cancelRace to be called after background drain")
	}
}
