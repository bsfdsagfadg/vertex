package scheduler

import (
	"context"
	"sync"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/transport"
)

type ExecutionMetadata struct {
	RequestID           string
	Operation           string
	EndpointFamily      string
	ModelFamily         string
	ModelProfileVersion string
	PayloadHash         string
	IdempotencyKey      string
}

type executionMetadataContextKey struct{}

func WithExecutionMetadata(ctx context.Context, metadata ExecutionMetadata) context.Context {
	return context.WithValue(ctx, executionMetadataContextKey{}, metadata)
}

func MetadataFromContext(ctx context.Context) ExecutionMetadata {
	metadata, _ := ctx.Value(executionMetadataContextKey{}).(ExecutionMetadata)
	return metadata
}

type HealthKey struct {
	Role           string
	Identity       string
	Operation      string
	EndpointFamily string
	ModelFamily    string
	ErrorClass     string
}

type HealthState struct {
	EWMALatencyMS      float64
	Successes          uint64
	Failures           uint64
	ConsecutiveFailure uint64
	CooldownUntil      time.Time
	CircuitOpenUntil   time.Time
	LastUpdated        time.Time
}

type HealthTracker struct {
	mu     sync.RWMutex
	states map[HealthKey]HealthState
}

func NewHealthTracker() *HealthTracker {
	return &HealthTracker{states: make(map[HealthKey]HealthState)}
}

var DefaultHealth = NewHealthTracker() //nolint:gochecknoglobals

func (h *HealthTracker) RecordRoute(route transport.Route, metadata ExecutionMetadata, success bool, errorClass, scope string, elapsed time.Duration) {
	if h == nil {
		return
	}
	record := func(role, identity string, ok bool) {
		if identity == "" {
			return
		}
		h.record(HealthKey{
			Role: role, Identity: identity, Operation: metadata.Operation,
			EndpointFamily: metadata.EndpointFamily, ModelFamily: metadata.ModelFamily,
		}, ok, elapsed)
		if !ok && errorClass != "" {
			h.record(HealthKey{
				Role: role, Identity: identity, Operation: metadata.Operation,
				EndpointFamily: metadata.EndpointFamily, ModelFamily: metadata.ModelFamily,
				ErrorClass: errorClass,
			}, false, elapsed)
		}
	}

	if success {
		record("global_proxy", route.GlobalProxyIdentity.SemanticFingerprint, true)
		record("request_node", route.RequestNodeIdentity.SemanticFingerprint, true)
		return
	}
	switch scope {
	case "global_proxy":
		record("global_proxy", route.GlobalProxyIdentity.SemanticFingerprint, false)
	case "request_node":
		record("request_node", route.RequestNodeIdentity.SemanticFingerprint, false)
	case "upstream":
		record("upstream", metadata.EndpointFamily, false)
	case "route":
		// Ambiguous chain failures are tracked independently and must not poison
		// either role-specific health score.
		record("route", routePairIdentity(route), false)
	}
}

func (h *HealthTracker) record(key HealthKey, success bool, elapsed time.Duration) {
	now := time.Now()
	latency := float64(elapsed.Milliseconds())
	h.mu.Lock()
	state := h.states[key]
	if latency > 0 {
		if state.EWMALatencyMS <= 0 {
			state.EWMALatencyMS = latency
		} else {
			state.EWMALatencyMS = 0.8*state.EWMALatencyMS + 0.2*latency
		}
	}
	if success {
		state.Successes++
		state.ConsecutiveFailure = 0
		state.CircuitOpenUntil = time.Time{}
	} else {
		state.Failures++
		state.ConsecutiveFailure++
		if state.ConsecutiveFailure >= 5 {
			state.CircuitOpenUntil = now.Add(30 * time.Second)
		}
	}
	state.LastUpdated = now
	h.states[key] = state
	h.mu.Unlock()
}

func (h *HealthTracker) Snapshot() map[HealthKey]HealthState {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make(map[HealthKey]HealthState, len(h.states))
	for key, state := range h.states {
		result[key] = state
	}
	return result
}

func (h *HealthTracker) CandidateScore(route transport.Route, metadata ExecutionMetadata) (score float64, admitted bool) {
	if h == nil {
		return 0, true
	}
	keys := []HealthKey{
		{Role: "request_node", Identity: route.RequestNodeIdentity.SemanticFingerprint, Operation: metadata.Operation, EndpointFamily: metadata.EndpointFamily, ModelFamily: metadata.ModelFamily},
		{Role: "global_proxy", Identity: route.GlobalProxyIdentity.SemanticFingerprint, Operation: metadata.Operation, EndpointFamily: metadata.EndpointFamily, ModelFamily: metadata.ModelFamily},
	}
	now := time.Now()
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, key := range keys {
		if key.Identity == "" {
			continue
		}
		state := h.states[key]
		if now.Before(state.CircuitOpenUntil) || now.Before(state.CooldownUntil) {
			return 0, false
		}
		total := state.Successes + state.Failures
		if total > 0 {
			score += float64(state.Successes) / float64(total) * 1000
		}
		score -= state.EWMALatencyMS
		score -= float64(state.ConsecutiveFailure) * 250
	}
	return score, true
}

func routePairIdentity(route transport.Route) string {
	return route.GlobalProxyIdentity.SemanticFingerprint + ":" + route.RequestNodeIdentity.SemanticFingerprint
}
