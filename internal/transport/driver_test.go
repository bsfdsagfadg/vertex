package transport

import (
	"reflect"
	"strings"
	"testing"
)

func TestDriversRegistry(t *testing.T) {
	drivers := ListDrivers()
	if len(drivers) < 10 {
		t.Fatalf("expected at least 10 drivers, got %d", len(drivers))
	}

	schemes := []string{
		"vless", "vmess", "trojan", "ss", "ssr", "shadowsocksr",
		"hysteria2", "hy2", "hysteria", "tuic", "socks5", "socks5h", "socks",
		"http", "https", "anytls", "clash",
	}

	for _, scheme := range schemes {
		driver, ok := GetDriver(scheme)
		if !ok || driver == nil {
			t.Fatalf("driver for scheme %q not found", scheme)
		}
		if !IsSupportedScheme(scheme) {
			t.Fatalf("scheme %q should be supported", scheme)
		}
	}
}

func TestVlessDriverRoundTrip(t *testing.T) {
	driver, _ := GetDriver("vless")
	rawURI := "vless://12345678-1234-1234-1234-123456789012@vless.example.com:443?alpn=h2%2Chttp%2F1.1&flow=xtls-rprx-vision&fp=chrome&host=vless.example.com&packetAddr=true&path=%2Fws&pbk=publickey&security=reality&sid=shortid&sni=vless.example.com&type=ws&xudp=true#demo-node"

	parsed, err := driver.ParseURI(rawURI)
	if err != nil {
		t.Fatalf("ParseURI failed: %v", err)
	}

	if parsed["name"] != "demo-node" || parsed["uuid"] != "12345678-1234-1234-1234-123456789012" {
		t.Fatalf("unexpected parsed values: %#v", parsed)
	}
	if parsed["flow"] != "xtls-rprx-vision" || parsed["network"] != "ws" {
		t.Fatalf("unexpected flow or network: %#v", parsed)
	}

	formatted, err := driver.FormatURI(parsed)
	if err != nil {
		t.Fatalf("FormatURI failed: %v", err)
	}

	reparsed, err := driver.ParseURI(formatted)
	if err != nil {
		t.Fatalf("Reparsing formatted URI failed: %v", err)
	}

	if reparsed["uuid"] != parsed["uuid"] || reparsed["server"] != parsed["server"] || reparsed["port"] != parsed["port"] {
		t.Fatalf("reparsed mismatch: %#v vs %#v", reparsed, parsed)
	}
}

func TestVmessDriverRoundTrip(t *testing.T) {
	driver, _ := GetDriver("vmess")
	cfg := map[string]any{
		"name":               "vmess-test",
		"type":               "vmess",
		"server":             "vmess.example.com",
		"port":               443,
		"uuid":               "12345678-1234-1234-1234-123456789012",
		"alterId":            0,
		"cipher":             "auto",
		"tls":                true,
		"sni":                "vmess.example.com",
		"client-fingerprint": "chrome",
		"alpn":               []string{"h2", "http/1.1"},
		"network":            "ws",
		"ws-opts": map[string]any{
			"path": "/ws",
			"headers": map[string]any{
				"Host": "vmess.example.com",
			},
		},
	}

	formatted, err := driver.FormatURI(cfg)
	if err != nil {
		t.Fatalf("FormatURI failed: %v", err)
	}

	parsed, err := driver.ParseURI(formatted)
	if err != nil {
		t.Fatalf("ParseURI failed: %v", err)
	}

	if parsed["name"] != cfg["name"] || parsed["uuid"] != cfg["uuid"] || parsed["server"] != cfg["server"] {
		t.Fatalf("parsed mismatch: %#v vs %#v", parsed, cfg)
	}
	if parsed["tls"] != true || parsed["network"] != "ws" {
		t.Fatalf("tls/network mismatch: %#v", parsed)
	}
}

func TestShadowsocksDriverRoundTrip(t *testing.T) {
	driver, _ := GetDriver("ss")
	cfg := map[string]any{
		"name":     "ss-test",
		"type":     "ss",
		"server":   "ss.example.com",
		"port":     8388,
		"cipher":   "aes-256-gcm",
		"password": "secret-password",
		"plugin":   "obfs",
		"plugin-opts": map[string]any{
			"mode": "http",
			"host": "ss.example.com",
		},
	}

	formatted, err := driver.FormatURI(cfg)
	if err != nil {
		t.Fatalf("FormatURI failed: %v", err)
	}

	parsed, err := driver.ParseURI(formatted)
	if err != nil {
		t.Fatalf("ParseURI failed: %v", err)
	}

	if parsed["name"] != cfg["name"] || parsed["cipher"] != cfg["cipher"] || parsed["password"] != cfg["password"] {
		t.Fatalf("parsed mismatch: %#v vs %#v", parsed, cfg)
	}
	if parsed["plugin"] != "obfs" {
		t.Fatalf("plugin mismatch: %#v", parsed)
	}
}

