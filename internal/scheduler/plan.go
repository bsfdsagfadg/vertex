package scheduler

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

var (
	ErrNoGlobalProxyRoute   = errors.New("no_global_proxy_route")
	ErrNoDistinctProxyRoute = errors.New("no_distinct_proxy_route")
)

// Candidate is one immutable, already validated execution route. Key is an
// opaque digest for lifecycle maps and never contains proxy credentials.
type Candidate struct {
	Node     nodes.Node
	Route    transport.Route
	Key      string
	Reserved bool
}

// PlanCandidates pairs each RequestNode snapshot with one eligible
// GlobalProxy. No network I/O or DNS resolution occurs here; transport performs
// the same identity validation again when constructing the route-bound session.
func PlanCandidates(cfg config.ConfigProvider, requestNodes []nodes.Node) ([]Candidate, error) {
	return PlanCandidatesWithMetadata(cfg, requestNodes, ExecutionMetadata{})
}

func PlanCandidatesWithMetadata(cfg config.ConfigProvider, requestNodes []nodes.Node, metadata ExecutionMetadata) ([]Candidate, error) {
	if cfg == nil {
		return nil, errors.New("route planner config is nil")
	}
	if len(requestNodes) == 0 {
		return nil, errors.New("route planner has no request nodes")
	}

	globalEnabled := cfg.GlobalProxyEnabled()
	globalRequired := globalEnabled && (cfg.GlobalProxyRequired() || !cfg.AllowDirectWithoutGlobalProxy())
	globalURIs := []string(nil)
	globalPoolSize := 0
	if globalEnabled {
		globalPoolSize = len(config.ListProxyCandidates())
		if globalPoolSize < 1 {
			globalPoolSize = 1
		}
		globalURIs = config.SelectEntryProxySequence(globalPoolSize, cfg)
		if len(globalURIs) == 0 && globalRequired {
			return nil, ErrNoGlobalProxyRoute
		}
	}

	planned := make([]Candidate, 0, len(requestNodes))
	for nodeIndex, node := range requestNodes {
		node.RawURI = strings.TrimSpace(node.RawURI)
		var route transport.Route
		var routeFound bool
		seenGlobal := make(map[string]struct{}, len(globalURIs))
		for offset := range globalURIs {
			globalURI := globalURIs[(nodeIndex+offset)%len(globalURIs)]
			globalURI = strings.TrimSpace(globalURI)
			if globalURI == "" {
				continue
			}
			if _, duplicate := seenGlobal[globalURI]; duplicate {
				continue
			}
			seenGlobal[globalURI] = struct{}{}
			candidate, err := transport.PlanRoute(globalURI, node.RawURI)
			if err != nil {
				if errors.Is(err, transport.ErrDuplicateProxyRoute) {
					continue
				}
				return nil, fmt.Errorf("plan route for request node: %w", err)
			}
			if candidate.RequestNodeURI == "" {
				continue
			}
			route = candidate
			routeFound = true
			break
		}
		if !routeFound && (!globalEnabled || !globalRequired) {
			candidate, err := transport.PlanRoute("", node.RawURI)
			if err != nil {
				return nil, err
			}
			route = candidate
			routeFound = true
		}
		if !routeFound {
			continue
		}
		planned = append(planned, Candidate{Node: node, Route: route, Key: routeKey(route)})
	}
	if len(planned) == 0 {
		if globalRequired && len(globalURIs) == 0 {
			return nil, ErrNoGlobalProxyRoute
		}
		return nil, ErrNoDistinctProxyRoute
	}
	type scoredCandidate struct {
		candidate Candidate
		score     float64
	}
	scored := make([]scoredCandidate, 0, len(planned))
	for _, candidate := range planned {
		score, admitted := DefaultHealth.CandidateScore(candidate.Route, metadata)
		if admitted {
			scored = append(scored, scoredCandidate{candidate: candidate, score: score})
		}
	}
	if len(scored) == 0 {
		return nil, ErrNoDistinctProxyRoute
	}
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].score > scored[j].score })
	planned = planned[:0]
	for _, item := range scored {
		planned = append(planned, item.candidate)
	}
	return planned, nil
}

func routeKey(route transport.Route) string {
	sum := sha256.Sum256([]byte(route.GlobalProxyURI + "\x00" + route.RequestNodeURI))
	return hex.EncodeToString(sum[:])
}
