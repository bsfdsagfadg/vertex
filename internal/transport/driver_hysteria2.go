package transport

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/strutil"
)


// Hysteria2Driver handles Hysteria2 (hysteria2://, hy2://) protocol URIs.
type Hysteria2Driver struct{}

func (d *Hysteria2Driver) Scheme() string {
	return "hysteria2"
}

func (d *Hysteria2Driver) ParseURI(rawURI string) (map[string]any, error) {
	u, err := url.Parse(rawURI)
	if err != nil {
		return nil, fmt.Errorf("hysteria2 parse error: %w", err)
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
		"type":     "hysteria2",
		"server":   u.Hostname(),
		"port":     port,
		"password": password,
		"tls":      true,
	}

	sni := strutil.FirstNonEmpty(q.Get("sni"), q.Get("peer"), u.Hostname())
	if sni != "" {
		out["sni"] = sni
		out["servername"] = sni
	}
	if ports := strutil.FirstNonEmpty(q.Get("ports"), q.Get("mport")); ports != "" {
		out["ports"] = ports
	}
	if obfs := q.Get("obfs"); obfs != "" {
		out["obfs"] = obfs
	}
	if obfsPassword := strutil.FirstNonEmpty(q.Get("obfs-password"), q.Get("obfsPassword")); obfsPassword != "" {
		out["obfs-password"] = obfsPassword
	}
	if fp := strutil.FirstNonEmpty(q.Get("fp"), q.Get("fingerprint")); fp != "" {
		if isCertPinningFingerprint(fp) {
			out["fingerprint"] = fp
		}
	}
	if alpn := q.Get("alpn"); alpn != "" {
		out["alpn"] = strings.Split(alpn, ",")
	}
	if queryFlag(q, "allowInsecure", "insecure") {
		out["skip-cert-verify"] = true
	}

	return out, nil
}

func (d *Hysteria2Driver) FormatURI(cfg map[string]any) (string, error) {
	server, _ := cfg["server"].(string)
	if server == "" {
		return "", fmt.Errorf("hysteria2 format failed: missing server")
	}
	port := strutil.ToInt(cfg["port"], 443)
	password, _ := cfg["password"].(string)
	name, _ := cfg["name"].(string)

	q := make(url.Values)
	if sni, _ := cfg["sni"].(string); sni != "" {
		q.Set("sni", sni)
	}
	if ports, _ := cfg["ports"].(string); ports != "" {
		q.Set("ports", ports)
	}
	if obfs, _ := cfg["obfs"].(string); obfs != "" {
		q.Set("obfs", obfs)
	}
	if obfsPass, _ := cfg["obfs-password"].(string); obfsPass != "" {
		q.Set("obfs-password", obfsPass)
	}
	if fp, _ := cfg["fingerprint"].(string); fp != "" {
		q.Set("fp", fp)
	}
	if alpn := extractALPN(cfg); len(alpn) > 0 {
		q.Set("alpn", strings.Join(alpn, ","))
	}
	if skip, _ := cfg["skip-cert-verify"].(bool); skip {
		q.Set("insecure", "1")
	}

	return buildStandardURI("hy2", password, server, port, q, name), nil
}
