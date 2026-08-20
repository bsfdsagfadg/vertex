package transport

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/strutil"
)


// HTTPDriver handles HTTP and HTTPS proxy protocol URIs.
type HTTPDriver struct{}

func (d *HTTPDriver) Scheme() string {
	return "http"
}

func (d *HTTPDriver) ParseURI(rawURI string) (map[string]any, error) {
	u, err := url.Parse(rawURI)
	if err != nil {
		return nil, fmt.Errorf("http proxy parse failed: %w", err)
	}
	defaultPort := 80
	if strings.EqualFold(u.Scheme, "https") {
		defaultPort = 443
	}
	port, err := parseProxyPort(u, defaultPort)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"name":   nameFromURL(u),
		"type":   "http",
		"server": u.Hostname(),
		"port":   port,
	}
	if u.User != nil {
		out["username"] = u.User.Username()
		if password, ok := u.User.Password(); ok {
			out["password"] = password
		}
	}
	if strings.EqualFold(u.Scheme, "https") {
		out["tls"] = true
		out["sni"] = strutil.FirstNonEmpty(u.Query().Get("sni"), u.Hostname())
		if queryFlag(u.Query(), "allowInsecure", "insecure") {
			out["skip-cert-verify"] = true
		}
	}
	return out, nil
}

func (d *HTTPDriver) FormatURI(cfg map[string]any) (string, error) {
	server, _ := cfg["server"].(string)
	if server == "" {
		return "", fmt.Errorf("http proxy format failed: missing server")
	}
	tls, _ := cfg["tls"].(bool)
	defaultPort := 80
	scheme := "http"
	if tls {
		defaultPort = 443
		scheme = "https"
	}
	port := strutil.ToInt(cfg["port"], defaultPort)
	username, _ := cfg["username"].(string)
	password, _ := cfg["password"].(string)
	name, _ := cfg["name"].(string)

	userinfo := username
	if password != "" {
		userinfo = username + ":" + password
	}

	var q url.Values
	if tls {
		q = make(url.Values)
		if sni, _ := cfg["sni"].(string); sni != "" {
			q.Set("sni", sni)
		}
		if skip, _ := cfg["skip-cert-verify"].(bool); skip {
			q.Set("insecure", "1")
		}
	}

	return buildStandardURI(scheme, userinfo, server, port, q, name), nil
}
