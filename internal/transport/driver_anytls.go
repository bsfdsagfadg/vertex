package transport

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/strutil"
)


// AnyTLSDriver handles AnyTLS protocol URIs.
type AnyTLSDriver struct{}

func (d *AnyTLSDriver) Scheme() string {
	return "anytls"
}

func (d *AnyTLSDriver) ParseURI(rawURI string) (map[string]any, error) {
	u, err := url.Parse(rawURI)
	if err != nil {
		return nil, fmt.Errorf("anytls parse failed: %w", err)
	}
	port, err := parseProxyPort(u, 443)
	if err != nil {
		return nil, err
	}
	password := ""
	if u.User != nil {
		password = u.User.Username()
	}
	if password == "" {
		return nil, fmt.Errorf("anytls parse failed: password is required")
	}
	query := u.Query()
	out := map[string]any{
		"name":     nameFromURL(u),
		"type":     "anytls",
		"server":   u.Hostname(),
		"port":     port,
		"password": password,
		"sni":      strutil.FirstNonEmpty(query.Get("sni"), u.Hostname()),
		"udp":      true,
	}
	if queryFlag(query, "allowInsecure", "insecure") {
		out["skip-cert-verify"] = true
	}
	if fp := strutil.FirstNonEmpty(query.Get("fp"), query.Get("fingerprint")); fp != "" {
		out["client-fingerprint"] = fp
	}
	if alpn := query.Get("alpn"); alpn != "" {
		out["alpn"] = strings.Split(alpn, ",")
	}
	return out, nil
}

func (d *AnyTLSDriver) FormatURI(cfg map[string]any) (string, error) {
	server, _ := cfg["server"].(string)
	if server == "" {
		return "", fmt.Errorf("anytls format failed: missing server")
	}
	port := strutil.ToInt(cfg["port"], 443)
	password, _ := cfg["password"].(string)
	if password == "" {
		return "", fmt.Errorf("anytls format failed: missing password")
	}
	name, _ := cfg["name"].(string)

	q := make(url.Values)
	if sni, _ := cfg["sni"].(string); sni != "" {
		q.Set("sni", sni)
	}
	if fp, _ := cfg["client-fingerprint"].(string); fp != "" {
		q.Set("fp", fp)
	}
	if alpn := extractALPN(cfg); len(alpn) > 0 {
		q.Set("alpn", strings.Join(alpn, ","))
	}
	if skip, _ := cfg["skip-cert-verify"].(bool); skip {
		q.Set("insecure", "1")
	}

	return buildStandardURI("anytls", password, server, port, q, name), nil
}
