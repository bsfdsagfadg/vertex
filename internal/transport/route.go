package transport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
)

// ErrDuplicateProxyRoute is returned when both hops resolve to the same proxy
// identity or physical endpoint.
var ErrDuplicateProxyRoute = errors.New("global proxy and request node must be distinct")

// Route is the immutable network tuple used by one upstream candidate.
type Route struct {
	GlobalProxyURI      string
	RequestNodeURI      string
	GlobalProxyIdentity CanonicalProxyIdentity
	RequestNodeIdentity CanonicalProxyIdentity
}

// CanonicalProxyIdentity separates a complete semantic identity from its
// physical endpoint fingerprint. Both are hashes so credentials never leak to
// logs, errors, or repository keys.
type CanonicalProxyIdentity struct {
	SemanticFingerprint string
	EndpointFingerprint string
}

// PlanRoute validates and freezes a two-hop route.
func PlanRoute(globalProxyURI, requestNodeURI string) (Route, error) {
	route := Route{
		GlobalProxyURI: strings.TrimSpace(globalProxyURI),
		RequestNodeURI: strings.TrimSpace(requestNodeURI),
	}
	var err error
	if route.GlobalProxyURI != "" {
		route.GlobalProxyIdentity, err = ProxyIdentity(route.GlobalProxyURI)
		if err != nil {
			return Route{}, fmt.Errorf("global proxy identity: %w", err)
		}
	}
	if route.RequestNodeURI != "" {
		route.RequestNodeIdentity, err = ProxyIdentity(route.RequestNodeURI)
		if err != nil {
			return Route{}, fmt.Errorf("request node identity: %w", err)
		}
	}
	if route.GlobalProxyURI != "" && route.RequestNodeURI != "" &&
		(route.GlobalProxyIdentity.SemanticFingerprint == route.RequestNodeIdentity.SemanticFingerprint ||
			(route.GlobalProxyIdentity.EndpointFingerprint != "" && route.GlobalProxyIdentity.EndpointFingerprint == route.RequestNodeIdentity.EndpointFingerprint)) {
		return Route{}, ErrDuplicateProxyRoute
	}
	return route, nil
}

// ProxyIdentity derives a stable semantic and endpoint identity from the
// protocol parser's normalized proxy map.
func ProxyIdentity(rawURI string) (CanonicalProxyIdentity, error) {
	rawURI = normalizeScheme(strings.TrimSpace(rawURI))
	if rawURI == "" {
		return CanonicalProxyIdentity{}, fmt.Errorf("empty proxy URI")
	}
	mapping, err := ParseURI(rawURI)
	if err != nil {
		return CanonicalProxyIdentity{}, err
	}
	if len(mapping) == 0 {
		return CanonicalProxyIdentity{}, fmt.Errorf("proxy URI produced an empty identity")
	}
	delete(mapping, "name")
	server := strings.ToLower(strings.TrimSpace(fmt.Sprint(mapping["server"])))
	server = strings.Trim(server, "[]")
	if ip := net.ParseIP(server); ip != nil {
		server = ip.String()
	}
	port := strings.TrimSpace(fmt.Sprint(mapping["port"]))
	if server == "" || server == "<nil>" || port == "" || port == "<nil>" {
		return CanonicalProxyIdentity{}, fmt.Errorf("proxy URI is missing server or port")
	}
	mapping["server"] = server

	semantic, err := json.Marshal(mapping)
	if err != nil {
		return CanonicalProxyIdentity{}, fmt.Errorf("marshal proxy identity: %w", err)
	}
	endpoint := net.JoinHostPort(server, port)
	return CanonicalProxyIdentity{
		SemanticFingerprint: fingerprint(semantic),
		EndpointFingerprint: fingerprint([]byte(endpoint)),
	}, nil
}

func normalizeScheme(rawURI string) string {
	separator := strings.Index(rawURI, "://")
	if separator <= 0 {
		return rawURI
	}
	return strings.ToLower(rawURI[:separator]) + rawURI[separator:]
}

func fingerprint(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
