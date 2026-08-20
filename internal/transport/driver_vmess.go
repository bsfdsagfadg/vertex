package transport

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/strutil"
)


// VmessDriver handles VMess protocol URIs.
type VmessDriver struct{}

func (d *VmessDriver) Scheme() string {
	return "vmess"
}

func (d *VmessDriver) ParseURI(rawURI string) (map[string]any, error) {
	b64Str := rawURI
	if strings.HasPrefix(strings.ToLower(b64Str), "vmess://") {
		b64Str = b64Str[8:]
	}
	if idx := strings.Index(b64Str, "?"); idx != -1 {
		b64Str = b64Str[:idx]
	}
	if idx := strings.Index(b64Str, "#"); idx != -1 {
		b64Str = b64Str[:idx]
	}

	b, err := base64.StdEncoding.DecodeString(strutil.PadB64(b64Str))
	if err != nil {
		if bDecoded, errLoose := strutil.DecodeBase64Loose(b64Str); errLoose == nil {
			b = bDecoded
		} else {
			return nil, fmt.Errorf("vmess base64 decode error: %w", err)
		}
	}

	var payload map[string]any
	if err := json.Unmarshal(b, &payload); err != nil {
		return nil, fmt.Errorf("vmess json unmarshal error: %w", err)
	}

	portStr := fmt.Sprintf("%v", payload["port"])
	port, _ := strconv.Atoi(portStr)

	cipher := "auto"
	if scy, ok := payload["scy"].(string); ok && strings.TrimSpace(scy) != "" && !strings.EqualFold(scy, "auto") {
		cipher = strings.TrimSpace(scy)
	}

	out := map[string]any{
		"name":   payload["ps"],
		"type":   "vmess",
		"server": payload["add"],
		"port":   port,
		"uuid":   payload["id"],
		"cipher": cipher,
	}

	if aidVal, ok := payload["aid"]; ok {
		switch v := aidVal.(type) {
		case float64:
			out["alterId"] = int(v)
		case int:
			out["alterId"] = v
		case string:
			if n, err := strconv.Atoi(v); err == nil {
				out["alterId"] = n
			}
		}
	}

	var tlsOn bool
	switch value := payload["tls"].(type) {
	case string:
		tlsOn = strings.EqualFold(value, "tls") || strings.EqualFold(value, "true") || value == "1"
	case bool:
		tlsOn = value
	case float64:
		tlsOn = value != 0
	case int:
		tlsOn = value != 0
	}

	if tlsOn {
		host, _ := payload["host"].(string)
		sni, _ := payload["sni"].(string)
		if sni == "" {
			sni = host
		}
		if sni == "" {
			sni, _ = payload["add"].(string)
		}
		out["tls"] = true
		out["sni"] = sni
		out["servername"] = sni
		if fp, ok := payload["fp"].(string); ok && fp != "" {
			out["client-fingerprint"] = fp
		} else {
			out["client-fingerprint"] = "chrome"
		}
		if alpn, ok := payload["alpn"].(string); ok && alpn != "" {
			out["alpn"] = strings.Split(alpn, ",")
		}
		if insecure, ok := payload["skip-cert-verify"].(bool); ok {
			out["skip-cert-verify"] = insecure
		} else if allowInsecure, ok := payload["allowInsecure"].(string); ok && (allowInsecure == "1" || strings.EqualFold(allowInsecure, "true")) {
			out["skip-cert-verify"] = true
		} else {
			out["skip-cert-verify"] = false
		}
	}

	netType, _ := payload["net"].(string)
	netType = strings.ToLower(strings.TrimSpace(netType))
	if netType != "" && netType != "tcp" {
		path, _ := payload["path"].(string)
		host, _ := payload["host"].(string)

		out["network"] = netType

		switch netType {
		case "ws":
			out["ws-opts"] = map[string]any{
				"path": path,
				"headers": map[string]any{
					"Host": host,
				},
			}
		case "grpc":
			out["grpc-opts"] = map[string]any{
				"grpc-service-name": path,
			}
		case "http":
			hPath := path
			if hPath == "" {
				hPath = "/"
			}
			out["http-opts"] = map[string]any{
				"method":  "GET",
				"path":    []string{hPath},
				"headers": map[string][]string{"Host": {host}},
			}
		case "h2":
			hPath := path
			if hPath == "" {
				hPath = "/"
			}
			hosts := []string{}
			if host != "" {
				hosts = []string{host}
			}
			out["h2-opts"] = map[string]any{"path": hPath, "host": hosts}
		}
	}

	return out, nil
}

func (d *VmessDriver) FormatURI(cfg map[string]any) (string, error) {
	server, _ := cfg["server"].(string)
	if server == "" {
		return "", fmt.Errorf("vmess format failed: missing server")
	}
	port := strutil.ToInt(cfg["port"], 443)
	uuid, _ := cfg["uuid"].(string)
	name, _ := cfg["name"].(string)
	cipher, _ := cfg["cipher"].(string)
	if cipher == "" {
		cipher = "auto"
	}
	alterID := strutil.ToInt(cfg["alterId"], 0)

	payload := map[string]any{
		"v":    "2",
		"ps":   name,
		"add":  server,
		"port": strconv.Itoa(port),
		"id":   uuid,
		"aid":  strconv.Itoa(alterID),
		"scy":  cipher,
		"net":  "tcp",
	}

	if tls, _ := cfg["tls"].(bool); tls {
		payload["tls"] = "tls"
		if sni, _ := cfg["sni"].(string); sni != "" {
			payload["sni"] = sni
		}
		if fp, _ := cfg["client-fingerprint"].(string); fp != "" {
			payload["fp"] = fp
		}
		if alpn := extractALPN(cfg); len(alpn) > 0 {
			payload["alpn"] = strings.Join(alpn, ",")
		}
		if skip, _ := cfg["skip-cert-verify"].(bool); skip {
			payload["allowInsecure"] = "1"
		}
	}

	if network, _ := cfg["network"].(string); network != "" && network != "tcp" {
		payload["net"] = network
		switch network {
		case "ws":
			if ws, ok := cfg["ws-opts"].(map[string]any); ok {
				if p, _ := ws["path"].(string); p != "" {
					payload["path"] = p
				}
				if h, ok := ws["headers"].(map[string]any); ok {
					if host, _ := h["Host"].(string); host != "" {
						payload["host"] = host
					}
				}
			}
		case "grpc":
			if grpc, ok := cfg["grpc-opts"].(map[string]any); ok {
				if svc, _ := grpc["grpc-service-name"].(string); svc != "" {
					payload["path"] = svc
				}
			}
		case "http":
			if httpOpts, ok := cfg["http-opts"].(map[string]any); ok {
				if paths, ok := httpOpts["path"].([]string); ok && len(paths) > 0 {
					payload["path"] = paths[0]
				}
				if headers, ok := httpOpts["headers"].(map[string][]string); ok {
					if hosts, ok := headers["Host"]; ok && len(hosts) > 0 {
						payload["host"] = hosts[0]
					}
				}
			}
		case "h2":
			if h2, ok := cfg["h2-opts"].(map[string]any); ok {
				if p, _ := h2["path"].(string); p != "" {
					payload["path"] = p
				}
				if hosts, ok := h2["host"].([]string); ok && len(hosts) > 0 {
					payload["host"] = hosts[0]
				}
			}
		}
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("vmess marshal json error: %w", err)
	}

	return "vmess://" + base64.StdEncoding.EncodeToString(data), nil
}