func TestHysteria2DriverRoundTrip(t *testing.T) {
	driver, _ := GetDriver("hy2")
	cfg := map[string]any{
		"name":          "hy2-test",
		"type":          "hysteria2",
		"server":        "hy2.example.com",
		"port":          443,
		"password":      "secret-pass",
		"sni":           "edge.example.com",
		"ports":         "20000-50000",
		"obfs":          "salamander",
		"obfs-password": "obfspassword",
		"alpn":          []string{"h3"},
	}

	formatted, err := driver.FormatURI(cfg)
	if err != nil {
		t.Fatalf("FormatURI failed: %v", err)
	}

	parsed, err := driver.ParseURI(formatted)
	if err != nil {
		t.Fatalf("ParseURI failed: %v", err)
	}

	if parsed["name"] != cfg["name"] || parsed["password"] != cfg["password"] || parsed["ports"] != cfg["ports"] {
		t.Fatalf("parsed mismatch: %#v vs %#v", parsed, cfg)
	}
	if parsed["obfs"] != cfg["obfs"] || parsed["obfs-password"] != cfg["obfs-password"] {
		t.Fatalf("obfs mismatch: %#v", parsed)
	}
}

func TestSocks5DriverRoundTrip(t *testing.T) {
	driver, _ := GetDriver("socks5")
	cfg := map[string]any{
		"name":     "socks-test",
		"type":     "socks5",
		"server":   "127.0.0.1",
		"port":     1080,
		"username": "user",
		"password": "pass",
	}

	formatted, err := driver.FormatURI(cfg)
	if err != nil {
		t.Fatalf("FormatURI failed: %v", err)
	}

	parsed, err := driver.ParseURI(formatted)
	if err != nil {
		t.Fatalf("ParseURI failed: %v", err)
	}

	if parsed["username"] != "user" || parsed["password"] != "pass" || parsed["server"] != "127.0.0.1" || parsed["port"] != 1080 {
		t.Fatalf("socks parsed mismatch: %#v", parsed)
	}
}

func TestHTTPDriverRoundTrip(t *testing.T) {
	driver, _ := GetDriver("http")
	cfg := map[string]any{
		"name":     "https-test",
		"type":     "http",
		"server":   "proxy.example.com",
		"port":     8443,
		"username": "admin",
		"password": "secret",
		"tls":      true,
		"sni":      "proxy.example.com",
	}

	formatted, err := driver.FormatURI(cfg)
	if err != nil {
		t.Fatalf("FormatURI failed: %v", err)
	}
	if !strings.HasPrefix(formatted, "https://") {
		t.Fatalf("expected https scheme for TLS http proxy, got %s", formatted)
	}

	parsed, err := driver.ParseURI(formatted)
	if err != nil {
		t.Fatalf("ParseURI failed: %v", err)
	}

	if parsed["tls"] != true || parsed["username"] != "admin" || parsed["password"] != "secret" {
		t.Fatalf("https parsed mismatch: %#v", parsed)
	}
}

func TestFormatURIGeneric(t *testing.T) {
	cfg := map[string]any{
		"name":     "generic-trojan",
		"type":     "trojan",
		"server":   "1.2.3.4",
		"port":     443,
		"password": "pass",
		"sni":      "example.com",
	}

	uri, err := FormatURI(cfg)
	if err != nil {
		t.Fatalf("FormatURI failed: %v", err)
	}
	if !strings.HasPrefix(uri, "trojan://") {
		t.Fatalf("expected trojan://, got %s", uri)
	}

	parsed, err := ParseURI(uri)
	if err != nil {
		t.Fatalf("ParseURI failed: %v", err)
	}
	if parsed["password"] != "pass" || parsed["sni"] != "example.com" {
		t.Fatalf("mismatch: %#v", parsed)
	}
}

func TestExtractALPNTypes(t *testing.T) {
	cases := []struct {
		input map[string]any
		want  []string
	}{
		{map[string]any{"alpn": []string{"h2", "http/1.1"}}, []string{"h2", "http/1.1"}},
		{map[string]any{"alpn": []any{"h2", "http/1.1"}}, []string{"h2", "http/1.1"}},
		{map[string]any{"alpn": "h2,http/1.1"}, []string{"h2", "http/1.1"}},
	}
	for i, tc := range cases {
		got := extractALPN(tc.input)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("case %d: got %#v, want %#v", i, got, tc.want)
		}
	}
}
