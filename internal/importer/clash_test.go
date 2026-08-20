package importer

import (
	"strings"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/transport"
)

func TestParseClashYAMLToNodesPreservesSSPluginOpts(t *testing.T) {
	yamlText := `
proxies:
  - { name: 'HK Demo', type: ss, server: example.com, port: 12022, cipher: aes-128-gcm, password: secret, plugin: obfs, plugin-opts: { mode: http, host: edge.example.com }, udp: true }
`

	imported := ParseClashYAMLToNodes(yamlText)
	if len(imported) != 1 {
		t.Fatalf("expected 1 node, got %d", len(imported))
	}
	if imported[0].Type != "ss" || imported[0].Name != "HK Demo" {
		t.Fatalf("unexpected imported node metadata: %#v", imported[0])
	}

	out, err := transport.ParseURI(imported[0].RawURI)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if got := out.Plugin; got != "obfs" {
		t.Fatalf("plugin not preserved: %#v", got)
	}
	if out.PluginOptions == "" {
		t.Fatalf("plugin-opts missing: %#v", out.PluginOptions)
	}
}

func TestParseClashYAMLToNodesSkipsInvalidProxyObjects(t *testing.T) {
	yamlText := `
proxies:
  - { name: bad missing endpoint, type: ss }
  - { name: group-ish, type: select }
`

	imported := ParseClashYAMLToNodes(yamlText)
	if len(imported) != 0 {
		t.Fatalf("expected invalid proxy objects to be skipped, got %#v", imported)
	}
}

func TestParseImportedNodesSupportsSingleTopLevelProxyObject(t *testing.T) {
	text := `{ name: 'HK Demo', type: ss, server: example.com, port: 12022, cipher: aes-128-gcm, password: secret, plugin: obfs, plugin-opts: { mode: http, host: edge.example.com } }`

	imported := ParseImportedNodes(text)
	if len(imported) != 1 {
		t.Fatalf("expected 1 node, got %d", len(imported))
	}

	out, err := transport.ParseURI(imported[0].RawURI)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if out.Type != "ss" || out.Server != "example.com" {
		t.Fatalf("unexpected imported node: %#v", out)
	}
}

func TestParseClashYAMLToNodesSkipsUnsupportedTypes(t *testing.T) {
	yamlText := `
proxies:
  - { name: wg-a, type: wireguard, server: 1.2.3.4, port: 51820, private-key: abc, public-key: def }
  - { name: snell-a, type: snell, server: 5.6.7.8, port: 443, psk: xyz }
  - { name: valid-ss, type: ss, server: example.com, port: 8388, cipher: aes-128-gcm, password: secret }
`
	imported := ParseClashYAMLToNodes(yamlText)
	if len(imported) != 1 {
		t.Fatalf("expected only the supported ss node to be imported, got %d: %#v", len(imported), imported)
	}
	if imported[0].Type != "ss" || imported[0].Name != "valid-ss" {
		t.Fatalf("unexpected imported node: %#v", imported[0])
	}
	if !strings.HasPrefix(imported[0].RawURI, "ss://") {
		t.Fatalf("expected standard ss URI, got %q", imported[0].RawURI)
	}
}

func TestParseClashYAMLToNodesVlessWSTLSAndHy2Ports(t *testing.T) {
	yamlText := `
proxies:
  - { name: vless-ws, type: vless, server: cf.example.com, port: 443, uuid: 12345678-1234-1234-1234-123456789012, tls: true, servername: edge.example.com, client-fingerprint: chrome, network: ws, ws-opts: { path: /ws, headers: { Host: edge.example.com } } }
  - { name: hy2-p, type: hysteria2, server: 203.10.99.51, port: 20000, password: secret, sni: www.bing.com, skip-cert-verify: true, ports: 20000-55000 }
`
	imported := ParseClashYAMLToNodes(yamlText)
	if len(imported) != 2 {
		t.Fatalf("expected 2 nodes, got %d: %#v", len(imported), imported)
	}

	vless, err := transport.ParseURI(imported[0].RawURI)
	if err != nil {
		t.Fatalf("ParseURI vless returned error: %v", err)
	}
	if vless.Type != "vless" || vless.TLS == nil || vless.TLS.ServerName != "edge.example.com" {
		t.Fatalf("unexpected vless IR: %#v", vless)
	}
	if vless.Transport == nil || vless.Transport.Type != "ws" || vless.Transport.Path != "/ws" {
		t.Fatalf("ws transport missing: %#v", vless.Transport)
	}
	if !vless.Supported {
		t.Fatalf("vless should be supported: %s", vless.UnsupportedReason)
	}

	hy2, err := transport.ParseURI(imported[1].RawURI)
	if err != nil {
		t.Fatalf("ParseURI hy2 returned error: %v", err)
	}
	if hy2.Type != "hysteria2" || len(hy2.ServerPorts) == 0 || hy2.ServerPorts[0] != "20000:55000" {
		t.Fatalf("hy2 ports not preserved: %#v", hy2)
	}
	if !hy2.Supported {
		t.Fatalf("hy2 should be supported: %s", hy2.UnsupportedReason)
	}
}
