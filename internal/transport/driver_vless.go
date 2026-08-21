package transport

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/strutil"
)


// VlessDriver handles VLESS protocol URIs.
type VlessDriver struct{}

func (d *VlessDriver) Scheme() string {
	return "vless"
}

func (d *VlessDriver) ParseURI(rawURI string) (map[string]any, error) {
	u, err := url.Parse(rawURI)
	if err != nil {
		return nil, fmt.Errorf("vless parse error: %w", err)
	}
	port, err := parseProxyPort(u, 443)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	name := nameFromURL(u)

	username := ""
	if u.User != nil {
		username = u.User.Username()
	}

	out := map[string]any{
		"name":   name,
		"type":   "vless",
		"server": u.Hostname(),
		"port":   port,
		"uuid":   username,
	}

	sec := strings.ToLower(q.Get("security"))
	if sec == "reality" || sec == "tls" {
		out["tls"] = true
		sni := strutil.FirstNonEmpty(q.Get("sni"), u.Hostname())
		out["sni"] = sni
		out["servername"] = strutil.FirstNonEmpty(q.Get("servername"), sni)
		if sec != "reality" && queryFlag(q, "allowInsecure", "insecure") {
			out["skip-cert-verify"] = true
		}
	}

	if sec == "reality" {
		if pubKey := strutil.FirstNonEmpty(q.Get("pbk"), q.Get("public-key")); pubKey != "" {
			out["reality-opts"] = map[string]any{
				"public-key": pubKey,
				"short-id":   strutil.FirstNonEmpty(q.Get("sid"), q.Get("short-id")),
			}
		}
		if strutil.FirstNonEmpty(q.Get("fp"), q.Get("client-fingerprint"), q.Get("fingerprint")) == "" {
			out["client-fingerprint"] = "chrome"
		}
	}

	if flow := q.Get("flow"); flow != "" {
		out["flow"] = flow
	}
	if fp := strutil.FirstNonEmpty(q.Get("fp"), q.Get("client-fingerprint"), q.Get("fingerprint")); fp != "" {
		out["client-fingerprint"] = fp
	} else if out["tls"] == true {
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

func (d *VlessDriver) FormatURI(cfg map[string]any) (string, error) {
	server, _ := cfg["server"].(string)
	if server == "" {
		return "", fmt.Errorf("vless format failed: missing server")
	}
	port := strutil.ToInt(cfg["port"], 443)
	uuid, _ := cfg["uuid"].(string)
	name, _ := cfg["name"].(string)

	q := make(url.Values)
	if flow, _ := cfg["flow"].(string); flow != "" {
		q.Set("flow", flow)
	}

	if tls, _ := cfg["tls"].(bool); tls {
		if realityOpts, ok := cfg["reality-opts"].(map[string]any); ok {
			q.Set("security", "reality")
			if pbk, _ := realityOpts["public-key"].(string); pbk != "" {
				q.Set("pbk", pbk)
			}
			if sid, _ := realityOpts["short-id"].(string); sid != "" {
				q.Set("sid", sid)
			}
		} else {
			q.Set("security", "tls")
			if skip, _ := cfg["skip-cert-verify"].(bool); skip {
				q.Set("allowInsecure", "1")
			}
		}
		sni, _ := cfg["sni"].(string)
		if sni == "" {
			sni, _ = cfg["servername"].(string)
		}
		if sni != "" {
			q.Set("sni", sni)
		}
		if fp, _ := cfg["client-fingerprint"].(string); fp != "" {
			q.Set("fp", fp)
		}
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

	return buildStandardURI("vless", uuid, server, port, q, name), nil
}
