package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/domain"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/strutil"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

const subscriptionFetchUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// parseImportedNodes parses text into standard domain.Node slices using the transport package.
func parseImportedNodes(text string) []nodes.Node {
	domainNodes := ParseImportedNodesToDomain(text)
	return legacyNodesFromDomain(domainNodes)
}

// ParseImportedNodesToDomain parses arbitrary imported text (Base64, Clash YAML, JSON outbounds, or URI lines)
// into a slice of domain.Node models.
func ParseImportedNodesToDomain(text string) []domain.Node {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	normalized := maybeDecodeSubscriptionText(text)

	// 1. Try Clash YAML (with `proxies:` top-level or list of proxy maps)
	if proxies, err := transport.ParseClashYAMLProxies([]byte(normalized)); err == nil && len(proxies) > 0 {
		return domainNodesFromProxyMaps(proxies)
	}

	// 2. Try JSON formats (outbounds, servers, or proxy list)
	if jsonNodes := parseJSONImportedNodes(normalized); len(jsonNodes) > 0 {
		return jsonNodes
	}

	// 3. Try line-by-line parsing (URI strings, inline Clash YAML, or v2rayn://)
	var imported []domain.Node
	for _, line := range strings.Split(normalized, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if node, ok := parseSingleNodeLine(trimmed); ok {
			imported = append(imported, node)
		}
	}
	return imported
}

func maybeDecodeSubscriptionText(text string) string {
	b, err := strutil.DecodeBase64Loose(text)
	if err != nil {
		return text
	}

	decoded := strings.TrimSpace(string(b))
	if decoded == "" {
		return text
	}
	if strings.Contains(decoded, "proxies:") || hasImportableNodeLine(decoded) || len(parseJSONImportedNodes(decoded)) > 0 {
		return decoded
	}
	return text
}

func hasImportableNodeLine(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if _, ok := parseSingleNodeLine(strings.TrimSpace(line)); ok {
			return true
		}
	}
	return false
}

func parseSingleNodeLine(line string) (domain.Node, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return domain.Node{}, false
	}

	// Check if it's an inline Clash YAML item: `- { name: ..., type: ... }` or `{ name: ... }`
	if strings.HasPrefix(line, "-") || strings.HasPrefix(line, "{") {
		cleanLine := strings.TrimSpace(strings.TrimPrefix(line, "-"))
		if proxyMap, err := transport.ParseClashInline(cleanLine); err == nil && len(proxyMap) > 0 {
			if node, ok := buildDomainNodeFromProxyMap(proxyMap); ok {
				return node, true
			}
		}
	}

	// Check if it's a v2rayn:// line
	if strings.HasPrefix(strings.ToLower(line), "v2rayn://") {
		if node, ok := parseV2RayNNodeLine(line); ok {
			return node, true
		}
	}

	// Standard proxy URI: vmess://, vless://, ss://, ssr://, hy2://, hysteria2://, trojan://, tuic://, socks5://, http://, clash://
	out, err := transport.ParseURI(line)
	if err != nil {
		return domain.Node{}, false
	}

	nodeType := strings.TrimSpace(strutil.ToString(out["type"]))
	if nodeType == "" {
		return domain.Node{}, false
	}

	nodeName := extractImportedNodeName(line, out)
	if nodeName == "" {
		nodeName = line[:min(len(line), 40)]
	}
	return domain.Node{Type: nodeType, Name: nodeName, RawURI: line, Disabled: false}, true
}

func extractImportedNodeName(raw string, out map[string]any) string {
	if name := strings.TrimSpace(strutil.ToString(out["name"])); name != "" {
		return name
	}

	if strings.HasPrefix(raw, "vmess://") {
		b64Str := raw[8:]
		if idx := strings.Index(b64Str, "?"); idx != -1 {
			b64Str = b64Str[:idx]
		}
		if idx := strings.Index(b64Str, "#"); idx != -1 {
			b64Str = b64Str[:idx]
		}
		if b, err := strutil.DecodeBase64Loose(b64Str); err == nil {
			var d map[string]any
			if errUnm := json.Unmarshal(b, &d); errUnm == nil {
				if ps, ok := d["ps"].(string); ok {
					return strings.TrimSpace(ps)
				}
			}
		}
	}

	if idx := strings.Index(raw, "#"); idx != -1 {
		escapedName := raw[idx+1:]
		if dec, err := url.QueryUnescape(escapedName); err == nil {
			return strings.TrimSpace(dec)
		}
		return strings.TrimSpace(escapedName)
	}

	return ""
}

