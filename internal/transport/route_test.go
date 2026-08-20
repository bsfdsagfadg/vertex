package transport

import (
	"errors"
	"testing"
)

func TestProxyIdentityNormalizesNamesSchemesAndDefaults(t *testing.T) {
	first, err := ProxyIdentity("SOCKS://user:pass@EXAMPLE.com#first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := ProxyIdentity("socks5h://user:pass@example.com:1080#second")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("equivalent proxies differ: first=%+v second=%+v", first, second)
	}
}

func TestPlanRouteRejectsSameIdentityAndEndpoint(t *testing.T) {
	_, err := PlanRoute(
		"vless://first@example.com:443?security=tls#global",
		"vless://first@example.com:443?security=tls#node",
	)
	if !errors.Is(err, ErrDuplicateProxyRoute) {
		t.Fatalf("same identity error=%v", err)
	}

	_, err = PlanRoute(
		"vless://first@example.com:443?security=tls",
		"trojan://different@example.com:443?security=tls",
	)
	if !errors.Is(err, ErrDuplicateProxyRoute) {
		t.Fatalf("same endpoint error=%v", err)
	}
}

func TestPlanRouteAllowsDistinctHops(t *testing.T) {
	route, err := PlanRoute(
		"socks5://global.example:1080",
		"vless://node@request.example:443?security=tls",
	)
	if err != nil {
		t.Fatal(err)
	}
	if route.GlobalProxyURI == "" || route.RequestNodeURI == "" {
		t.Fatalf("route was not preserved: %+v", route)
	}
}
