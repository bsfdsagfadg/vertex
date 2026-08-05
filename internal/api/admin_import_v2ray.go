package api

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/nodes"
)

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
			// 无用户名（uuid）时以 password 兼作身份与 token，保证 tuicProxyMapToURI 可导出
			proxy["uuid"] = password
			proxy["token"] = password
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
