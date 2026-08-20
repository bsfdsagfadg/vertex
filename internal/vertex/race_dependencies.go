package vertex

import (
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/scheduler"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

type raceNodeRuntime interface {
	StickyPool() *nodes.StickyNodePool
	Select(k, topK int, stickyBonus bool, reserve bool) []nodes.Node
	IncInFlight(uri string)
	DecInFlight(uri string)
	NodeName(uri string) string
	AverageLatency() float64
	RecordResult(uri string, success bool, latencyMS float64, message string)
	RecordRateLimit(uri string, cooldownSeconds int)
}

type routePlannerRuntime interface {
	PlanCandidates(cfg config.ConfigProvider, requestNodes []nodes.Node, metadata scheduler.ExecutionMetadata) ([]scheduler.Candidate, error)
	RecordRoute(route transport.Route, metadata scheduler.ExecutionMetadata, success bool, errorClass, scope string, elapsed time.Duration)
	MarkGlobalProxySuccess(rawURI string) error
	MarkGlobalProxyFailure(rawURI, message string, cooldownSeconds int) error
}

type raceDependencies struct {
	nodes   raceNodeRuntime
	planner routePlannerRuntime
}

type legacyNodeRuntime struct{}

func (legacyNodeRuntime) StickyPool() *nodes.StickyNodePool { return nodes.GetStickyPool() }
func (legacyNodeRuntime) Select(k, topK int, sticky bool, reserve bool) []nodes.Node {
	if reserve {
		return nodes.SelectAndReserveForParallel(k, topK, false, sticky)
	}
	return nodes.SelectForParallel(k, topK, false, sticky)
}
func (legacyNodeRuntime) IncInFlight(uri string)     { nodes.IncInFlight(uri) }
func (legacyNodeRuntime) DecInFlight(uri string)     { nodes.DecInFlight(uri) }
func (legacyNodeRuntime) NodeName(uri string) string { return nodes.GetNodeName(uri) }
func (legacyNodeRuntime) AverageLatency() float64    { return nodes.GetAverageLatency() }
func (legacyNodeRuntime) RecordResult(uri string, success bool, latencyMS float64, message string) {
	nodes.RecordTest(uri, success, latencyMS, message)
}
func (legacyNodeRuntime) RecordRateLimit(uri string, cooldownSeconds int) {
	nodes.RecordRateLimit(uri, cooldownSeconds)
}

type legacyRoutePlanner struct{}

func (legacyRoutePlanner) PlanCandidates(cfg config.ConfigProvider, requestNodes []nodes.Node, metadata scheduler.ExecutionMetadata) ([]scheduler.Candidate, error) {
	return scheduler.PlanCandidatesWithMetadata(cfg, requestNodes, metadata)
}
func (legacyRoutePlanner) RecordRoute(route transport.Route, metadata scheduler.ExecutionMetadata, success bool, errorClass, scope string, elapsed time.Duration) {
	scheduler.DefaultHealth.RecordRoute(route, metadata, success, errorClass, scope, elapsed)
}
func (legacyRoutePlanner) MarkGlobalProxySuccess(rawURI string) error {
	return config.MarkEntryProxySuccess(rawURI)
}
func (legacyRoutePlanner) MarkGlobalProxyFailure(rawURI, message string, _ int) error {
	return config.MarkEntryProxyFailure(rawURI, message)
}

func legacyRaceDependencies() raceDependencies {
	return raceDependencies{nodes: legacyNodeRuntime{}, planner: legacyRoutePlanner{}}
}
