package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/repository"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

// RoutePlanner owns the per-process GlobalProxy cursor and health tracker.
// It reads candidates from the injected repository so control-plane changes
// become visible without a package-level repository singleton.
type RoutePlanner struct {
	repository *repository.SQLite
	health     *HealthTracker
	cursor     atomic.Uint64
	mu         sync.Mutex
}

func NewRoutePlanner(store *repository.SQLite, health *HealthTracker) (*RoutePlanner, error) {
	if store == nil {
		return nil, errors.New("route planner repository is nil")
	}
	if health == nil {
		health = NewHealthTracker()
	}
	return &RoutePlanner{repository: store, health: health}, nil
}

func (p *RoutePlanner) PlanCandidates(cfg config.ConfigProvider, requestNodes []nodes.Node, metadata ExecutionMetadata) ([]Candidate, error) {
	if cfg == nil {
		return nil, errors.New("route planner config is nil")
	}
	if len(requestNodes) == 0 {
		return nil, errors.New("route planner has no request nodes")
	}
	globalEnabled := cfg.GlobalProxyEnabled()
	globalRequired := globalEnabled && (cfg.GlobalProxyRequired() || !cfg.AllowDirectWithoutGlobalProxy())
	globalURIs := []string(nil)
	if globalEnabled {
		var err error
		globalURIs, err = p.globalProxySequence(context.Background(), 0, cfg)
		if err != nil {
			return nil, fmt.Errorf("load global proxy candidates: %w", err)
		}
		if len(globalURIs) == 0 && globalRequired {
			return nil, ErrNoGlobalProxyRoute
		}
	}

	planned := make([]Candidate, 0, len(requestNodes))
	for nodeIndex, node := range requestNodes {
		node.RawURI = strings.TrimSpace(node.RawURI)
		var selected transport.Route
		found := false
		seen := make(map[string]struct{}, len(globalURIs))
		for offset := range globalURIs {
			globalURI := strings.TrimSpace(globalURIs[(nodeIndex+offset)%len(globalURIs)])
			if globalURI == "" {
				continue
			}
			if _, duplicate := seen[globalURI]; duplicate {
				continue
			}
			seen[globalURI] = struct{}{}
			route, err := transport.PlanRoute(globalURI, node.RawURI)
			if err != nil {
				if errors.Is(err, transport.ErrDuplicateProxyRoute) {
					continue
				}
				return nil, fmt.Errorf("plan route for request node: %w", err)
			}
			selected, found = route, route.RequestNodeURI != ""
			if found {
				break
			}
		}
		if !found && (!globalEnabled || !globalRequired) {
			route, err := transport.PlanRoute("", node.RawURI)
			if err != nil {
				return nil, err
			}
			selected, found = route, true
		}
		if found {
			planned = append(planned, Candidate{Node: node, Route: selected, Key: routeKey(selected)})
		}
	}
	if len(planned) == 0 {
		return nil, ErrNoDistinctProxyRoute
	}
	type scored struct {
		candidate Candidate
		score     float64
	}
	scores := make([]scored, 0, len(planned))
	for _, candidate := range planned {
		score, admitted := p.health.CandidateScore(candidate.Route, metadata)
		if admitted {
			scores = append(scores, scored{candidate: candidate, score: score})
		}
	}
	if len(scores) == 0 {
		return nil, ErrNoDistinctProxyRoute
	}
	sort.SliceStable(scores, func(i, j int) bool { return scores[i].score > scores[j].score })
	result := make([]Candidate, 0, len(scores))
	for _, value := range scores {
		result = append(result, value.candidate)
	}
	return result, nil
}

func (p *RoutePlanner) RecordRoute(route transport.Route, metadata ExecutionMetadata, success bool, errorClass, scope string, elapsed time.Duration) {
	p.health.RecordRoute(route, metadata, success, errorClass, scope, elapsed)
}

func (p *RoutePlanner) MarkGlobalProxySuccess(rawURI string) error {
	if strings.TrimSpace(rawURI) == "" {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	records, err := p.repository.ListGlobalProxies(context.Background())
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.RawURI != rawURI {
			continue
		}
		return p.repository.UpdateGlobalProxyHealth(context.Background(), repository.GlobalProxyHealth{
			GlobalProxyID: record.ID, LastTestOK: true, LastTestMS: record.LastTestMS,
			LastTestAt: time.Now().Unix(), LastTestError: "", ConsecutiveFailures: 0,
		}, record.Disabled)
	}
	return nil
}

func (p *RoutePlanner) MarkGlobalProxyFailure(rawURI, message string, cooldownSeconds int) error {
	if strings.TrimSpace(rawURI) == "" {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	records, err := p.repository.ListGlobalProxies(context.Background())
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.RawURI != rawURI {
			continue
		}
		if cooldownSeconds < 0 {
			cooldownSeconds = 0
		}
		now := time.Now()
		return p.repository.UpdateGlobalProxyHealth(context.Background(), repository.GlobalProxyHealth{
			GlobalProxyID: record.ID, CooldownUntil: now.Add(time.Duration(cooldownSeconds) * time.Second).Unix(),
			LastTestOK: false, LastTestMS: 0, LastTestAt: now.Unix(), LastTestError: message,
			ConsecutiveFailures: record.ConsecutiveFailures,
		}, record.Disabled)
	}
	return nil
}

func (p *RoutePlanner) globalProxySequence(ctx context.Context, count int, cfg config.ConfigProvider) ([]string, error) {
	records, err := p.repository.ListGlobalProxies(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	eligible := make([]repository.GlobalProxyRecord, 0, len(records))
	for _, record := range records {
		if strings.TrimSpace(record.RawURI) == "" || record.Disabled || record.CooldownUntil > now {
			continue
		}
		eligible = append(eligible, record)
	}
	if strings.EqualFold(strings.TrimSpace(cfg.GlobalProxySelection()), "health") {
		sort.SliceStable(eligible, func(i, j int) bool {
			left, right := eligible[i], eligible[j]
			if left.Pinned != right.Pinned {
				return left.Pinned
			}
			if left.LastTestOK != right.LastTestOK {
				return left.LastTestOK
			}
			if left.ConsecutiveFailures != right.ConsecutiveFailures {
				return left.ConsecutiveFailures < right.ConsecutiveFailures
			}
			leftLatency, rightLatency := left.LastTestMS, right.LastTestMS
			if leftLatency <= 0 {
				leftLatency = 1e18
			}
			if rightLatency <= 0 {
				rightLatency = 1e18
			}
			return leftLatency < rightLatency
		})
	}
	if len(eligible) == 0 {
		return nil, nil
	}
	if count <= 0 || count > len(eligible) {
		count = len(eligible)
	}
	start := p.cursor.Add(1) - 1
	sequence := make([]string, count)
	for index := range sequence {
		sequence[index] = eligible[(start+uint64(index))%uint64(len(eligible))].RawURI
	}
	return sequence, nil
}
