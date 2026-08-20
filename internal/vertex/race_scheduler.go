package vertex

import (
	"log"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
)

// CandidateScheduler manages candidate node selection, round progression,
// and timer-based hedge dispatch across race candidates.
type CandidateScheduler struct {
	cfg         config.ConfigProvider
	usedURIs    map[string]bool
	roundBudget int
	round       int

	candidates []nodes.Node
	nextIdx    int
	delay      time.Duration
	timer      *time.Timer
}

// NewCandidateScheduler initializes a candidate scheduler with config.
func NewCandidateScheduler(cfg config.ConfigProvider) *CandidateScheduler {
	roundBudget := 0
	if cfg.ParallelPoolEnabled() && !cfg.ParallelPoolRetryEnabled() {
		roundBudget = cfg.MaxRetries()
	}
	return &CandidateScheduler{
		cfg:         cfg,
		usedURIs:    make(map[string]bool),
		roundBudget: roundBudget,
		round:       0,
	}
}

// SelectInitialCandidates determines the initial batch of candidates.
// Returns candidates and true if parallel racing should run, or nil and false if single-node fallback.
func (s *CandidateScheduler) SelectInitialCandidates() ([]nodes.Node, bool) {
	if !s.cfg.ParallelPoolEnabled() {
		return nil, false
	}
	cands := s.selectFreshCandidates()
	if len(cands) == 0 {
		return nil, false
	}
	return cands, true
}

// FallbackProxy returns the active node or proxy URL when parallel racing is disabled or has no candidates.
func (s *CandidateScheduler) FallbackProxy() string {
	proxy := s.cfg.ActiveNodeURI()
	if proxy == "" {
		proxy = s.cfg.ProxyURL()
	}
	return proxy
}

func (s *CandidateScheduler) selectFreshCandidates() []nodes.Node {
	cands := nodes.SelectForParallel(s.cfg.ParallelPoolSize(), s.cfg.ParallelNodeTopK(), s.cfg.DebugMode(), s.cfg.StickyNodePriority())
	fresh := make([]nodes.Node, 0, len(cands))
	for _, c := range cands {
		if !s.usedURIs[c.RawURI] {
			fresh = append(fresh, c)
		}
	}
	return fresh
}

// PrepareRound sets up a new candidate round and initializes hedge timing.
func (s *CandidateScheduler) PrepareRound(candidates []nodes.Node) {
	s.StopTimer()
	s.candidates = candidates
	s.nextIdx = 0

	delay := time.Duration(s.cfg.ParallelPoolDelayMs()) * time.Millisecond
	if s.cfg.ParallelPoolDelayDynamic() {
		delay = time.Duration(nodes.GetAverageLatency()) * time.Millisecond
	}
	if delay < 0 {
		delay = 0
	}
	s.delay = delay
	s.timer = time.NewTimer(delay)
}

// CurrentRound returns the current round index (0-based).
func (s *CandidateScheduler) CurrentRound() int {
	return s.round
}

// RoundBudget returns remaining round swap budget.
func (s *CandidateScheduler) RoundBudget() int {
	return s.roundBudget
}

// HasMore returns true if there are remaining candidates to launch in the current round.
func (s *CandidateScheduler) HasMore() bool {
	return s.nextIdx < len(s.candidates)
}

// NextCandidate returns the next candidate in the current round, marking it as used.
func (s *CandidateScheduler) NextCandidate() (nodes.Node, bool) {
	if !s.HasMore() {
		return nodes.Node{}, false
	}
	cand := s.candidates[s.nextIdx]
	s.nextIdx++
	s.usedURIs[cand.RawURI] = true
	return cand, true
}

// PeekNextCandidate returns the next candidate without advancing nextIdx (for logging).
func (s *CandidateScheduler) PeekNextCandidate() (nodes.Node, bool) {
	if !s.HasMore() {
		return nodes.Node{}, false
	}
	return s.candidates[s.nextIdx], true
}

// TimerChan returns the channel for the hedge timer.
func (s *CandidateScheduler) TimerChan() <-chan time.Time {
	if s.timer == nil {
		return nil
	}
	return s.timer.C
}

// StopTimer stops the hedge timer cleanly and drains it.
func (s *CandidateScheduler) StopTimer() {
	if s.timer != nil {
		if !s.timer.Stop() {
			select {
			case <-s.timer.C:
			default:
			}
		}
		s.timer = nil
	}
}

// ResetTimer restarts the hedge timer for the next candidate.
func (s *CandidateScheduler) ResetTimer() {
	if !s.HasMore() {
		s.StopTimer()
		return
	}
	if s.timer == nil {
		s.timer = time.NewTimer(s.delay)
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

// NextRound attempts to select a fresh batch of candidates for the next round.
// Returns the next candidates and true if a round is available, or nil and false if budget is exhausted.
func (s *CandidateScheduler) NextRound() ([]nodes.Node, bool) {
	if s.roundBudget <= 0 {
		return nil, false
	}
	next := s.selectFreshCandidates()
	if len(next) == 0 {
		if s.cfg.DebugMode() {
			log.Printf("[Racing] 新鲜节点已耗尽，清空防重过滤，允许节点跨轮次重试复用...")
		}
		s.usedURIs = make(map[string]bool)
		next = s.selectFreshCandidates()
	}
	if len(next) == 0 {
		return nil, false
	}
	s.roundBudget--
	s.round++
	return next, true
}