func domainNodesFromProxyMaps(proxies []map[string]any) []domain.Node {
	var list []domain.Node
	for _, p := range proxies {
		if node, ok := buildDomainNodeFromProxyMap(p); ok {
			list = append(list, node)
		}
	}
	return list
}

func buildDomainNodeFromProxyMap(proxy map[string]any) (domain.Node, bool) {
	if len(proxy) == 0 {
		return domain.Node{}, false
	}
	nodeType := strings.ToLower(strings.TrimSpace(strutil.ToString(proxy["type"])))
	if nodeType == "" {
		return domain.Node{}, false
	}
	if !looksLikeValidClashProxy(nodeType, proxy) {
		return domain.Node{}, false
	}
	name := strings.TrimSpace(strutil.ToString(proxy["name"]))

	// Format back into a URI using transport drivers or clash:// URI
	rawURI, err := transport.FormatURI(proxy)
	if err != nil || rawURI == "" {
		// Fallback to clash:// format
		data, err := json.Marshal(proxy)
		if err != nil {
			return domain.Node{}, false
		}
		rawURI = "clash://" + base64.StdEncoding.EncodeToString(data)
	}

	if name == "" {
		name = extractImportedNodeName(rawURI, proxy)
	}
	if name == "" {
		name = fmt.Sprintf("%s-%s", nodeType, strutil.ToString(proxy["server"]))
	}

	return domain.Node{
		Type:     nodeType,
		Name:     name,
		RawURI:   rawURI,
		Disabled: false,
	}, true
}

func looksLikeValidClashProxy(typ string, obj map[string]any) bool {
	switch typ {
	case "ss", "ssr", "socks5", "socks", "http", "https", "vmess", "vless", "snell", "trojan", "hysteria", "hysteria2", "hy2", "wireguard", "tuic", "gost-relay", "ssh", "mieru", "anytls", "sudoku", "masque", "trusttunnel", "openvpn", "tailscale", "clash":
	default:
		return false
	}

	server := strings.TrimSpace(strutil.ToString(obj["server"]))
	port := strutil.ToInt(obj["port"], 0)
	hasEndpoint := server != "" && port > 0

	switch typ {
	case "tuic":
		hasToken := strings.TrimSpace(strutil.ToString(obj["token"])) != ""
		hasUserPassword := strings.TrimSpace(strutil.ToString(obj["uuid"])) != "" && strings.TrimSpace(strutil.ToString(obj["password"])) != ""
		return hasEndpoint && (hasToken || hasUserPassword)
	case "wireguard":
		if strings.TrimSpace(strutil.ToString(obj["private-key"])) == "" ||
			(strings.TrimSpace(strutil.ToString(obj["ip"])) == "" && strings.TrimSpace(strutil.ToString(obj["ipv6"])) == "") {
			return false
		}
		peers, _ := obj["peers"].([]any)
		if len(peers) == 0 {
			return hasEndpoint && strings.TrimSpace(strutil.ToString(obj["public-key"])) != ""
		}
		for _, rawPeer := range peers {
			if peer, ok := rawPeer.(map[string]any); ok {
				if strings.TrimSpace(strutil.ToString(peer["server"])) == "" || strutil.ToInt(peer["port"], 0) <= 0 ||
					strings.TrimSpace(strutil.ToString(peer["public-key"])) == "" {
					return false
				}
			}
		}
		return true
	case "clash":
		return true
	default:
		return hasEndpoint
	}
}

func parseJSONImportedNodes(text string) []domain.Node {
	var raw any
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return nil
	}

	normalized := transport.NormalizeYAMLValue(raw)
	if obj, ok := normalized.(map[string]any); ok {
		if proxies, ok := obj["proxies"].([]any); ok {
			return parseAnyProxySlice(proxies)
		}
		if outbounds, ok := obj["outbounds"].([]any); ok {
			return parseAnyOutboundsSlice(outbounds)
		}
		if servers, ok := obj["servers"].([]any); ok {
			return parseSIP008Servers(servers)
		}
		if node, ok := buildDomainNodeFromProxyMap(obj); ok {
			return []domain.Node{node}
		}
	}
	if list, ok := normalized.([]any); ok {
		return parseAnyProxySlice(list)
	}
	return nil
}

