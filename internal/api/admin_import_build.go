package api

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/transport"
	"gopkg.in/yaml.v3"
)

func applyCommonImportedProxyFields(proxy map[string]any, obj map[string]any) {
	if sni := strings.TrimSpace(valueToString(obj["Sni"])); sni != "" {
		proxy["sni"] = sni
		proxy["servername"] = sni
	}
	if fp := strings.TrimSpace(valueToString(obj["Fingerprint"])); fp != "" {
		proxy["client-fingerprint"] = fp
		proxy["fingerprint"] = fp
	}
	if importedAllowInsecure(obj["AllowInsecure"]) {
		proxy["skip-cert-verify"] = true
	}
	if alpn := splitCSV(valueToString(obj["Alpn"])); len(alpn) > 0 {
		proxy["alpn"] = alpn
	}
	if cert := strings.TrimSpace(valueToString(obj["Cert"])); cert != "" {
		proxy["certificate"] = cert
	}
	if privateKey := strings.TrimSpace(valueToString(obj["PrivateKey"])); privateKey != "" {
		proxy["private-key"] = privateKey
	}
}

func applyTransportExtras(proxy map[string]any, obj map[string]any, transport map[string]any) {
	network := normalizeImportedNetwork(valueToString(obj["Network"]))
	if network == "" {
		return
	}

	switch network {
	case "ws":
		proxy["network"] = "ws"
		wsOpts := map[string]any{}
		if path := strings.TrimSpace(valueToString(transport["Path"])); path != "" {
			wsOpts["path"] = path
		}
		if host := strings.TrimSpace(valueToString(transport["Host"])); host != "" {
			wsOpts["headers"] = map[string]any{"Host": host}
		}
		if len(wsOpts) > 0 {
			proxy["ws-opts"] = wsOpts
		}
	case "grpc":
		proxy["network"] = "grpc"
		grpcOpts := map[string]any{}
		if serviceName := strings.TrimSpace(valueToString(transport["GrpcServiceName"])); serviceName != "" {
			grpcOpts["grpc-service-name"] = serviceName
		}
		if len(grpcOpts) > 0 {
			proxy["grpc-opts"] = grpcOpts
		}
	case "http", "h2":
		proxy["network"] = "http"
		httpOpts := map[string]any{}
		if path := strings.TrimSpace(valueToString(transport["Path"])); path != "" {
			httpOpts["path"] = []string{path}
		}
		if host := strings.TrimSpace(valueToString(transport["Host"])); host != "" {
			httpOpts["headers"] = map[string][]string{"Host": []string{host}}
		}
		if len(httpOpts) > 0 {
			httpOpts["method"] = "GET"
			proxy["http-opts"] = httpOpts
		}
	case "xhttp":
		proxy["network"] = "xhttp"
		xhttpOpts := map[string]any{}
		if path := strings.TrimSpace(valueToString(transport["Path"])); path != "" {
			xhttpOpts["path"] = path
		}
		if host := strings.TrimSpace(valueToString(transport["Host"])); host != "" {
			xhttpOpts["host"] = host
		}
		if mode := strings.TrimSpace(valueToString(transport["XhttpMode"])); mode != "" {
			xhttpOpts["mode"] = mode
		}
		if headers := parseJSONMapString(valueToString(transport["XhttpExtra"])); len(headers) > 0 {
			xhttpOpts["extra"] = headers
		}
		if len(xhttpOpts) > 0 {
			proxy["xhttp-opts"] = xhttpOpts
		}
	default:
		proxy["network"] = network
	}
}

func applyImportedTLSFields(proxy map[string]any, obj map[string]any) {
	streamSecurity := strings.ToLower(strings.TrimSpace(valueToString(obj["StreamSecurity"])))
	switch streamSecurity {
	case "tls":
		proxy["tls"] = true
	case "reality":
		proxy["tls"] = true
		proxy["reality-opts"] = map[string]any{
			"public-key": strings.TrimSpace(valueToString(obj["PublicKey"])),
			"short-id":   strings.TrimSpace(valueToString(obj["ShortId"])),
		}
	}
}

