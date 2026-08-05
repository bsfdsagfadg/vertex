package api

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"gopkg.in/yaml.v3"
)

func supportedClashProxyType(typ string) bool {
	switch typ {
	case "ss", "ssr", "socks5", "http", "vmess", "vless", "snell", "trojan", "hysteria", "hysteria2", "wireguard", "tuic", "gost-relay", "ssh", "mieru", "anytls", "sudoku", "masque", "trusttunnel", "openvpn", "tailscale":
		return true
	default:
		return false
	}
}

func looksLikeClashProxyMap(obj map[string]any) bool {
	typ := strings.ToLower(strings.TrimSpace(valueToString(obj["type"])))
	if !supportedClashProxyType(typ) {
		return false
	}
	if typ == "wireguard" {
		return strings.TrimSpace(valueToString(obj["private-key"])) != "" &&
			(strings.TrimSpace(valueToString(obj["server"])) != "" || len(sliceValue(obj["peers"])) > 0)
	}
	return strings.TrimSpace(valueToString(obj["server"])) != "" && intValue(obj["port"]) > 0
}

func clashProxyToURI(attrs map[string]string) string {
	typ := strings.ToLower(strings.TrimSpace(attrs["type"]))
	name := attrs["name"]
	server := attrs["server"]
	port := attrs["port"]

	if server == "" || port == "" {
		return ""
	}

	switch typ {
	case "ss":
		cipher := attrs["cipher"]
		password := attrs["password"]
		if cipher == "" || password == "" {
			return ""
		}
		userinfo := base64.StdEncoding.EncodeToString([]byte(cipher + ":" + password))
		return "ss://" + userinfo + "@" + server + ":" + port + "#" + url.QueryEscape(name)

	case "vmess":
		uuid := attrs["uuid"]
		alterIdStr := attrs["alterId"]
		if alterIdStr == "" {
			alterIdStr = "0"
		}
		alterId, _ := strconv.Atoi(alterIdStr)

		tlsEnabled := false
		if attrs["tls"] == "true" {
			tlsEnabled = true
		}

		vmessJSON := map[string]any{
			"v":    "2",
			"ps":   name,
			"add":  server,
			"port": port,
			"id":   uuid,
			"aid":  alterId,
			"net":  "tcp",
			"type": "none",
			"host": "",
			"path": "",
			"tls":  "",
		}

		if attrs["network"] == "ws" {
			vmessJSON["net"] = "ws"
			if wsOpts, ok := attrs["ws-opts"]; ok {
				path := "/"
				if idx := strings.Index(wsOpts, "path:"); idx != -1 {
					sub := wsOpts[idx+5:]
					if commaIdx := strings.Index(sub, ","); commaIdx != -1 {
						sub = sub[:commaIdx]
					}
					path = strings.Trim(strings.TrimSpace(sub), "\"'{}")
				}
				vmessJSON["path"] = path

				host := ""
				if idx := strings.Index(wsOpts, "Host:"); idx != -1 {
					sub := wsOpts[idx+5:]
					if commaIdx := strings.Index(sub, ","); commaIdx != -1 {
						sub = sub[:commaIdx]
					}
					if braceIdx := strings.Index(sub, "}"); braceIdx != -1 {
						sub = sub[:braceIdx]
					}
					host = strings.Trim(strings.TrimSpace(sub), "\"'{}")
				}
				vmessJSON["host"] = host
			}
		}

		if tlsEnabled {
			vmessJSON["tls"] = "tls"
		}

		jsonBytes, _ := json.Marshal(vmessJSON)
		b64Str := base64.StdEncoding.EncodeToString(jsonBytes)
		return "vmess://" + b64Str

	case "vless":
		uuid := attrs["uuid"]
		if uuid == "" {
			return ""
		}

		query := url.Values{}
		serverName := firstNonEmpty(attrs["servername"], attrs["sni"], server)
		realityOpts := parseInlineYamlObject(attrs["reality-opts"])
		if len(realityOpts) > 0 {
			query.Set("security", "reality")
			if publicKey := realityOpts["public-key"]; publicKey != "" {
				query.Set("pbk", publicKey)
			}
			if shortID := realityOpts["short-id"]; shortID != "" {
				query.Set("sid", shortID)
			}
		} else if isTruthy(attrs["tls"]) {
			query.Set("security", "tls")
		}
		if serverName != "" {
			query.Set("sni", serverName)
		}
		if isTruthy(attrs["skip-cert-verify"]) {
			query.Set("allowInsecure", "1")
		}
		if flow := attrs["flow"]; flow != "" {
			query.Set("flow", flow)
		}
		if fp := attrs["client-fingerprint"]; fp != "" {
			query.Set("fp", fp)
		}
		if network := strings.ToLower(strings.TrimSpace(attrs["network"])); network != "" {
			query.Set("type", network)
			switch network {
			case "ws":
				wsOpts := parseInlineYamlObject(attrs["ws-opts"])
				if path := wsOpts["path"]; path != "" {
					query.Set("path", path)
				}
				headers := parseInlineYamlObject(wsOpts["headers"])
				if host := firstNonEmpty(headers["Host"], headers["host"]); host != "" {
					query.Set("host", host)
				}
			case "grpc":
				grpcOpts := parseInlineYamlObject(attrs["grpc-opts"])
				if serviceName := firstNonEmpty(grpcOpts["grpc-service-name"], grpcOpts["serviceName"]); serviceName != "" {
					query.Set("serviceName", serviceName)
				}
			}
		}
		return buildProxyURI("vless", uuid, server, port, name, query)

	case "trojan":
		password := attrs["password"]
		if password == "" {
			return ""
		}

		query := url.Values{}
		if sni := firstNonEmpty(attrs["sni"], attrs["servername"], server); sni != "" {
			query.Set("sni", sni)
		}
		if isTruthy(attrs["skip-cert-verify"]) {
			query.Set("allowInsecure", "1")
		}
		if fp := attrs["client-fingerprint"]; fp != "" {
			query.Set("fp", fp)
		}
		if network := strings.ToLower(strings.TrimSpace(attrs["network"])); network != "" {
			query.Set("type", network)
			switch network {
			case "ws":
				wsOpts := parseInlineYamlObject(attrs["ws-opts"])
				if path := wsOpts["path"]; path != "" {
					query.Set("path", path)
				}
				headers := parseInlineYamlObject(wsOpts["headers"])
				if host := firstNonEmpty(headers["Host"], headers["host"]); host != "" {
					query.Set("host", host)
				}
			case "grpc":
				grpcOpts := parseInlineYamlObject(attrs["grpc-opts"])
				if serviceName := firstNonEmpty(grpcOpts["grpc-service-name"], grpcOpts["serviceName"]); serviceName != "" {
					query.Set("serviceName", serviceName)
				}
			}
		}
		return buildProxyURI("trojan", password, server, port, name, query)

	case "hysteria2", "hy2":
		password := attrs["password"]
		if password == "" {
			return ""
		}

		query := url.Values{}
		if sni := firstNonEmpty(attrs["sni"], attrs["servername"], server); sni != "" {
			query.Set("sni", sni)
		}
		if isTruthy(attrs["skip-cert-verify"]) {
			query.Set("insecure", "1")
		}
		if ports := firstNonEmpty(attrs["ports"], attrs["mport"]); ports != "" {
			query.Set("ports", ports)
		}
		if obfs := attrs["obfs"]; obfs != "" {
			query.Set("obfs", obfs)
		}
		if obfsPassword := attrs["obfs-password"]; obfsPassword != "" {
			query.Set("obfs-password", obfsPassword)
		}
		if fp := firstNonEmpty(attrs["client-fingerprint"], attrs["fingerprint"]); fp != "" {
			query.Set("fp", fp)
		}
		return buildProxyURI("hy2", password, server, port, name, query)
	}

	return ""
}