func parseAnyProxySlice(items []any) []domain.Node {
	var list []domain.Node
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			if node, ok2 := buildDomainNodeFromProxyMap(m); ok2 {
				list = append(list, node)
			}
		}
	}
	return list
}

func parseAnyOutboundsSlice(outbounds []any) []domain.Node {
	var list []domain.Node
	for _, ob := range outbounds {
		m, ok := ob.(map[string]any)
		if !ok {
			continue
		}
		protocol := strings.ToLower(strings.TrimSpace(strutil.ToString(m["protocol"])))
		if protocol == "" {
			continue
		}
		tag := strutil.ToString(m["tag"])
		settings, _ := m["settings"].(map[string]any)
		streamSettings, _ := m["streamSettings"].(map[string]any)

		proxy := map[string]any{
			"name": tag,
			"type": protocol,
		}
		if vnextList, ok := settings["vnext"].([]any); ok && len(vnextList) > 0 {
			if vnext, ok := vnextList[0].(map[string]any); ok {
				proxy["server"] = vnext["address"]
				proxy["port"] = vnext["port"]
				if users, ok := vnext["users"].([]any); ok && len(users) > 0 {
					if user, ok := users[0].(map[string]any); ok {
						proxy["uuid"] = user["id"]
						if protocol == "vmess" {
							proxy["cipher"] = strutil.FirstNonEmpty(strutil.ToString(user["security"]), "auto")
							proxy["alterId"] = user["alterId"]
						}
					}
				}
			}
		}
		if serversList, ok := settings["servers"].([]any); ok && len(serversList) > 0 {
			if srv, ok := serversList[0].(map[string]any); ok {
				proxy["server"] = srv["address"]
				proxy["port"] = srv["port"]
				if password, ok := srv["password"]; ok {
					proxy["password"] = password
				}
				if method, ok := srv["method"]; ok {
					proxy["cipher"] = method
				}
			}
		}

		if streamSettings != nil {
			if netType, ok := streamSettings["network"].(string); ok {
				proxy["network"] = netType
				if netType == "ws" {
					if ws, ok := streamSettings["wsSettings"].(map[string]any); ok {
						wsOpts := map[string]any{}
						if path, ok := ws["path"].(string); ok {
							wsOpts["path"] = path
						}
						if headers, ok := ws["headers"].(map[string]any); ok {
							wsOpts["headers"] = headers
						}
						proxy["ws-opts"] = wsOpts
					}
				}
			}
			if sec, ok := streamSettings["security"].(string); ok {
				if sec == "tls" {
					proxy["tls"] = true
					if tls, ok := streamSettings["tlsSettings"].(map[string]any); ok {
						if sName, ok := tls["serverName"].(string); ok {
							proxy["servername"] = sName
							proxy["sni"] = sName
						}
						if fp, ok := tls["fingerprint"].(string); ok {
							proxy["client-fingerprint"] = fp
						}
						if allow, ok := tls["allowInsecure"].(bool); ok && allow {
							proxy["skip-cert-verify"] = true
						}
					}
				}
			}
		}

		if node, ok2 := buildDomainNodeFromProxyMap(proxy); ok2 {
			list = append(list, node)
		}
	}
	return list
}

func parseSIP008Servers(servers []any) []domain.Node {
	var list []domain.Node
	for _, s := range servers {
		m, ok := s.(map[string]any)
		if !ok {
			continue
		}
		server := strutil.ToString(m["server"])
		port := m["server_port"]
		method := strutil.ToString(m["method"])
		password := strutil.ToString(m["password"])
		if server == "" || method == "" || password == "" {
			continue
		}
		name := strutil.ToString(m["remarks"])
		if name == "" {
			name = strutil.ToString(m["name"])
		}
		proxy := map[string]any{
			"name":     name,
			"type":     "ss",
			"server":   server,
			"port":     port,
			"cipher":   method,
			"password": password,
			"udp":      true,
		}
		if node, ok2 := buildDomainNodeFromProxyMap(proxy); ok2 {
			list = append(list, node)
		}
	}
	return list
}