func buildClashURI(proxy map[string]any) string {
	body, err := json.Marshal(proxy)
	if err != nil {
		return ""
	}
	return "clash://" + base64.StdEncoding.EncodeToString(body)
}

func v2rayNConfigType(v any) int {
	switch x := v.(type) {
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "vmess":
			return 1
		case "shadowsocks", "ss":
			return 3
		case "socks", "socks5":
			return 4
		case "vless":
			return 5
		case "trojan":
			return 6
		case "hysteria2", "hy2":
			return 7
		case "tuic":
			return 8
		case "wireguard":
			return 9
		case "http":
			return 10
		case "anytls":
			return 11
		case "naive":
			return 12
		default:
			return intValue(x)
		}
	default:
		return intValue(v)
	}
}

func buildImportedProxyFromV2RayNProfile(obj map[string]any) map[string]any {
	cfgType := v2rayNConfigType(obj["ConfigType"])
	if cfgType == 0 {
		return nil
	}

	name := firstNonEmpty(valueToString(obj["Remarks"]), valueToString(obj["Name"]))
	server := strings.TrimSpace(valueToString(obj["Address"]))
	port := intValue(obj["Port"])
	password := strings.TrimSpace(valueToString(obj["Password"]))
	username := strings.TrimSpace(valueToString(obj["Username"]))
	proto := nestedObject(obj, "ProtoExtraObj", "ProtoExtra")
	transportExtra := nestedObject(obj, "TransportExtraObj", "TransportExtra")

	switch cfgType {
	case 1:
		if server == "" || port == 0 || password == "" {
			return nil
		}
		proxy := map[string]any{
			"name":    name,
			"type":    "vmess",
			"server":  server,
			"port":    port,
			"uuid":    password,
			"cipher":  firstNonEmpty(valueToString(proto["VmessSecurity"]), "auto"),
			"alterId": intValue(proto["AlterId"]),
			"udp":     true,
		}
		applyCommonImportedProxyFields(proxy, obj)
		applyImportedTLSFields(proxy, obj)
		applyTransportExtras(proxy, obj, transportExtra)
		return proxy
	case 3:
		method := strings.TrimSpace(valueToString(proto["SsMethod"]))
		if server == "" || port == 0 || password == "" || method == "" {
			return nil
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
		return proxy
	case 4:
		if server == "" || port == 0 {
			return nil
		}
		proxy := map[string]any{
			"name":     name,
			"type":     "socks5",
			"server":   server,
			"port":     port,
			"username": username,
			"password": password,
			"udp":      true,
		}
		return proxy
	case 5:
		if server == "" || port == 0 || password == "" {
			return nil
		}
		proxy := map[string]any{
			"name":       name,
			"type":       "vless",
			"server":     server,
			"port":       port,
			"uuid":       password,
			"encryption": firstNonEmpty(valueToString(proto["VlessEncryption"]), "none"),
			"udp":        true,
		}
		if flow := strings.TrimSpace(valueToString(proto["Flow"])); flow != "" {
			proxy["flow"] = flow
		}
		applyCommonImportedProxyFields(proxy, obj)
		applyImportedTLSFields(proxy, obj)
		applyTransportExtras(proxy, obj, transportExtra)
		return proxy
	case 6:
		if server == "" || port == 0 || password == "" {
			return nil
		}
		proxy := map[string]any{
			"name":     name,
			"type":     "trojan",
			"server":   server,
			"port":     port,
			"password": password,
			"udp":      true,
		}
		applyCommonImportedProxyFields(proxy, obj)
		applyImportedTLSFields(proxy, obj)
		applyTransportExtras(proxy, obj, transportExtra)
		return proxy
	case 7:
		if server == "" || port == 0 || password == "" {
			return nil
		}
		proxy := map[string]any{
			"name":     name,
			"type":     "hysteria2",
			"server":   server,
			"port":     port,
			"password": password,
			"udp":      true,
		}
		if ports := strings.TrimSpace(firstNonEmpty(valueToString(proto["Ports"]), valueToString(obj["Ports"]))); ports != "" {
			proxy["ports"] = strings.ReplaceAll(ports, ":", "-")
		}
		if obfsPassword := strings.TrimSpace(valueToString(proto["SalamanderPass"])); obfsPassword != "" {
			proxy["obfs"] = "salamander"
			proxy["obfs-password"] = obfsPassword
		}
		applyCommonImportedProxyFields(proxy, obj)
		return proxy
	case 8:
		if server == "" || port == 0 || password == "" {
			return nil
		}
		proxy := map[string]any{
			"name":     name,
			"type":     "tuic",
			"server":   server,
			"port":     port,
			"password": password,
			"udp":      true,
		}
		if username != "" {
			proxy["uuid"] = username
		} else {
			proxy["token"] = password
			delete(proxy, "password")
		}
		if cc := strings.TrimSpace(valueToString(proto["CongestionControl"])); cc != "" {
			proxy["congestion-controller"] = cc
		}
		applyCommonImportedProxyFields(proxy, obj)
		return proxy
	case 9:
		if server == "" || port == 0 || password == "" {
			return nil
		}
		proxy := map[string]any{
			"name":        name,
			"type":        "wireguard",
			"server":      server,
			"port":        port,
			"private-key": password,
			"public-key":  strings.TrimSpace(valueToString(proto["WgPublicKey"])),
			"udp":         true,
		}
		if preSharedKey := strings.TrimSpace(valueToString(proto["WgPresharedKey"])); preSharedKey != "" {
			proxy["pre-shared-key"] = preSharedKey
		}
		if reserved := parseReservedBytes(valueToString(proto["WgReserved"])); len(reserved) > 0 {
			proxy["reserved"] = reserved
		}
		if mtu := intValue(proto["WgMtu"]); mtu > 0 {
			proxy["mtu"] = mtu
		}
		ip, ipv6 := splitInterfaceAddresses(valueToString(proto["WgInterfaceAddress"]))
		if ip != "" {
			proxy["ip"] = ip
		}
		if ipv6 != "" {
			proxy["ipv6"] = ipv6
		}
		return proxy
	case 10:
		if server == "" || port == 0 {
			return nil
		}
		proxy := map[string]any{
			"name":     name,
			"type":     "http",
			"server":   server,
			"port":     port,
			"username": username,
			"password": password,
		}
		applyCommonImportedProxyFields(proxy, obj)
		return proxy
	case 11:
		if server == "" || port == 0 || password == "" {
			return nil
		}
		proxy := map[string]any{
			"name":     name,
			"type":     "anytls",
			"server":   server,
			"port":     port,
			"password": password,
			"udp":      true,
		}
		applyCommonImportedProxyFields(proxy, obj)
		return proxy
	default:
		return nil
	}
}

func applyV2RayStreamSettings(proxy map[string]any, stream map[string]any) {
	if len(stream) == 0 {
		return
	}

	network := strings.ToLower(strings.TrimSpace(valueToString(stream["network"])))
	switch network {
	case "ws":
		proxy["network"] = "ws"
		wsSettings := mapValue(stream["wsSettings"])
		wsOpts := map[string]any{}
		if path := strings.TrimSpace(valueToString(wsSettings["path"])); path != "" {
			wsOpts["path"] = path
		}
		if host := strings.TrimSpace(valueToString(mapValue(wsSettings["headers"])["Host"])); host != "" {
			wsOpts["headers"] = map[string]any{"Host": host}
		}
		if len(wsOpts) > 0 {
			proxy["ws-opts"] = wsOpts
		}
	case "grpc":
		proxy["network"] = "grpc"
		grpcSettings := mapValue(stream["grpcSettings"])
		if serviceName := strings.TrimSpace(firstNonEmpty(valueToString(grpcSettings["serviceName"]), valueToString(grpcSettings["grpc-service-name"]))); serviceName != "" {
			proxy["grpc-opts"] = map[string]any{"grpc-service-name": serviceName}
		}
	case "http", "h2":
		proxy["network"] = "http"
		httpSettings := mapValue(stream["httpSettings"])
		httpOpts := map[string]any{"method": "GET"}
		if path := strings.TrimSpace(valueToString(httpSettings["path"])); path != "" {
			httpOpts["path"] = []string{path}
		}
		hostValue := httpSettings["host"]
		if hosts := sliceValue(hostValue); len(hosts) > 0 {
			host := strings.TrimSpace(valueToString(hosts[0]))
			if host != "" {
				httpOpts["headers"] = map[string][]string{"Host": []string{host}}
			}
		} else if host := strings.TrimSpace(valueToString(hostValue)); host != "" {
			httpOpts["headers"] = map[string][]string{"Host": []string{host}}
		}
		proxy["http-opts"] = httpOpts
	}

	security := strings.ToLower(strings.TrimSpace(valueToString(stream["security"])))
	switch security {
	case "tls":
		proxy["tls"] = true
		tlsSettings := mapValue(stream["tlsSettings"])
		if sni := strings.TrimSpace(firstNonEmpty(valueToString(tlsSettings["serverName"]), valueToString(tlsSettings["sni"]))); sni != "" {
			proxy["servername"] = sni
			proxy["sni"] = sni
		}
		if fp := strings.TrimSpace(firstNonEmpty(valueToString(tlsSettings["fingerprint"]), valueToString(tlsSettings["fp"]))); fp != "" {
			proxy["client-fingerprint"] = fp
			proxy["fingerprint"] = fp
		}
		if boolValue(tlsSettings["allowInsecure"]) {
			proxy["skip-cert-verify"] = true
		}
		if alpn := splitCSV(valueToString(tlsSettings["alpn"])); len(alpn) > 0 {
			proxy["alpn"] = alpn
		}
	case "reality":
		proxy["tls"] = true
		realitySettings := mapValue(stream["realitySettings"])
		if sni := strings.TrimSpace(firstNonEmpty(valueToString(realitySettings["serverName"]), valueToString(realitySettings["sni"]))); sni != "" {
			proxy["servername"] = sni
			proxy["sni"] = sni
		}
		if fp := strings.TrimSpace(firstNonEmpty(valueToString(realitySettings["fingerprint"]), valueToString(realitySettings["fp"]))); fp != "" {
			proxy["client-fingerprint"] = fp
			proxy["fingerprint"] = fp
		}
		proxy["reality-opts"] = map[string]any{
			"public-key": strings.TrimSpace(valueToString(realitySettings["publicKey"])),
			"short-id":   strings.TrimSpace(firstNonEmpty(valueToString(realitySettings["shortId"]), valueToString(realitySettings["short-id"]))),
		}
	}
}

func buildImportedProxyFromV2RayOutbound(obj map[string]any) map[string]any {
	protocol := strings.ToLower(strings.TrimSpace(valueToString(obj["protocol"])))
	if protocol == "" {
		return nil
	}

	name := firstNonEmpty(valueToString(obj["remarks"]), valueToString(obj["tag"]), protocol)
	settings := mapValue(obj["settings"])
	streamSettings := mapValue(obj["streamSettings"])

	switch protocol {
	case "vmess", "vless":
		vnext := firstMapValue(settings["vnext"])
		user := firstMapValue(vnext["users"])
		server := strings.TrimSpace(valueToString(vnext["address"]))
		port := intValue(vnext["port"])
		id := strings.TrimSpace(valueToString(user["id"]))
		if server == "" || port == 0 || id == "" {
			return nil
		}
		proxy := map[string]any{
			"name":   name,
			"type":   protocol,
			"server": server,
			"port":   port,
			"uuid":   id,
			"udp":    true,
		}
		if protocol == "vmess" {
			proxy["cipher"] = firstNonEmpty(valueToString(user["security"]), "auto")
			proxy["alterId"] = intValue(user["alterId"])
		} else {
			proxy["encryption"] = firstNonEmpty(valueToString(user["encryption"]), "none")
			if flow := strings.TrimSpace(valueToString(user["flow"])); flow != "" {
				proxy["flow"] = flow
			}
		}
		applyV2RayStreamSettings(proxy, streamSettings)
		return proxy
	case "trojan":
		serverInfo := firstMapValue(settings["servers"])
		server := strings.TrimSpace(valueToString(serverInfo["address"]))
		port := intValue(serverInfo["port"])
		password := strings.TrimSpace(valueToString(serverInfo["password"]))
		if server == "" || port == 0 || password == "" {
			return nil
		}
		proxy := map[string]any{
			"name":     name,
			"type":     "trojan",
			"server":   server,
			"port":     port,
			"password": password,
			"udp":      true,
		}
		applyV2RayStreamSettings(proxy, streamSettings)
		return proxy
	case "shadowsocks":
		serverInfo := firstMapValue(settings["servers"])
		server := strings.TrimSpace(valueToString(serverInfo["address"]))
		port := intValue(serverInfo["port"])
		password := strings.TrimSpace(valueToString(serverInfo["password"]))
		method := strings.TrimSpace(valueToString(serverInfo["method"]))
		if server == "" || port == 0 || password == "" || method == "" {
			return nil
		}
		return map[string]any{
			"name":     name,
			"type":     "ss",
			"server":   server,
			"port":     port,
			"cipher":   method,
			"password": password,
			"udp":      true,
		}
	case "socks":
		serverInfo := firstMapValue(settings["servers"])
		server := strings.TrimSpace(valueToString(serverInfo["address"]))
		port := intValue(serverInfo["port"])
		if server == "" || port == 0 {
			return nil
		}
		return map[string]any{
			"name":     name,
			"type":     "socks5",
			"server":   server,
			"port":     port,
			"username": strings.TrimSpace(valueToString(serverInfo["user"])),
			"password": strings.TrimSpace(valueToString(serverInfo["pass"])),
			"udp":      true,
		}
	case "http":
		serverInfo := firstMapValue(settings["servers"])
		server := strings.TrimSpace(valueToString(serverInfo["address"]))
		port := intValue(serverInfo["port"])
		if server == "" || port == 0 {
			return nil
		}
		return map[string]any{
			"name":     name,
			"type":     "http",
			"server":   server,
			"port":     port,
			"username": strings.TrimSpace(valueToString(serverInfo["user"])),
			"password": strings.TrimSpace(valueToString(serverInfo["pass"])),
		}
	default:
		return nil
	}
}

func buildImportedProxyFromSIP008(obj map[string]any) map[string]any {
	server := strings.TrimSpace(valueToString(obj["server"]))
	port := intValue(obj["server_port"])
	method := strings.TrimSpace(firstNonEmpty(valueToString(obj["method"]), valueToString(obj["cipher"])))
	password := strings.TrimSpace(valueToString(obj["password"]))
	if server == "" || port == 0 || method == "" || password == "" {
		return nil
	}
	return map[string]any{
		"name":     firstNonEmpty(valueToString(obj["remarks"]), valueToString(obj["name"])),
		"type":     "ss",
		"server":   server,
		"port":     port,
		"cipher":   method,
		"password": password,
		"udp":      true,
	}
}

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

	hasEndpoint := strings.TrimSpace(valueToString(obj["server"])) != "" && intValue(obj["port"]) > 0
	switch typ {
	case "tuic":
		hasToken := strings.TrimSpace(valueToString(obj["token"])) != ""
		hasUserPassword := strings.TrimSpace(valueToString(obj["uuid"])) != "" && strings.TrimSpace(valueToString(obj["password"])) != ""
		return hasEndpoint && (hasToken || hasUserPassword)
	case "wireguard":
		if strings.TrimSpace(valueToString(obj["private-key"])) == "" ||
			(strings.TrimSpace(valueToString(obj["ip"])) == "" && strings.TrimSpace(valueToString(obj["ipv6"])) == "") {
			return false
		}
		peers := sliceValue(obj["peers"])
		if len(peers) == 0 {
			return hasEndpoint && strings.TrimSpace(valueToString(obj["public-key"])) != ""
		}
		for _, rawPeer := range peers {
			peer := mapValue(rawPeer)
			if strings.TrimSpace(valueToString(peer["server"])) == "" || intValue(peer["port"]) <= 0 ||
				strings.TrimSpace(valueToString(peer["public-key"])) == "" || len(sliceValue(peer["allowed-ips"])) == 0 {
				return false
			}
		}
		return true
	default:
		return hasEndpoint
	}
}

