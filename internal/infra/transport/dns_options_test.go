package transport

import (
	"testing"

	"github.com/sagernet/sing-box/option"
)

func TestDNSOptionsForNode(t *testing.T) {
	const uuid = "12345678-1234-1234-1234-123456789012"
	cases := []struct {
		name         string
		uri          string
		wantNil      bool
		wantLen      int
		wantServer   string
		wantPort     uint16
		wantPath     string
		wantFinal    string
		wantStrategy bool
	}{
		{
			name:       "ECH节点带DoH",
			uri:        "vless://" + uuid + "@example.com:443?security=tls&ech=cloudflare-ech.com%2Bhttps%3A%2F%2Fdns.alidns.com%2Fdns-query",
			wantLen:    2,
			wantServer: "dns.alidns.com",
			wantPort:   443,
			wantPath:   "/dns-query",
			wantFinal:  "doh",
		},
		{
			name:       "ECH节点无DoH",
			uri:        "vless://" + uuid + "@example.com:443?security=tls&ech=cloudflare-ech.com",
			wantLen:    2,
			wantServer: "dns.alidns.com",
			wantPort:   443,
			wantPath:   "/dns-query",
			wantFinal:  "doh",
		},
		{
			name:       "TLS非ECH节点",
			uri:        "vless://" + uuid + "@example.com:443?security=tls",
			wantLen:    2,
			wantServer: "dns.alidns.com",
			wantPort:   443,
			wantPath:   "/dns-query",
			wantFinal:  "doh",
		},
		{
			name:    "无TLS节点",
			uri:     "ss://YWVzLTEyOC1nY206cGFzc3dvcmQ@example.com:443",
			wantNil: true,
		},
		{
			name:    "非法DoH非https",
			uri:     "vless://" + uuid + "@example.com:443?security=tls&ech=cloudflare-ech.com%2Bftp%3A%2F%2Fx",
			wantNil: true,
		},
		{
			name:       "DoH非标准端口路径",
			uri:        "vless://" + uuid + "@example.com:443?security=tls&ech=cloudflare-ech.com%2Bhttps%3A%2F%2Fdns.alidns.com%3A8443%2Fcustom",
			wantLen:    2,
			wantServer: "dns.alidns.com",
			wantPort:   8443,
			wantPath:   "/custom",
			wantFinal:  "doh",
		},
		{
			name:       "DoH带query透传",
			uri:        "vless://" + uuid + "@example.com:443?security=tls&ech=cloudflare-ech.com%2Bhttps%3A%2F%2Fdns.example.com%2Fdns-query%3Ftoken%3Dxyz%26v%3D2",
			wantLen:    2,
			wantServer: "dns.example.com",
			wantPort:   443,
			wantPath:   "/dns-query?token=xyz&v=2",
			wantFinal:  "doh",
		},
		{
			name:    "IP形式DoH",
			uri:     "vless://" + uuid + "@example.com:443?security=tls&ech=cloudflare-ech.com%2Bhttps%3A%2F%2F223.5.5.5%2Fdns-query",
			wantNil: true,
		},
		{
			name:    "nil节点",
			uri:     "",
			wantNil: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var n *ParsedNode
			if tc.uri != "" {
				var err error
				n, err = NewIRCache().GetOrParse(tc.uri)
				if err != nil {
					t.Fatalf("GetOrParse returned error: %v", err)
				}
			}
			opts := dnsOptionsForNode(n)
			if tc.wantNil {
				if opts != nil {
					t.Fatalf("expected nil, got %#v", opts)
				}
				return
			}
			if opts == nil {
				t.Fatal("expected non-nil DNS options")
			}
			servers := opts.RawDNSOptions.Servers
			if len(servers) != tc.wantLen {
				t.Fatalf("Servers len = %d, want %d", len(servers), tc.wantLen)
			}
			if servers[0].Type != "udp" || servers[0].Tag != "bootstrap" {
				t.Fatalf("Servers[0] = %s/%s, want udp/bootstrap", servers[0].Type, servers[0].Tag)
			}
			bootstrap, ok := servers[0].Options.(*option.RemoteDNSServerOptions)
			if !ok {
				t.Fatalf("Servers[0].Options type = %T, want *option.RemoteDNSServerOptions", servers[0].Options)
			}
			if bootstrap.Server != "223.5.5.5" || bootstrap.ServerPort != 53 {
				t.Fatalf("bootstrap = %s:%d, want 223.5.5.5:53", bootstrap.Server, bootstrap.ServerPort)
			}
			doh, ok := servers[1].Options.(*option.RemoteHTTPSDNSServerOptions)
			if !ok {
				t.Fatalf("Servers[1].Options type = %T, want *option.RemoteHTTPSDNSServerOptions", servers[1].Options)
			}
			if doh.Server != tc.wantServer {
				t.Fatalf("Server = %q, want %q", doh.Server, tc.wantServer)
			}
			if doh.ServerPort != tc.wantPort {
				t.Fatalf("ServerPort = %d, want %d", doh.ServerPort, tc.wantPort)
			}
			if doh.Path != tc.wantPath {
				t.Fatalf("Path = %q, want %q", doh.Path, tc.wantPath)
			}
			if doh.DomainResolver == nil || doh.DomainResolver.Server != "bootstrap" {
				t.Fatalf("DomainResolver = %#v, want bootstrap", doh.DomainResolver)
			}
			if opts.RawDNSOptions.Final != tc.wantFinal {
				t.Fatalf("Final = %q, want %q", opts.RawDNSOptions.Final, tc.wantFinal)
			}
			if opts.RawDNSOptions.DNSClientOptions.Strategy != 1 {
				t.Fatalf("Strategy = %d, want prefer_ipv4(1)", opts.RawDNSOptions.DNSClientOptions.Strategy)
			}
		})
	}
}