func parseV2RayNNodeLine(line string) (domain.Node, bool) {
	raw := strings.TrimSpace(line)
	if !strings.HasPrefix(strings.ToLower(raw), "v2rayn://") {
		return domain.Node{}, false
	}

	body := raw[len("v2rayn://"):]
	slash := strings.IndexByte(body, '/')
	if slash <= 0 || slash+1 >= len(body) {
		return domain.Node{}, false
	}

	nodeType := strings.ToLower(body[:slash])
	encoded := body[slash+1:]
	decoded, err := strutil.DecodeBase64Loose(encoded)
	if err != nil {
		return domain.Node{}, false
	}

	var obj map[string]any
	if errUnm := json.Unmarshal(decoded, &obj); errUnm != nil {
		return domain.Node{}, false
	}

	proxy := map[string]any{
		"name":   strutil.FirstNonEmpty(strutil.ToString(obj["Remarks"]), strutil.ToString(obj["Name"])),
		"type":   nodeType,
		"server": obj["Address"],
		"port":   obj["Port"],
		"uuid":   obj["Password"],
	}
	if obj["StreamSecurity"] == "tls" {
		proxy["tls"] = true
		if sni := strutil.ToString(obj["Sni"]); sni != "" {
			proxy["sni"] = sni
			proxy["servername"] = sni
		}
		if fp := strutil.ToString(obj["Fingerprint"]); fp != "" {
			proxy["client-fingerprint"] = fp
		}
	}
	if net, ok := obj["Network"].(string); ok && net == "ws" {
		proxy["network"] = "ws"
		if transportExtra, ok := obj["TransportExtraObj"].(map[string]any); ok {
			wsOpts := map[string]any{}
			if p, ok := transportExtra["Path"].(string); ok {
				wsOpts["path"] = p
			}
			if h, ok := transportExtra["Host"].(string); ok {
				wsOpts["headers"] = map[string]any{"Host": h}
			}
			proxy["ws-opts"] = wsOpts
		}
	}
	return buildDomainNodeFromProxyMap(proxy)
}

func legacyNodesFromDomain(domainList []domain.Node) []nodes.Node {
	out := make([]nodes.Node, len(domainList))
	for i, n := range domainList {
		out[i] = nodes.Node{
			Type:     n.Type,
			Name:     n.Name,
			RawURI:   n.RawURI,
			Disabled: n.Disabled,
		}
	}
	return out
}

func (adm *AdminHandler) adminImportNodes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text    string `json:"text"`
		Replace bool   `json:"replace"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	log.Printf("[Admin] [ImportNodes] 收到优选节点文件导入请求, 替换模式: %v", body.Replace)

	newDomainNodes := ParseImportedNodesToDomain(strings.TrimSpace(body.Text))
	log.Printf("[Admin] [ImportNodes] 正在合并导入的新节点数量: %d", len(newDomainNodes))

	if adm.nodeRepo != nil {
		err := adm.nodeRepo.UpsertNodesWithSource(
			r.Context(),
			newDomainNodes,
			domain.NodeSource{Type: domain.SourceManual},
			body.Replace,
		)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, adminErr("导入节点失败: "+err.Error()))
			return
		}
	}
	legacyNodes := legacyNodesFromDomain(newDomainNodes)
	_ = nodes.ImportManualNodes(legacyNodes, body.Replace)

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": len(newDomainNodes)})
}

func (adm *AdminHandler) adminImportNodesJson(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text    string `json:"text"`
		Replace bool   `json:"replace"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	log.Printf("[Admin] [ImportNodesJson] 收到旧版 nodes.json 导入请求, 替换模式: %v", body.Replace)

	var d struct {
		Nodes []domain.Node `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(body.Text), &d); err != nil {
		writeJSON(w, http.StatusBadRequest, adminErr("JSON 解析失败: "+err.Error()))
		return
	}

	log.Printf("[Admin] [ImportNodesJson] 正在合并导入的新节点数量: %d", len(d.Nodes))
	if adm.nodeRepo != nil {
		err := adm.nodeRepo.UpsertNodesWithSource(
			r.Context(),
			d.Nodes,
			domain.NodeSource{Type: domain.SourceManual},
			body.Replace,
		)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, adminErr("导入旧版节点失败: "+err.Error()))
			return
		}
	}
	legacyNodes := legacyNodesFromDomain(d.Nodes)
	_ = nodes.ImportManualNodes(legacyNodes, body.Replace)

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": len(d.Nodes)})
}
