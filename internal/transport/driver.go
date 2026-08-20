package transport

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

// ProxyProtocolDriver defines the contract for proxy protocol URI parsing and formatting.
type ProxyProtocolDriver interface {
	Scheme() string
	ParseURI(rawURI string) (map[string]any, error)
	FormatURI(cfg map[string]any) (string, error)
}

type driverRegistry struct {
	mu      sync.RWMutex
	drivers map[string]ProxyProtocolDriver
}

//nolint:gochecknoglobals // Package-level driver registry
var defaultRegistry = newDefaultRegistry()

func newDefaultRegistry() *driverRegistry {
	reg := &driverRegistry{
		drivers: make(map[string]ProxyProtocolDriver),
	}
	registerAllBuiltinDrivers(reg)
	return reg
}

func registerAllBuiltinDrivers(reg *driverRegistry) {
	register := func(driver ProxyProtocolDriver, aliases ...string) {
		reg.drivers[strings.ToLower(driver.Scheme())] = driver
		for _, alias := range aliases {
			reg.drivers[strings.ToLower(strings.TrimSpace(alias))] = driver
		}
	}
	register(&VlessDriver{})
	register(&VmessDriver{})
	register(&TrojanDriver{})
	register(&ShadowsocksDriver{})
	register(&ShadowsocksRDriver{}, "shadowsocksr")
	register(&Hysteria2Driver{}, "hy2")
	register(&HysteriaDriver{})
	register(&TuicDriver{})
	register(&SocksDriver{}, "socks5h", "socks")
	register(&HTTPDriver{}, "https")
	register(&AnyTLSDriver{})
	register(&ClashDriver{})
}

// RegisterDriver registers a driver for its primary scheme and any aliases.
func RegisterDriver(driver ProxyProtocolDriver, aliases ...string) {
	defaultRegistry.mu.Lock()
	defer defaultRegistry.mu.Unlock()
	defaultRegistry.drivers[strings.ToLower(driver.Scheme())] = driver
	for _, alias := range aliases {
		defaultRegistry.drivers[strings.ToLower(strings.TrimSpace(alias))] = driver
	}
}

// GetDriver returns the registered driver for the specified scheme.
func GetDriver(scheme string) (ProxyProtocolDriver, bool) {
	defaultRegistry.mu.RLock()
	defer defaultRegistry.mu.RUnlock()
	d, ok := defaultRegistry.drivers[strings.ToLower(strings.TrimSpace(scheme))]
	return d, ok
}

// ListDrivers returns all registered primary drivers.
func ListDrivers() []ProxyProtocolDriver {
	defaultRegistry.mu.RLock()
	defer defaultRegistry.mu.RUnlock()
	seen := make(map[ProxyProtocolDriver]bool)
	var list []ProxyProtocolDriver
	for _, d := range defaultRegistry.drivers {
		if !seen[d] {
			seen[d] = true
			list = append(list, d)
		}
	}
	return list
}

// ExtractScheme extracts the scheme from a raw URI.
func ExtractScheme(uri string) (string, error) {
	uri = strings.TrimSpace(uri)
	idx := strings.Index(uri, "://")
	if idx == -1 {
		return "", fmt.Errorf("invalid proxy URI: missing scheme delimiter '://'")
	}
	return strings.ToLower(uri[:idx]), nil
}

// Helper utilities shared across proxy drivers.

