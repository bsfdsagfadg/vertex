package transport

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/strutil"
)


// HysteriaDriver handles Hysteria v1 (hysteria://) protocol URIs.
type HysteriaDriver struct{}

func (d *HysteriaDriver) Scheme() string {
	return "hysteria"
}

func (d *HysteriaDriver) ParseURI(rawURI string) (map[string]any, error) {
	u, err := url.Parse(rawURI)
	if err != nil {
		return nil, fmt.Errorf("hysteria parse failed: %w", err)
	}
	port, err := parseProxyPort(u, 443)
	if err != nil {
		return nil, err
	}
	query := u.Query()
	up := normalizeHysteriaSpeed(strutil.FirstNonEmpty(query.Get("up"), query.Get("upmbps"), query.Get("up-speed")))
	down := normalizeHysteriaSpeed(strutil.FirstNonEmpty(query.Get("down"), query.Get("downmbps"), query.Get("down-speed")))
	if up == "" || down == "" {
		return nil, fmt.Errorf("hysteria parse failed: up/down speed is required")
	}
	auth := query.Get("auth")
	if u.User != nil && u.User.Username() != "" {
		auth = u.User.Username()
	}
	out := map[string]any{
		"name":     nameFromURL(u),
		"type":     "hysteria",
		"server":   u.Hostname(),
		"port":     port,
		"up":       up,
		"down":     down,
		"auth-str": auth,
		"sni":      strutil.FirstNonEmpty(query.Get("sni"), query.Get("peer"), u.Hostname()),
	}
	if queryFlag(query, "allowInsecure", "insecure") {
		out["skip-cert-verify"] = true
	}
	if obfs := query.Get("obfs"); obfs != "" {
		out["obfs"] = obfs
	}
	if alpn := query.Get("alpn"); alpn != "" {
		out["alpn"] = strings.Split(alpn, ",")
	}
	return out, nil
}

func (d *HysteriaDriver) FormatURI(cfg map[string]any) (string, error) {
	server, _ := cfg["server"].(string)
	if server == "" {
		return "", fmt.Errorf("hysteria format failed: missing server")
	}
	port := strutil.ToInt(cfg["port"], 443)
	auth, _ := cfg["auth-str"].(string)
	name, _ := cfg["name"].(string)

	q := make(url.Values)
	if up, _ := cfg["up"].(string); up != "" {
		q.Set("up", up)
	}
	if down, _ := cfg["down"].(string); down != "" {
		q.Set("down", down)
	}
	if sni, _ := cfg["sni"].(string); sni != "" {
		q.Set("sni", sni)
	}
	if obfs, _ := cfg["obfs"].(string); obfs != "" {
		q.Set("obfs", obfs)
	}
	if alpn := extractALPN(cfg); len(alpn) > 0 {
		q.Set("alpn", strings.Join(alpn, ","))
	}
	if skip, _ := cfg["skip-cert-verify"].(bool); skip {
		q.Set("insecure", "1")
	}

	return buildStandardURI("hysteria", auth, server, port, q, name), nil
}
