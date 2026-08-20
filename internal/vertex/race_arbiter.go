package vertex

import (
	"context"
	"fmt"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/nodes"
)

// ResultArbiter coordinates winner evaluation, non-winning candidate collection,
// error prioritization, and background result draining.
type ResultArbiter[T any] struct {
	cfg          raceConfig[T]
	stickyPool   *nodes.StickyNodePool
	recorder     *ResultRecorder[T]
	failedErrors []error
}

// NewResultArbiter constructs a new ResultArbiter.
func NewResultArbiter[T any](rc raceConfig[T], stickyPool *nodes.StickyNodePool) *ResultArbiter[T] {
	return &ResultArbiter[T]{
		cfg:          rc,
		stickyPool:   stickyPool,
		recorder:     NewResultRecorder[T](stickyPool),
		failedErrors: make([]error, 0),
	}
}

// IsWinning checks if a successful result qualifies as an immediate winner.
func (a *ResultArbiter[T]) IsWinning(val T) bool {
	if a.cfg.isWinningResult == nil {
		return true
	}
	return a.cfg.isWinningResult(val)
}

// RecordWinner records the winning candidate node's health and sticky state.
func (a *ResultArbiter[T]) RecordWinner(uri string) {
	nodes.RecordTest(uri, true, 50, "")
	if a.stickyPool != nil {
		a.stickyPool.Add(uri)
	}
}

// RecordNonWinning collects a non-winning successful result for later finalization.
func (a *ResultArbiter[T]) RecordNonWinning(res raceResult[T]) {
	a.cfg.collectedResults = append(a.cfg.collectedResults, res)
	nodes.RecordTest(res.uri, true, 50, "")
	if a.stickyPool != nil {
		a.stickyPool.Add(res.uri)
	}
}

// RecordFailure records a candidate failure, updates health/sticky, and tracks the error.
func (a *ResultArbiter[T]) RecordFailure(res raceResult[T]) {
	a.recorder.Record(res)
	a.failedErrors = append(a.failedErrors, res.err)
}

// HasCollected returns true if there are collected non-winning successful results.
func (a *ResultArbiter[T]) HasCollected() bool {
	return len(a.cfg.collectedResults) > 0
}

// FinalizeCollected chooses the best result among collected non-winning candidates.
func (a *ResultArbiter[T]) FinalizeCollected() (T, error) {
	var zero T
	if len(a.cfg.collectedResults) == 0 {
		return zero, fmt.Errorf("no collected results available")
	}
	if a.cfg.finalizeCollected != nil {
		return a.cfg.finalizeCollected(a.cfg.collectedResults)
	}
	return a.cfg.collectedResults[0].val, nil
}

// PickBestError selects the highest priority error among candidate failures.
func (a *ResultArbiter[T]) PickBestError() error {
	return pickBestError(a.failedErrors)
}

// DrainBackgroundResults spawns a background worker to cleanly drain and record
// the outcome of remaining in-flight candidates after a winner has been decided.
func (a *ResultArbiter[T]) DrainBackgroundResults(
	launcher *CandidateLauncher[T],
	resCh <-chan raceResult[T],
	cancelRace context.CancelFunc,
	poolSize int,
) {
	if launcher.ActiveCount() == 0 {
		if !a.cfg.noCancelOnSuccess && cancelRace != nil {
			cancelRace()
		}
		return
	}

	collectTimeout := time.Duration(min(30, 5+poolSize)) * time.Second
	go func() {
		collectCtx, collectCancel := context.WithTimeout(context.Background(), collectTimeout)
		defer collectCancel()
		defer func() {
			if !a.cfg.noCancelOnSuccess && cancelRace != nil {
				cancelRace()
			}
		}()

		for {
			select {
			case bgRes := <-resCh:
				launcher.DecrementActive()
				a.recorder.Record(bgRes)
				if launcher.ActiveCount() == 0 {
					return
				}
			case <-collectCtx.Done():
				return
			}
		}
	}()
}
