package transport

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/strutil"
)


// TuicDriver handles TUIC protocol URIs.
type TuicDriver struct{}

func (d *TuicDriver) Scheme() string {
	return "tuic"
}

func (d *TuicDriver) ParseURI(rawURI string) (map[string]any, error) {
	u, err := url.Parse(rawURI)
	if err != nil {
		return nil, fmt.Errorf("tuic parse error: %w", err)
	}
	port, err := parseProxyPort(u, 443)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	name := nameFromURL(u)

	username := ""
	password := ""
	if u.User != nil {
		username = u.User.Username()
		if pass, ok := u.User.Password(); ok {
			password = pass
		}
	}

	out := map[string]any{
		"name":     name,
		"type":     "tuic",
		"server":   u.Hostname(),
		"port":     port,
		"uuid":     username,
		"password": password,
		"tls":      true,
	}

	sni := strutil.FirstNonEmpty(q.Get("sni"), u.Hostname())
	out["sni"] = sni
	out["servername"] = strutil.FirstNonEmpty(q.Get("servername"), sni)
	if queryFlag(q, "allowInsecure", "insecure") {
		out["skip-cert-verify"] = true
	}
	if alpn := q.Get("alpn"); alpn != "" {
		out["alpn"] = strings.Split(alpn, ",")
	}
	if cc := q.Get("congestion_controller"); cc != "" {
		out["congestion-controller"] = cc
	}
	if udpMode := q.Get("udp_relay_mode"); udpMode != "" {
		out["udp-relay-mode"] = udpMode
	}

	return out, nil
}

func (d *TuicDriver) FormatURI(cfg map[string]any) (string, error) {
	server, _ := cfg["server"].(string)
	if server == "" {
		return "", fmt.Errorf("tuic format failed: missing server")
	}
	port := strutil.ToInt(cfg["port"], 443)
	uuid, _ := cfg["uuid"].(string)
	password, _ := cfg["password"].(string)
	name, _ := cfg["name"].(string)

	userinfo := uuid
	if password != "" {
		userinfo = uuid + ":" + password
	}

	q := make(url.Values)
	if sni, _ := cfg["sni"].(string); sni != "" {
		q.Set("sni", sni)
	}
	if alpn := extractALPN(cfg); len(alpn) > 0 {
		q.Set("alpn", strings.Join(alpn, ","))
	}
	if cc, _ := cfg["congestion-controller"].(string); cc != "" {
		q.Set("congestion_controller", cc)
	}
	if udpMode, _ := cfg["udp-relay-mode"].(string); udpMode != "" {
		q.Set("udp_relay_mode", udpMode)
	}
	if skip, _ := cfg["skip-cert-verify"].(bool); skip {
		q.Set("insecure", "1")
	}

	return buildStandardURI("tuic", userinfo, server, port, q, name), nil
}
