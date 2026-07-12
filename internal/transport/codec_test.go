package transport

import (
	"encoding/base64"
	"testing"
)

func TestParseURIShadowsocksKeepsPortAndPlugin(t *testing.T) {
	raw := "ss://YWVzLTEyOC1nY206aGFNTE1YaXJCeW42ckdWaA@example.com:20111/?plugin=simple-obfs%3Bobfs%3Dhttp%3Bobfs-host%3Dcdn.example.com#demo"

	out, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}

	if got := out["port"]; got != 20111 {
		t.Fatalf("expected port 20111, got %#v", got)
	}
	if got := out["plugin"]; got != "obfs" {
		t.Fatalf("expected plugin obfs, got %#v", got)
	}
	opts, ok := out["plugin-opts"].(map[string]any)
	if !ok {
		t.Fatalf("plugin-opts missing or wrong type: %#v", out["plugin-opts"])
	}
	if opts["mode"] != "http" || opts["host"] != "cdn.example.com" {
		t.Fatalf("unexpected plugin opts: %#v", opts)
	}
}

func TestParseURIVlessKeepsRealityAndWS(t *testing.T) {
	raw := "vless://12345678-1234-1234-1234-123456789012@cf.example.com:443?security=reality&sni=edge.example.com&fp=chrome&pbk=pubkey&sid=abcd&type=ws&host=edge.example.com&path=%2Fws&flow=xtls-rprx-vision#demo"

	out, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}

	if got := out["servername"]; got != "edge.example.com" {
		t.Fatalf("expected servername edge.example.com, got %#v", got)
	}
	if got := out["client-fingerprint"]; got != "chrome" {
		t.Fatalf("expected client-fingerprint chrome, got %#v", got)
	}
	if got := out["flow"]; got != "xtls-rprx-vision" {
		t.Fatalf("expected flow preserved, got %#v", got)
	}
	realityOpts, ok := out["reality-opts"].(map[string]any)
	if !ok {
		t.Fatalf("reality-opts missing or wrong type: %#v", out["reality-opts"])
	}
	if realityOpts["public-key"] != "pubkey" || realityOpts["short-id"] != "abcd" {
		t.Fatalf("unexpected reality opts: %#v", realityOpts)
	}
	if got := out["network"]; got != "ws" {
		t.Fatalf("expected network ws, got %#v", got)
	}
	wsOpts, ok := out["ws-opts"].(map[string]any)
	if !ok {
		t.Fatalf("ws-opts missing or wrong type: %#v", out["ws-opts"])
	}
	headers, ok := wsOpts["headers"].(map[string]any)
	if !ok {
		t.Fatalf("ws headers missing or wrong type: %#v", wsOpts["headers"])
	}
	if wsOpts["path"] != "/ws" || headers["Host"] != "edge.example.com" {
		t.Fatalf("unexpected ws opts: %#v", wsOpts)
	}
}

func TestParseURIHy2KeepsPortRange(t *testing.T) {
	raw := "hy2://secret@203.10.99.51:20000?sni=www.bing.com&insecure=1&ports=20000-55000#demo"

	out, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}

	if got := out["ports"]; got != "20000-55000" {
		t.Fatalf("expected ports preserved, got %#v", got)
	}
	if got := out["sni"]; got != "www.bing.com" {
		t.Fatalf("expected sni preserved, got %#v", got)
	}
	if got := out["skip-cert-verify"]; got != true {
		t.Fatalf("expected skip-cert-verify=true, got %#v", got)
	}
}

func TestParseURIVmessKeepsSNIAndFingerprint(t *testing.T) {
	rawJSON := `{"v":"2","ps":"demo","add":"vmess.example.com","port":"443","id":"12345678-1234-1234-1234-123456789012","aid":"0","net":"ws","host":"edge.example.com","path":"/ws","tls":"tls","sni":"edge.example.com","fp":"chrome","alpn":"h2,http/1.1","allowInsecure":"1"}`
	raw := "vmess://" + base64.StdEncoding.EncodeToString([]byte(rawJSON))

	out, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}

	if out["servername"] != "edge.example.com" || out["client-fingerprint"] != "chrome" {
		t.Fatalf("tls metadata not preserved: %#v", out)
	}
	alpn, ok := out["alpn"].([]string)
	if !ok || len(alpn) != 2 || alpn[0] != "h2" {
		t.Fatalf("alpn not preserved: %#v", out["alpn"])
	}
}

