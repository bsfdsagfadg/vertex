package transport

import (
	"fmt"
	"net/url"

	"github.com/bsfdsagfadg/vertex/internal/strutil"
)


// SocksDriver handles SOCKS5 (socks5://, socks5h://, socks://) protocol URIs.
type SocksDriver struct{}

func (d *SocksDriver) Scheme() string {
	return "socks5"
}

func (d *SocksDriver) ParseURI(rawURI string) (map[string]any, error) {
	u, err := url.Parse(rawURI)
	if err != nil {
		return nil, fmt.Errorf("socks parse failed: %w", err)
	}
	port, err := parseProxyPort(u, 1080)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"name":   nameFromURL(u),
		"type":   "socks5",
		"server": u.Hostname(),
		"port":   port,
		"udp":    true,
	}
	if u.User != nil {
		out["username"] = u.User.Username()
		if password, ok := u.User.Password(); ok {
			out["password"] = password
		}
	}
	return out, nil
}

func (d *SocksDriver) FormatURI(cfg map[string]any) (string, error) {
	server, _ := cfg["server"].(string)
	if server == "" {
		return "", fmt.Errorf("socks format failed: missing server")
	}
	port := strutil.ToInt(cfg["port"], 1080)
	username, _ := cfg["username"].(string)
	password, _ := cfg["password"].(string)
	name, _ := cfg["name"].(string)

	userinfo := username
	if password != "" {
		userinfo = username + ":" + password
	}

	return buildStandardURI("socks5", userinfo, server, port, nil, name), nil
}
