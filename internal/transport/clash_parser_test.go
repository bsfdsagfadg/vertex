package transport

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestClashParserInlineYAML(t *testing.T) {
	inline := `{name: "vmess-node", type: vmess, server: "example.com", port: 443, uuid: "12345678-1234-1234-1234-123456789012", cipher: "auto", tls: true, network: ws, ws-opts: {path: "/ws", headers: {Host: "example.com"}}}`
	m, err := ParseClashInline(inline)
	if err != nil {
		t.Fatalf("ParseClashInline failed: %v", err)
	}

	if m["name"] != "vmess-node" {
		t.Fatalf("expected name vmess-node, got %v", m["name"])
	}
	if m["type"] != "vmess" {
		t.Fatalf("expected type vmess, got %v", m["type"])
	}
	if m["server"] != "example.com" {
		t.Fatalf("expected server example.com, got %v", m["server"])
	}
	if m["port"] != 443 {
		t.Fatalf("expected port 443, got %v (%T)", m["port"], m["port"])
	}
	if m["tls"] != true {
		t.Fatalf("expected tls true, got %v", m["tls"])
	}
	wsOpts, ok := m["ws-opts"].(map[string]any)
	if !ok {
		t.Fatalf("missing ws-opts map: %#v", m["ws-opts"])
	}
	if wsOpts["path"] != "/ws" {
		t.Fatalf("expected ws-opts.path /ws, got %v", wsOpts["path"])
	}
	headers, ok := wsOpts["headers"].(map[string]any)
	if !ok || headers["Host"] != "example.com" {
		t.Fatalf("expected ws-opts.headers.Host example.com, got %#v", wsOpts["headers"])
	}
}

func TestClashParserYAMLDocument(t *testing.T) {
	yamlData := `
proxies:
  - name: "ss-node"
    type: ss
    server: 1.2.3.4
    port: 8388
    cipher: aes-128-gcm
    password: secret-password
    plugin: obfs
    plugin-opts:
      mode: http
      host: edge.example.com
  - name: "hy2-node"
    type: hysteria2
    server: 5.6.7.8
    port: 443
    password: hy2-secret
    sni: edge.example.com
`
	proxies, err := ParseClashYAMLProxies([]byte(yamlData))
	if err != nil {
		t.Fatalf("ParseClashYAMLProxies failed: %v", err)
	}
	if len(proxies) != 2 {
		t.Fatalf("expected 2 proxies, got %d", len(proxies))
	}

	p1 := proxies[0]
	if p1["name"] != "ss-node" || p1["type"] != "ss" || p1["port"] != 8388 {
		t.Fatalf("unexpected p1: %#v", p1)
	}
	p1Opts, ok := p1["plugin-opts"].(map[string]any)
	if !ok || p1Opts["mode"] != "http" || p1Opts["host"] != "edge.example.com" {
		t.Fatalf("unexpected p1 plugin-opts: %#v", p1["plugin-opts"])
	}

	p2 := proxies[1]
	if p2["name"] != "hy2-node" || p2["type"] != "hysteria2" || p2["sni"] != "edge.example.com" {
		t.Fatalf("unexpected p2: %#v", p2)
	}
}

func TestClashDriverRoundTrip(t *testing.T) {
	cfg := map[string]any{
		"name":     "clash-test",
		"type":     "trojan",
		"server":   "trojan.example.com",
		"port":     443,
		"password": "secret-pass",
		"sni":      "edge.example.com",
	}

	driver, ok := GetDriver("clash")
	if !ok {
		t.Fatal("clash driver not found")
	}

	uri, err := driver.FormatURI(cfg)
	if err != nil {
		t.Fatalf("FormatURI failed: %v", err)
	}
	if !strings.HasPrefix(uri, "clash://") {
		t.Fatalf("expected clash:// prefix, got %s", uri)
	}

	parsed, err := driver.ParseURI(uri)
	if err != nil {
		t.Fatalf("ParseURI failed: %v", err)
	}
	if parsed["name"] != "clash-test" || parsed["type"] != "trojan" || parsed["password"] != "secret-pass" {
		t.Fatalf("roundtrip mismatch: %#v", parsed)
	}
}

func TestClashDriverJSONAndYAMLSupport(t *testing.T) {
	jsonObj := map[string]any{
		"name":   "json-node",
		"type":   "http",
		"server": "1.2.3.4",
		"port":   8080,
	}
	rawJSON, _ := json.Marshal(jsonObj)
	uriJSON := "clash://" + base64.StdEncoding.EncodeToString(rawJSON)

	driver, _ := GetDriver("clash")
	parsedJSON, err := driver.ParseURI(uriJSON)
	if err != nil {
		t.Fatalf("ParseURI JSON failed: %v", err)
	}
	if parsedJSON["name"] != "json-node" {
		t.Fatalf("unexpected parsedJSON: %#v", parsedJSON)
	}

	rawYAML := []byte("name: yaml-node\ntype: socks5\nserver: 1.2.3.4\nport: 1080\n")
	uriYAML := "clash://" + base64.StdEncoding.EncodeToString(rawYAML)
	parsedYAML, err := driver.ParseURI(uriYAML)
	if err != nil {
		t.Fatalf("ParseURI YAML failed: %v", err)
	}
	if parsedYAML["name"] != "yaml-node" || parsedYAML["type"] != "socks5" {
		t.Fatalf("unexpected parsedYAML: %#v", parsedYAML)
	}
}