func TestParseURISOCKS5(t *testing.T) {
	raw := "socks5://user:pass@192.168.1.1:1080#my-socks5"
	out, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if out["type"] != "socks5" {
		t.Fatalf("expected type socks5, got %#v", out["type"])
	}
	if out["name"] != "my-socks5" {
		t.Fatalf("expected name my-socks5, got %#v", out["name"])
	}
	if out["server"] != "192.168.1.1" {
		t.Fatalf("expected server 192.168.1.1, got %#v", out["server"])
	}
	if out["port"] != 1080 {
		t.Fatalf("expected port 1080, got %#v", out["port"])
	}
	if out["username"] != "user" || out["password"] != "pass" {
		t.Fatalf("expected user/pass, got username=%#v password=%#v", out["username"], out["password"])
	}
}

func TestParseURISOCKS5NoAuth(t *testing.T) {
	raw := "socks5://192.168.1.1:1080#no-auth"
	out, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if out["type"] != "socks5" {
		t.Fatalf("expected type socks5, got %#v", out["type"])
	}
	if out["name"] != "no-auth" {
		t.Fatalf("expected name no-auth, got %#v", out["name"])
	}
	if out["server"] != "192.168.1.1" {
		t.Fatalf("expected server 192.168.1.1, got %#v", out["server"])
	}
	if out["port"] != 1080 {
		t.Fatalf("expected port 1080, got %#v", out["port"])
	}
	if _, ok := out["username"]; ok {
		t.Fatalf("expected no username field, got %#v", out["username"])
	}
}

func TestParseURSocks5h(t *testing.T) {
	raw := "socks5h://admin:secret@10.0.0.1:1080#socks5h"
	out, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if out["type"] != "socks5" {
		t.Fatalf("expected type socks5, got %#v", out["type"])
	}
}

func TestParseURSocks(t *testing.T) {
	raw := "socks://user:pass@10.0.0.1:1080#socks"
	out, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if out["type"] != "socks5" {
		t.Fatalf("expected type socks5, got %#v", out["type"])
	}
}

func TestParseURIHTTP(t *testing.T) {
	raw := "http://user:pass@proxy.example.com:8080#http-proxy"
	out, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if out["type"] != "http" {
		t.Fatalf("expected type http, got %#v", out["type"])
	}
	if out["name"] != "http-proxy" {
		t.Fatalf("expected name http-proxy, got %#v", out["name"])
	}
	if out["server"] != "proxy.example.com" {
		t.Fatalf("expected server proxy.example.com, got %#v", out["server"])
	}
	if out["port"] != 8080 {
		t.Fatalf("expected port 8080, got %#v", out["port"])
	}
	if out["username"] != "user" || out["password"] != "pass" {
		t.Fatalf("expected user/pass, got username=%#v password=%#v", out["username"], out["password"])
	}
	if tls, ok := out["tls"]; ok && tls == true {
		t.Fatalf("expected no tls for http, got tls=true")
	}
}

func TestParseURIHTTPS(t *testing.T) {
	raw := "https://user:pass@proxy.example.com:443#https-proxy"
	out, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if out["type"] != "http" {
		t.Fatalf("expected type http, got %#v", out["type"])
	}
	if out["name"] != "https-proxy" {
		t.Fatalf("expected name https-proxy, got %#v", out["name"])
	}
	if out["server"] != "proxy.example.com" {
		t.Fatalf("expected server proxy.example.com, got %#v", out["server"])
	}
	if out["port"] != 443 {
		t.Fatalf("expected port 443, got %#v", out["port"])
	}
	if out["tls"] != true {
		t.Fatalf("expected tls=true for https, got %#v", out["tls"])
	}
}