func buildImportedNodeFromProxyMap(proxy map[string]any) (nodes.Node, bool) {
	if len(proxy) == 0 {
		return nodes.Node{}, false
	}
	raw := buildClashURI(proxy)
	if raw == "" {
		return nodes.Node{}, false
	}
	return parseImportedNodeLine(raw)
}

func buildImportedNodeFromMap(obj map[string]any) (nodes.Node, bool) {
	if proxy := buildImportedProxyFromV2RayNProfile(obj); len(proxy) > 0 {
		return buildImportedNodeFromProxyMap(proxy)
	}
	if proxy := buildImportedProxyFromV2RayOutbound(obj); len(proxy) > 0 {
		return buildImportedNodeFromProxyMap(proxy)
	}
	if proxy := buildImportedProxyFromSIP008(obj); len(proxy) > 0 {
		return buildImportedNodeFromProxyMap(proxy)
	}
	if looksLikeClashProxyMap(obj) {
		return buildClashNode(obj)
	}
	return nodes.Node{}, false
}

func buildImportedNodesFromSlice(items []any) []nodes.Node {
	imported := make([]nodes.Node, 0, len(items))
	for _, item := range items {
		obj := mapValue(item)
		if len(obj) == 0 {
			continue
		}
		if node, ok2 := buildImportedNodeFromMap(obj); ok2 {
			imported = append(imported, node)
		}
	}
	return imported
}