func parseClashYAMLToNodes(yamlText string) []nodes.Node {
	yamlText = strings.TrimSpace(yamlText)
	if yamlText == "" {
		return nil
	}

	if imported := parseStructuredClashYAMLNodes(yamlText); len(imported) > 0 {
		return imported
	}
	return parseInlineClashYAMLNodes(yamlText)
}

func parseStructuredClashYAMLNodes(yamlText string) []nodes.Node {
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal([]byte(yamlText), &doc); err == nil && len(doc.Proxies) > 0 {
		return buildClashNodes(doc.Proxies)
	}

	var proxies []map[string]any
	if err := yaml.Unmarshal([]byte(yamlText), &proxies); err == nil && len(proxies) > 0 {
		return buildClashNodes(proxies)
	}

	var proxy map[string]any
	if err := yaml.Unmarshal([]byte(yamlText), &proxy); err == nil && len(proxy) > 0 {
		if normalized, ok := normalizeYAMLValue(proxy).(map[string]any); ok && looksLikeClashProxyMap(normalized) {
			if node, ok2 := buildClashNode(normalized); ok2 {
				return []nodes.Node{node}
			}
		}
	}

	return nil
}

func parseInlineClashYAMLNodes(yamlText string) []nodes.Node {
	var imported []nodes.Node
	lines := strings.Split(yamlText, "\n")
	inProxies := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "proxies:") {
			inProxies = true
			continue
		}
		if inProxies && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && strings.Contains(trimmed, ":") {
			inProxies = false
		}
		if !inProxies || !strings.HasPrefix(trimmed, "-") {
			continue
		}

		inline := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
		if !strings.HasPrefix(inline, "{") || !strings.HasSuffix(inline, "}") {
			continue
		}

		var proxy map[string]any
		if err := yaml.Unmarshal([]byte(inline), &proxy); err == nil {
			if node, ok := buildClashNode(proxy); ok {
				imported = append(imported, node)
				continue
			}
		}

		cleaned := inline[1 : len(inline)-1]
		attrs := parseInlineYamlAttrs(cleaned)
		if uri := clashProxyToURI(attrs); uri != "" {
			if node, ok := parseImportedNodeLine(uri); ok {
				imported = append(imported, node)
			}
		}
	}

	return imported
}

func buildClashNodes(proxies []map[string]any) []nodes.Node {
	imported := make([]nodes.Node, 0, len(proxies))
	for _, proxy := range proxies {
		if node, ok := buildClashNode(proxy); ok {
			imported = append(imported, node)
		}
	}
	return imported
}

func buildClashNode(proxy map[string]any) (nodes.Node, bool) {
	normalized, ok := normalizeYAMLValue(proxy).(map[string]any)
	if !ok || len(normalized) == 0 {
		return nodes.Node{}, false
	}
	if !looksLikeClashProxyMap(normalized) {
		return nodes.Node{}, false
	}

	rawURI := proxyMapToURI(normalized)
	if rawURI == "" {
		return nodes.Node{}, false
	}
	return parseImportedNodeLine(rawURI)
}