func TestParseURISSR(t *testing.T) {
	ssrDecoded := "1.2.3.4:1234:origin:aes-256-cfb:tls1.2_ticket_auth:dGVzdHBhc3M=?remarks=my-ssr"
	raw := "ssr://" + base64.StdEncoding.EncodeToString([]byte(ssrDecoded))

	out, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if out["type"] != "ssr" {
		t.Fatalf("expected type ssr, got %#v", out["type"])
	}
	if out["name"] != "my-ssr" {
		t.Fatalf("expected name my-ssr, got %#v", out["name"])
	}
	if out["server"] != "1.2.3.4" {
		t.Fatalf("expected server 1.2.3.4, got %#v", out["server"])
	}
	if out["port"] != 1234 {
		t.Fatalf("expected port 1234, got %#v", out["port"])
	}
	if out["cipher"] != "aes-256-cfb" {
		t.Fatalf("expected cipher aes-256-cfb, got %#v", out["cipher"])
	}
	if out["password"] != "testpass" {
		t.Fatalf("expected password testpass, got %#v", out["password"])
	}
	if out["protocol"] != "origin" {
		t.Fatalf("expected protocol origin, got %#v", out["protocol"])
	}
	if out["obfs"] != "tls1.2_ticket_auth" {
		t.Fatalf("expected obfs tls1.2_ticket_auth, got %#v", out["obfs"])
	}
}

func TestParseURIShadowsocksr(t *testing.T) {
	ssrDecoded := "10.0.0.1:443:auth_aes128_md5:chacha20-ietf:http_simple:MTIzNDU2Nzg5MA==?remarks=ssr-node"
	raw := "shadowsocksr://" + base64.StdEncoding.EncodeToString([]byte(ssrDecoded))

	out, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if out["type"] != "ssr" {
		t.Fatalf("expected type ssr, got %#v", out["type"])
	}
	if out["name"] != "ssr-node" {
		t.Fatalf("expected name ssr-node, got %#v", out["name"])
	}
	if out["password"] != "1234567890" {
		t.Fatalf("expected password 1234567890, got %#v", out["password"])
	}
}

func TestParseURIHysteria(t *testing.T) {
	raw := "hysteria://host.example.com:443?auth=mysecret&obfs=xplus&sni=sni.example.com&insecure=1&alpn=h3#hysteria-node"
	out, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if out["type"] != "hysteria" {
		t.Fatalf("expected type hysteria, got %#v", out["type"])
	}
	if out["name"] != "hysteria-node" {
		t.Fatalf("expected name hysteria-node, got %#v", out["name"])
	}
	if out["server"] != "host.example.com" {
		t.Fatalf("expected server host.example.com, got %#v", out["server"])
	}
	if out["port"] != 443 {
		t.Fatalf("expected port 443, got %#v", out["port"])
	}
	if out["auth_str"] != "mysecret" {
		t.Fatalf("expected auth_str mysecret, got %#v", out["auth_str"])
	}
	if out["tls"] != true {
		t.Fatalf("expected tls true, got %#v", out["tls"])
	}
	if out["sni"] != "sni.example.com" {
		t.Fatalf("expected sni sni.example.com, got %#v", out["sni"])
	}
	if out["obfs"] != "xplus" {
		t.Fatalf("expected obfs xplus, got %#v", out["obfs"])
	}
	if out["skip-cert-verify"] != true {
		t.Fatalf("expected skip-cert-verify true, got %#v", out["skip-cert-verify"])
	}
	alpn, ok := out["alpn"].([]string)
	if !ok || len(alpn) != 1 || alpn[0] != "h3" {
		t.Fatalf("expected alpn [h3], got %#v", out["alpn"])
	}
}

func TestParseURIAnyTLS(t *testing.T) {
	raw := "anytls://mypassword@host.example.com:443#my-anytls"
	out, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if out["type"] != "anytls" {
		t.Fatalf("expected type anytls, got %#v", out["type"])
	}
	if out["name"] != "my-anytls" {
		t.Fatalf("expected name my-anytls, got %#v", out["name"])
	}
	if out["server"] != "host.example.com" {
		t.Fatalf("expected server host.example.com, got %#v", out["server"])
	}
	if out["port"] != 443 {
		t.Fatalf("expected port 443, got %#v", out["port"])
	}
	if out["password"] != "mypassword" {
		t.Fatalf("expected password mypassword, got %#v", out["password"])
	}
	if out["tls"] != true {
		t.Fatalf("expected tls true, got %#v", out["tls"])
	}
	if out["sni"] != "host.example.com" {
		t.Fatalf("expected sni host.example.com, got %#v", out["sni"])
	}
}
