package vertex

import (
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
)

// hedgeScheduler manages timer-based and failure-based candidate progression.
type hedgeScheduler struct {
	delay   time.Duration
	timer   *time.Timer
	total   int
	nextIdx int
}

func newHedgeScheduler(cfg config.ConfigProvider, candidates []nodes.Node) *hedgeScheduler {
	delay := time.Duration(cfg.ParallelPoolDelayMs()) * time.Millisecond
	if cfg.ParallelPoolDelayDynamic() {
		delay = time.Duration(nodes.GetAverageLatency()) * time.Millisecond
	}
	if delay < 0 {
		delay = 0
	}

	timer := time.NewTimer(delay)
	return &hedgeScheduler{
		delay:   delay,
		timer:   timer,
		total:   len(candidates),
		nextIdx: 0,
	}
}

func (s *hedgeScheduler) HasMore() bool {
	return s.nextIdx < s.total
}

func (s *hedgeScheduler) NextIndex() int {
	idx := s.nextIdx
	s.nextIdx++
	return idx
}

func (s *hedgeScheduler) TimerChan() <-chan time.Time {
	return s.timer.C
}

func (s *hedgeScheduler) StopTimer() {
	if s.timer != nil {
		s.timer.Stop()
	}
}

func (s *hedgeScheduler) ResetTimer() {
	if !s.HasMore() {
		return
	}
	if !s.timer.Stop() {
		select {
		case <-s.timer.C:
		default:
		}
	}
	s.timer.Reset(s.delay)
}