func parseProxyPort(u *url.URL, defaultPort int) (int, error) {
	if strings.TrimSpace(u.Hostname()) == "" {
		return 0, fmt.Errorf("proxy parse failed: server is required")
	}
	if u.Port() == "" {
		return defaultPort, nil
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port <= 0 || port > 65535 {
		return 0, fmt.Errorf("proxy parse failed: invalid port")
	}
	return port, nil
}

func nameFromURL(u *url.URL) string {
	name := u.Fragment
	if decoded, err := url.QueryUnescape(name); err == nil {
		return strings.TrimSpace(decoded)
	}
	return strings.TrimSpace(name)
}

func queryFlag(q url.Values, keys ...string) bool {
	for _, key := range keys {
		switch strings.ToLower(strings.TrimSpace(q.Get(key))) {
		case "1", "true", "yes", "on":
			return true
		}
	}
	return false
}

func isCertPinningFingerprint(fp string) bool {
	cleaned := strings.ReplaceAll(strings.TrimSpace(fp), ":", "")
	if len(cleaned) != 64 {
		return false
	}
	for _, ch := range cleaned {
		if !strings.ContainsRune("0123456789abcdefABCDEF", ch) {
			return false
		}
	}
	return true
}

func normalizeSSCipher(method string) string {
	if strings.EqualFold(strings.TrimSpace(method), "chacha20-poly1305") {
		return "chacha20-ietf-poly1305"
	}
	return method
}

func normalizeHysteriaSpeed(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if _, err := strconv.Atoi(value); err == nil {
		return value + " Mbps"
	}
	return value
}

func applyNetworkOpts(out map[string]any, network, path, host, mode, serviceName string) {
	network = strings.ToLower(strings.TrimSpace(network))
	if network != "ws" && network != "grpc" && network != "http" && network != "h2" && network != "xhttp" {
		return
	}
	if path == "" {
		path = "/"
	}
	out["network"] = network
	switch network {
	case "ws":
		wsOpts := map[string]any{"path": path}
		if host != "" {
			wsOpts["headers"] = map[string]any{"Host": host}
		}
		out["ws-opts"] = wsOpts
	case "grpc":
		svc := serviceName
		if svc == "" {
			svc = strings.TrimPrefix(path, "/")
		}
		if svc != "" {
			out["grpc-opts"] = map[string]any{"grpc-service-name": svc}
		}
	case "http":
		out["http-opts"] = map[string]any{
			"method":  "GET",
			"path":    []string{path},
			"headers": map[string][]string{"Host": {host}},
		}
	case "h2":
		hosts := []string{}
		if host != "" {
			hosts = []string{host}
		}
		out["h2-opts"] = map[string]any{"path": path, "host": hosts}
	case "xhttp":
		xhttpOpts := map[string]any{"path": path}
		if host != "" {
			xhttpOpts["host"] = host
		}
		if mode != "" {
			xhttpOpts["mode"] = mode
		}
		out["xhttp-opts"] = xhttpOpts
	}
}

func extractNetworkFromOpts(cfg map[string]any, q url.Values) {
	network, _ := cfg["network"].(string)
	network = strings.ToLower(strings.TrimSpace(network))
	if network == "" {
		return
	}
	q.Set("type", network)
	switch network {
	case "ws":
		if ws, ok := cfg["ws-opts"].(map[string]any); ok {
			if path, _ := ws["path"].(string); path != "" {
				q.Set("path", path)
			}
			if headers, ok := ws["headers"].(map[string]any); ok {
				if host, _ := headers["Host"].(string); host != "" {
					q.Set("host", host)
				}
			}
		}
	case "grpc":
		if grpc, ok := cfg["grpc-opts"].(map[string]any); ok {
			if svc, _ := grpc["grpc-service-name"].(string); svc != "" {
				q.Set("serviceName", svc)
			}
		}
	case "http":
		if httpOpts, ok := cfg["http-opts"].(map[string]any); ok {
			if paths, ok := httpOpts["path"].([]string); ok && len(paths) > 0 {
				q.Set("path", paths[0])
			} else if paths, ok := httpOpts["path"].([]any); ok && len(paths) > 0 {
				q.Set("path", fmt.Sprintf("%v", paths[0]))
			}
			if headers, ok := httpOpts["headers"].(map[string][]string); ok {
				if hosts, ok := headers["Host"]; ok && len(hosts) > 0 {
					q.Set("host", hosts[0])
				}
			}
		}
	case "h2":
		if h2, ok := cfg["h2-opts"].(map[string]any); ok {
			if path, _ := h2["path"].(string); path != "" {
				q.Set("path", path)
			}
			if hosts, ok := h2["host"].([]string); ok && len(hosts) > 0 {
				q.Set("host", hosts[0])
			} else if hosts, ok := h2["host"].([]any); ok && len(hosts) > 0 {
				q.Set("host", fmt.Sprintf("%v", hosts[0]))
			}
		}
	case "xhttp":
		if xhttp, ok := cfg["xhttp-opts"].(map[string]any); ok {
			if path, _ := xhttp["path"].(string); path != "" {
				q.Set("path", path)
			}
			if host, _ := xhttp["host"].(string); host != "" {
				q.Set("host", host)
			}
			if mode, _ := xhttp["mode"].(string); mode != "" {
				q.Set("mode", mode)
			}
		}
	}
}

func extractALPN(cfg map[string]any) []string {
	if val, ok := cfg["alpn"]; ok {
		switch v := val.(type) {
		case []string:
			return v
		case []any:
			var res []string
			for _, item := range v {
				if s := fmt.Sprintf("%v", item); s != "" {
					res = append(res, s)
				}
			}
			return res
		case string:
			if v != "" {
				return strings.Split(v, ",")
			}
		}
	}
	return nil
}

func buildStandardURI(scheme, user, server string, port int, query url.Values, name string) string {
	var userinfo *url.Userinfo
	if user != "" {
		if idx := strings.Index(user, ":"); idx != -1 {
			userinfo = url.UserPassword(user[:idx], user[idx+1:])
		} else {
			userinfo = url.User(user)
		}
	}
	hostPort := net.JoinHostPort(server, strconv.Itoa(port))
	u := url.URL{
		Scheme:   scheme,
		User:     userinfo,
		Host:     hostPort,
		Fragment: name,
	}
	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}
	return u.String()
}
