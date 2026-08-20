package transport

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/strutil"
)


// TrojanDriver handles Trojan protocol URIs.
type TrojanDriver struct{}

func (d *TrojanDriver) Scheme() string {
	return "trojan"
}

func (d *TrojanDriver) ParseURI(rawURI string) (map[string]any, error) {
	u, err := url.Parse(rawURI)
	if err != nil {
		return nil, fmt.Errorf("trojan parse error: %w", err)
	}
	port, err := parseProxyPort(u, 443)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	name := nameFromURL(u)

	password := ""
	if u.User != nil {
		password = u.User.Username()
		if pass, ok := u.User.Password(); ok && pass != "" {
			password = pass
		}
	}

	out := map[string]any{
		"name":     name,
		"type":     "trojan",
		"server":   u.Hostname(),
		"port":     port,
		"password": password,
		"tls":      true,
	}

	sni := strutil.FirstNonEmpty(q.Get("sni"), u.Hostname())
	out["sni"] = sni
	out["servername"] = strutil.FirstNonEmpty(q.Get("servername"), sni)
	if queryFlag(q, "allowInsecure", "insecure") {
		out["skip-cert-verify"] = true
	}

	if flow := q.Get("flow"); flow != "" {
		out["flow"] = flow
	}
	if fp := strutil.FirstNonEmpty(q.Get("fp"), q.Get("client-fingerprint"), q.Get("fingerprint")); fp != "" {
		out["client-fingerprint"] = fp
	} else {
		out["client-fingerprint"] = "chrome"
	}

	applyNetworkOpts(out, q.Get("type"), q.Get("path"), q.Get("host"), q.Get("mode"), q.Get("serviceName"))

	if alpn := q.Get("alpn"); alpn != "" {
		out["alpn"] = strings.Split(alpn, ",")
	}
	if q.Get("packetAddr") == "true" {
		out["packet-addr"] = true
	}
	if q.Get("xudp") == "true" {
		out["xudp"] = true
	}

	return out, nil
}

func (d *TrojanDriver) FormatURI(cfg map[string]any) (string, error) {
	server, _ := cfg["server"].(string)
	if server == "" {
		return "", fmt.Errorf("trojan format failed: missing server")
	}
	port := strutil.ToInt(cfg["port"], 443)
	password, _ := cfg["password"].(string)
	name, _ := cfg["name"].(string)

	q := make(url.Values)
	if flow, _ := cfg["flow"].(string); flow != "" {
		q.Set("flow", flow)
	}
	if sni, _ := cfg["sni"].(string); sni != "" {
		q.Set("sni", sni)
	}
	if fp, _ := cfg["client-fingerprint"].(string); fp != "" {
		q.Set("fp", fp)
	}
	if skip, _ := cfg["skip-cert-verify"].(bool); skip {
		q.Set("allowInsecure", "1")
	}

	extractNetworkFromOpts(cfg, q)

	if alpn := extractALPN(cfg); len(alpn) > 0 {
		q.Set("alpn", strings.Join(alpn, ","))
	}
	if packetAddr, _ := cfg["packet-addr"].(bool); packetAddr {
		q.Set("packetAddr", "true")
	}
	if xudp, _ := cfg["xudp"].(bool); xudp {
		q.Set("xudp", "true")
	}

	return buildStandardURI("trojan", password, server, port, q, name), nil
}
