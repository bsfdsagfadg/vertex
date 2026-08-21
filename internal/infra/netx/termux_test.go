package netx

import "testing"

func TestParseResolvConfNameserversPrefersUsableServers(t *testing.T) {
	content := `
# comment
nameserver 127.0.0.1
nameserver ::1
nameserver 8.8.8.8
nameserver 1.1.1.1
nameserver 8.8.8.8
`

	got := parseResolvConfNameservers(content)
	if len(got) != 2 {
		t.Fatalf("expected 2 usable nameservers, got %d: %#v", len(got), got)
	}
	if got[0] != "8.8.8.8:53" {
		t.Fatalf("unexpected first nameserver: %q", got[0])
	}
	if got[1] != "1.1.1.1:53" {
		t.Fatalf("unexpected second nameserver: %q", got[1])
	}
}

func TestNormalizeNameserverAddrSupportsIPv6AndRejectsLoopback(t *testing.T) {
	if got := normalizeNameserverAddr("2001:4860:4860::8888"); got != "[2001:4860:4860::8888]:53" {
		t.Fatalf("unexpected IPv6 address: %q", got)
	}
	if got := normalizeNameserverAddr("[2606:4700:4700::1111]:5353"); got != "[2606:4700:4700::1111]:5353" {
		t.Fatalf("unexpected IPv6 host:port: %q", got)
	}
	if got := normalizeNameserverAddr("127.0.0.1:53"); got != "" {
		t.Fatalf("loopback should be rejected, got %q", got)
	}
}
