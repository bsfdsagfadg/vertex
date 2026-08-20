package vertex

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/scheduler"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

// candidateLauncher manages launching and lifecycle of single node race candidates.
type candidateLauncher[T any] struct {
	run         func(ctx context.Context, proxyURI string) (T, error)
	raceTimeout int
	parentCtx   context.Context
	keepSuccess bool
	cancels     map[string]context.CancelFunc
	cancelsMu   sync.Mutex
	active      int32
	nodes       raceNodeRuntime
}

func newCandidateLauncher[T any](parentCtx context.Context, raceTimeout int, keepSuccess bool, run func(ctx context.Context, proxyURI string) (T, error), nodeRuntime raceNodeRuntime) *candidateLauncher[T] {
	return &candidateLauncher[T]{
		run:         run,
		raceTimeout: raceTimeout,
		parentCtx:   parentCtx,
		keepSuccess: keepSuccess,
		cancels:     make(map[string]context.CancelFunc),
		nodes:       nodeRuntime,
	}
}

func (l *candidateLauncher[T]) CancelCandidate(key string) {
	l.cancelsMu.Lock()
	cancelFn := l.cancels[key]
	l.cancelsMu.Unlock()
	if cancelFn != nil {
		cancelFn()
	}
}

func (l *candidateLauncher[T]) CancelAllExcept(winnerKey string) {
	l.cancelsMu.Lock()
	for key, cancelFn := range l.cancels {
		if key != winnerKey {
			cancelFn()
		}
	}
	l.cancelsMu.Unlock()
}

func (l *candidateLauncher[T]) ActiveCount() int32 {
	return atomic.LoadInt32(&l.active)
}

func (l *candidateLauncher[T]) DecrementActive() int32 {
	return atomic.AddInt32(&l.active, -1)
}

func (l *candidateLauncher[T]) Launch(c scheduler.Candidate, round int, resCh chan<- raceResult[T]) {
	uri := c.Route.RequestNodeURI

	// The race timeout owns only the pre-commit phase. A streaming winner must
	// continue on the parent request lifetime after its first semantic event.
	candCtx, cancelCause := context.WithCancelCause(l.parentCtx)
	candCancel := func() { cancelCause(context.Canceled) }
	var timeout *time.Timer
	if l.raceTimeout > 0 {
		timeout = time.AfterFunc(time.Duration(l.raceTimeout)*time.Second, func() {
			cancelCause(context.DeadlineExceeded)
		})
	}
	candCtx = context.WithValue(candCtx, raceRoundKey{}, round)
	candCtx = transport.WithEntryProxy(candCtx, c.Route.GlobalProxyURI)

	l.cancelsMu.Lock()
	l.cancels[c.Key] = candCancel
	l.cancelsMu.Unlock()

	if !c.Reserved {
		l.nodes.IncInFlight(uri)
	}
	atomic.AddInt32(&l.active, 1)
	go func(candidate scheduler.Candidate, candidateCtx context.Context, candidateCancel context.CancelCauseFunc, candidateTimeout *time.Timer) {
		u := candidate.Route.RequestNodeURI
		defer l.nodes.DecInFlight(u)
		result := raceResult[T]{key: candidate.Key, uri: u, route: candidate.Route}
		started := time.Now()
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					result.err = NewInternalError(fmt.Sprintf("节点 %s 候选执行 panic: %v", l.nodes.NodeName(u), recovered))
				}
			}()
			result.val, result.err = l.run(candidateCtx, u)
		}()
		if candidateTimeout != nil {
			candidateTimeout.Stop()
		}
		result.elapsed = time.Since(started)
		if errors.Is(context.Cause(candidateCtx), context.DeadlineExceeded) {
			result.err = NewUnavailableError(fmt.Sprintf("节点 %s 竞速超时（%d 秒），已淘汰", l.nodes.NodeName(u), l.raceTimeout))
		} else if result.err == nil && candidateCtx.Err() != nil {
			result.err = candidateCtx.Err()
		}
		if result.err != nil || !l.keepSuccess {
			candidateCancel(context.Canceled)
		}
		resCh <- result
	}(c, candCtx, cancelCause, timeout)
}

// resultRecorder handles outcome recording against sticky pools and node health.
type resultRecorder[T any] struct {
	stickyPool   *nodes.StickyNodePool
	metadata     scheduler.ExecutionMetadata
	dependencies raceDependencies
}

func newResultRecorder[T any](stickyPool *nodes.StickyNodePool, metadata scheduler.ExecutionMetadata, dependencies raceDependencies) *resultRecorder[T] {
	return &resultRecorder[T]{stickyPool: stickyPool, metadata: metadata, dependencies: dependencies}
}
func (r *resultRecorder[T]) Record(res raceResult[T]) {
	if res.err == nil {
		r.dependencies.planner.RecordRoute(res.route, r.metadata, true, "", "", res.elapsed)
		if res.route.GlobalProxyURI != "" {
			_ = r.dependencies.planner.MarkGlobalProxySuccess(res.route.GlobalProxyURI)
		}
		r.dependencies.nodes.RecordResult(res.uri, true, res.elapsed.Seconds()*1000, "")
		r.stickyPool.Add(res.uri)
		return
	}
	if errors.Is(res.err, context.Canceled) {
		return
	}

	ve := asVertexError(res.err)
	errorClass, scope := "unknown", ""
	if ve != nil {
		errorClass = ve.Kind
		scope = string(ve.Scope)
	}
	r.dependencies.planner.RecordRoute(res.route, r.metadata, false, errorClass, scope, res.elapsed)
	if ve != nil && ve.IsGlobalHardError() {
		return
	}
	if ve != nil && (ve.Scope == ScopeGlobalProxy || ve.Scope == ScopeRoute || ve.Scope == ScopeTransient) {
		return
	}
	if ve != nil && ve.Kind == "ratelimit" {
		r.dependencies.nodes.RecordRateLimit(res.uri, 30)
		r.stickyPool.Evict(res.uri)
		return
	}

	r.dependencies.nodes.RecordResult(res.uri, false, 0, res.err.Error())
	r.stickyPool.Evict(res.uri)
}
