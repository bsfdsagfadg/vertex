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

// candidateLauncher manages launching and lifecycle of single node race candidates.
type candidateLauncher[T any] struct {
	run         func(ctx context.Context, proxyURI string) (T, error)
	raceTimeout int
	parentCtx   context.Context
	cancels     map[string]context.CancelFunc
	cancelsMu   sync.Mutex
	active      int32
}

func newCandidateLauncher[T any](parentCtx context.Context, raceTimeout int, run func(ctx context.Context, proxyURI string) (T, error)) *candidateLauncher[T] {
	return &candidateLauncher[T]{
		run:         run,
		raceTimeout: raceTimeout,
		parentCtx:   parentCtx,
		cancels:     make(map[string]context.CancelFunc),
	}
}

func (l *candidateLauncher[T]) CancelCandidate(uri string) {
	l.cancelsMu.Lock()
	cancelFn := l.cancels[uri]
	l.cancelsMu.Unlock()
	if cancelFn != nil {
		cancelFn()
	}
}

func (l *candidateLauncher[T]) CancelAllExcept(winnerURI string) {
	l.cancelsMu.Lock()
	for u, cancelFn := range l.cancels {
		if u != winnerURI {
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

func (l *candidateLauncher[T]) Launch(c nodes.Node, round int, resCh chan<- raceResult[T]) {
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
		defer timer.Stop()
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

// resultRecorder handles outcome recording against sticky pools and node health.
type resultRecorder[T any] struct {
	stickyPool *nodes.StickyNodePool
}

func newResultRecorder[T any](stickyPool *nodes.StickyNodePool) *resultRecorder[T] {
	return &resultRecorder[T]{stickyPool: stickyPool}
}
func (r *resultRecorder[T]) Record(res raceResult[T]) {
	if res.err == nil {
		r.stickyPool.Add(res.uri)
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
		r.stickyPool.Evict(res.uri)
		return
	}

	nodes.RecordTest(res.uri, false, 0, res.err.Error())
	r.stickyPool.Evict(res.uri)
}