func parseJSONImportedNodes(text string) []nodes.Node {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	var raw any
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return nil
	}

	normalized := normalizeYAMLValue(raw)
	if obj, ok := normalized.(map[string]any); ok {
		if proxies := buildImportedNodesFromSlice(sliceValue(obj["proxies"])); len(proxies) > 0 {
			return proxies
		}
		if outbounds := buildImportedNodesFromSlice(sliceValue(obj["outbounds"])); len(outbounds) > 0 {
			return outbounds
		}
		if servers := buildImportedNodesFromSlice(sliceValue(obj["servers"])); len(servers) > 0 {
			return servers
		}
		if node, ok2 := buildImportedNodeFromMap(obj); ok2 {
			return []nodes.Node{node}
		}
		return nil
	}
	if items, ok := normalized.([]any); ok {
		return buildImportedNodesFromSlice(items)
	}
	return nil
}

func parseV2RayNNodeLine(line string) (nodes.Node, bool) {
	raw := strings.TrimSpace(line)
	if !strings.HasPrefix(strings.ToLower(raw), "v2rayn://") {
		return nodes.Node{}, false
	}

	body := raw[len("v2rayn://"):]
	slash := strings.IndexByte(body, '/')
	if slash <= 0 || slash+1 >= len(body) {
		return nodes.Node{}, false
	}

	encoded := body[slash+1:]
	encoded = strings.ReplaceAll(strings.ReplaceAll(encoded, "-", "+"), "_", "/")
	if pad := len(encoded) % 4; pad != 0 {
		encoded += strings.Repeat("=", 4-pad)
	}

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nodes.Node{}, false
	}

	var obj map[string]any
	if errUnm := json.Unmarshal(decoded, &obj); errUnm != nil {
		return nodes.Node{}, false
	}

	normalized, _ := normalizeYAMLValue(obj).(map[string]any)
	if len(normalized) == 0 {
		return nodes.Node{}, false
	}
	return buildImportedNodeFromMap(normalized)
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

	if proxies, err := transport.ParseClashYAMLProxies([]byte(yamlText)); err == nil && len(proxies) > 0 {
		return buildClashNodes(proxies)
	}
	return parseInlineClashYAMLNodes(yamlText)
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

	rawURI := clashProxyObjectToURI(normalized)
	if rawURI == "" {
		return nodes.Node{}, false
	}
	return parseImportedNodeLine(rawURI)
}

func clashProxyObjectToURI(proxy map[string]any) string {
	body, err := json.Marshal(proxy)
	if err != nil {
		return ""
	}
	return "clash://" + base64.StdEncoding.EncodeToString(body)
}
