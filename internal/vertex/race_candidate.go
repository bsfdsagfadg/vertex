package vertex

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/nodes"
)

// CandidateLauncher manages launching, lifecycle tracking, and cancellation of race candidates.
type CandidateLauncher[T any] struct {
	run         func(ctx context.Context, proxyURI string) (T, error)
	raceTimeout int
	parentCtx   context.Context
	cancels     map[string]context.CancelFunc
	cancelsMu   sync.Mutex
	active      int32
}

// NewCandidateLauncher constructs a new CandidateLauncher.
func NewCandidateLauncher[T any](parentCtx context.Context, raceTimeout int, run func(ctx context.Context, proxyURI string) (T, error)) *CandidateLauncher[T] {
	return &CandidateLauncher[T]{
		run:         run,
		raceTimeout: raceTimeout,
		parentCtx:   parentCtx,
		cancels:     make(map[string]context.CancelFunc),
		active:      0,
	}
}

// CancelCandidate cancels the context of a single candidate by URI.
func (l *CandidateLauncher[T]) CancelCandidate(uri string) {
	l.cancelsMu.Lock()
	cancelFn := l.cancels[uri]
	l.cancelsMu.Unlock()
	if cancelFn != nil {
		cancelFn()
	}
}

// CancelAllExcept cancels all candidates except the specified winning candidate.
func (l *CandidateLauncher[T]) CancelAllExcept(winnerURI string) {
	l.cancelsMu.Lock()
	for u, cancelFn := range l.cancels {
		if u != winnerURI && cancelFn != nil {
			cancelFn()
		}
	}
	l.cancelsMu.Unlock()
}

// CancelAll cancels all launched candidates.
func (l *CandidateLauncher[T]) CancelAll() {
	l.cancelsMu.Lock()
	for _, cancelFn := range l.cancels {
		if cancelFn != nil {
			cancelFn()
		}
	}
	l.cancelsMu.Unlock()
}

// ActiveCount returns the number of currently in-flight candidate goroutines.
func (l *CandidateLauncher[T]) ActiveCount() int32 {
	return atomic.LoadInt32(&l.active)
}

// DecrementActive decrements the count of active candidate goroutines.
func (l *CandidateLauncher[T]) DecrementActive() int32 {
	return atomic.AddInt32(&l.active, -1)
}
func (l *CandidateLauncher[T]) Launch(c nodes.Node, round int, resCh chan<- raceResult[T]) {
	uri := c.RawURI

	candCtx, candCancel := context.WithCancel(l.parentCtx)
	candCtx = context.WithValue(candCtx, raceRoundKey{}, round)

	l.cancelsMu.Lock()
	l.cancels[uri] = candCancel
	l.cancelsMu.Unlock()

	atomic.AddInt32(&l.active, 1)
	go func(u string, candidateCtx context.Context, candidateCancel context.CancelFunc) {
		nodes.IncInFlight(u)
		defer nodes.DecInFlight(u)
		resultReady := make(chan raceResult[T], 1)
		go func() {
			result := raceResult[T]{uri: u}
			func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						result.err = NewInternalError(fmt.Sprintf("节点 %s 候选执行 panic: %v", nodes.GetNodeName(u), recovered))
					}
				}()
				result.val, result.err = l.run(candidateCtx, u)
			}()
			resultReady <- result
		}()

		if l.raceTimeout <= 0 {
			select {
			case result := <-resultReady:
				resCh <- result
			case <-candidateCtx.Done():
				select {
				case result := <-resultReady:
					resCh <- result
				default:
					resCh <- raceResult[T]{uri: u, err: candidateCtx.Err()}
				}
			}
			return
		}

		timer := time.NewTimer(time.Duration(l.raceTimeout) * time.Second)
		defer func() {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}()

		select {
		case result := <-resultReady:
			resCh <- result
		case <-timer.C:
			candidateCancel()
			resCh <- raceResult[T]{
				uri: u,
				err: NewUnavailableError(fmt.Sprintf("节点 %s 竞速超时（%d 秒），已淘汰", nodes.GetNodeName(u), l.raceTimeout)),
			}
		case <-candidateCtx.Done():
			select {
			case result := <-resultReady:
				resCh <- result
			default:
				resCh <- raceResult[T]{uri: u, err: candidateCtx.Err()}
			}
		}
	}(uri, candCtx, candCancel)
}

// ResultRecorder handles outcome recording against sticky pools and node health.
type ResultRecorder[T any] struct {
	stickyPool *nodes.StickyNodePool
}

// NewResultRecorder constructs a ResultRecorder.
func NewResultRecorder[T any](stickyPool *nodes.StickyNodePool) *ResultRecorder[T] {
	return &ResultRecorder[T]{stickyPool: stickyPool}
}

// Record records candidate outcome into sticky pool and health stats.
func (r *ResultRecorder[T]) Record(res raceResult[T]) {
	if r == nil {
		return
	}
	if res.err == nil {
		if r.stickyPool != nil {
			r.stickyPool.Add(res.uri)
		}
		return
	}
	if errors.Is(res.err, context.Canceled) {
		return
	}

	ve := asVertexError(res.err)
	if ve != nil && ve.IsGlobalHardError() {
		return
	}
	if ve != nil && ve.Kind == "ratelimit" {
		nodes.RecordRateLimit(res.uri, 30)
		if r.stickyPool != nil {
			r.stickyPool.Evict(res.uri)
		}
		return
	}

	nodes.RecordTest(res.uri, false, 0, res.err.Error())
	if r.stickyPool != nil {
		r.stickyPool.Evict(res.uri)
	}
}
