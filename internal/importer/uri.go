package importer

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// proxyMapToURI 把 clash 风格 proxy map 转标准 URI（sing-box 支持的协议）。
// 缺关键字段（server/port/凭证）或 sing-box 不支持的 clash 类型（wireguard/snell 等）返回 ""，
// 调用方跳过不导入。转出 URI 保证能被 ParseURI 解析（parseImportedNodeLine 验证链覆盖）。
func proxyMapToURI(proxy map[string]any) string {
	if len(proxy) == 0 {
		return ""
	}
	typ := strings.ToLower(strings.TrimSpace(ValueToString(proxy["type"])))
	if typ == "" {
		return ""
	}
	name := ValueToString(proxy["name"])
	server := strings.TrimSpace(ValueToString(proxy["server"]))
	port := intValue(proxy["port"])
	if server == "" || port <= 0 {
		return ""
	}
	portStr := strconv.Itoa(port)

	switch typ {
	case "ss":
		return ssProxyMapToURI(proxy, name, server, portStr)
	case "vmess":
		return vmessProxyMapToURI(proxy, name, server, portStr)
	case "vless":
		return vlessTrojanProxyMapToURI("vless", proxy, name, server, portStr)
	case "trojan":
		return vlessTrojanProxyMapToURI("trojan", proxy, name, server, portStr)
	case "hysteria2", "hy2":
		return hy2ProxyMapToURI(proxy, name, server, portStr)
	case "ssr":
		return ssrProxyMapToURI(proxy, name, server, port)
	case "socks5", "socks":
		return userPassProxyMapToURI("socks5", proxy, name, server, portStr)
	case "http":
		return userPassProxyMapToURI("http", proxy, name, server, portStr)
	case "hysteria":
		return hysteriaProxyMapToURI(proxy, name, server, portStr)
	case "anytls":
		password := strings.TrimSpace(ValueToString(proxy["password"]))
		if password == "" {
			return ""
		}
		return buildProxyURI("anytls", password, server, portStr, name, nil)
	case "tuic":
		return tuicProxyMapToURI(proxy, name, server, portStr)
	case "ssh":
		return sshProxyMapToURI(proxy, name, server, portStr)
	default:
		// wireguard/snell/naive/mieru 等 sing-box 不支持的 clash 类型 → 跳过导入
		return ""
	}
}

func proxyStrKey(m map[string]any, key string) string {
	return proxyFirstString(m[key])
}

func proxyFirstString(v any) string {
	switch x := v.(type) {
	case []string:
		if len(x) > 0 {
			return strings.TrimSpace(x[0])
		}
	case []any:
		if len(x) > 0 {
			return strings.TrimSpace(ValueToString(x[0]))
		}
	}
	return strings.TrimSpace(ValueToString(v))
}

func proxyStrList(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			if s := strings.TrimSpace(ValueToString(item)); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// applyMapTLSQuery 填充 vless/trojan/hy2 等通用的 TLS 查询参数（sni/insecure/fp/alpn）。
func applyMapTLSQuery(q url.Values, proxy map[string]any) {
	if sni := firstNonEmpty(proxyStrKey(proxy, "sni"), proxyStrKey(proxy, "servername")); sni != "" {
		q.Set("sni", sni)
	}
	if boolValue(proxy["skip-cert-verify"]) {
		q.Set("allowInsecure", "1")
	}
	if fp := firstNonEmpty(proxyStrKey(proxy, "client-fingerprint"), proxyStrKey(proxy, "fingerprint")); fp != "" {
		q.Set("fp", fp)
	}
	if alpn := proxyStrList(proxy["alpn"]); len(alpn) > 0 {
		q.Set("alpn", strings.Join(alpn, ","))
	}
}

// applyMapTransport 把 clash 的 network/ws-opts/grpc-opts/http-opts 转 URI 查询参数。
func applyMapTransport(q url.Values, proxy map[string]any) {
	network := strings.ToLower(strings.TrimSpace(ValueToString(proxy["network"])))
	if network == "" || network == "tcp" || network == "none" || network == "raw" {
		return
	}
	q.Set("type", network)
	switch network {
	case "ws":
		wsOpts := mapValue(proxy["ws-opts"])
		if path := proxyFirstString(wsOpts["path"]); path != "" {
			q.Set("path", path)
		}
		headers := mapValue(wsOpts["headers"])
		if host := firstNonEmpty(proxyFirstString(headers["Host"]), proxyFirstString(headers["host"])); host != "" {
			q.Set("host", host)
		}
		if ed := firstNonEmpty(proxyFirstString(wsOpts["max-early-data"]), proxyFirstString(wsOpts["max_early_data"]), proxyFirstString(wsOpts["ed"])); ed != "" {
			q.Set("ed", ed)
		}
		if edHeader := firstNonEmpty(proxyFirstString(wsOpts["early-data-header-name"]), proxyFirstString(wsOpts["early_data_header_name"])); edHeader != "" {
			q.Set("early_data_header_name", edHeader)
		}
	case "grpc":
		grpcOpts := mapValue(proxy["grpc-opts"])
		if serviceName := firstNonEmpty(proxyFirstString(grpcOpts["grpc-service-name"]), proxyFirstString(grpcOpts["serviceName"])); serviceName != "" {
			q.Set("serviceName", serviceName)
		}
	case "http", "h2":
		httpOpts := mapValue(proxy["http-opts"])
		if path := proxyFirstString(httpOpts["path"]); path != "" {
			q.Set("path", path)
		}
		headers := mapValue(httpOpts["headers"])
		if host := firstNonEmpty(proxyFirstString(headers["Host"]), proxyFirstString(headers["host"])); host != "" {
			q.Set("host", host)
		}
		if method := proxyFirstString(httpOpts["method"]); method != "" {
			q.Set("method", method)
		}
	case "httpupgrade":
		if path := proxyStrKey(proxy, "path"); path != "" {
			q.Set("path", path)
		}
		if host := proxyStrKey(proxy, "host"); host != "" {
			q.Set("host", host)
		}
	case "quic":
	default:
		// xhttp/splithttp 等：保留 type，capability 判定为 unsupported，导入时跳过
	}
}

func ssProxyMapToURI(proxy map[string]any, name, server, portStr string) string {
	cipher := strings.TrimSpace(ValueToString(proxy["cipher"]))
	password := strings.TrimSpace(ValueToString(proxy["password"]))
	if cipher == "" || password == "" {
		return ""
	}
	userinfo := base64.StdEncoding.EncodeToString([]byte(cipher + ":" + password))
	raw := "ss://" + userinfo + "@" + net.JoinHostPort(server, portStr)
	q := url.Values{}
	if plugin := strings.TrimSpace(ValueToString(proxy["plugin"])); plugin != "" {
		pluginURI := plugin
		if opts := mapValue(proxy["plugin-opts"]); len(opts) > 0 {
			var segments []string
			for _, item := range []struct{ key, uriKey string }{
				{"mode", "obfs"},
				{"host", "obfs-host"},
				{"path", "obfs-uri"},
				{"tls", "obfs-tls"},
				{"cert", "cert"},
				{"password", "password"},
			} {
				if v := strings.TrimSpace(ValueToString(opts[item.key])); v != "" {
					segments = append(segments, item.uriKey+"="+v)
				}
			}
			if len(segments) > 0 {
				pluginURI += ";" + strings.Join(segments, ";")
			}
		}
		q.Set("plugin", pluginURI)
	}
	if len(q) > 0 {
		raw += "?" + q.Encode()
	}
	if name != "" {
		raw += "#" + url.QueryEscape(name)
	}
	return raw
}

func vmessProxyMapToURI(proxy map[string]any, name, server, portStr string) string {
	uuid := strings.TrimSpace(ValueToString(proxy["uuid"]))
	if uuid == "" {
		return ""
	}
	alterID := intValue(proxy["alterId"])
	if alterID == 0 {
		alterID = intValue(proxy["aid"])
	}
	network := strings.ToLower(strings.TrimSpace(ValueToString(proxy["network"])))
	if network == "" || network == "tcp" || network == "none" || network == "raw" {
		network = "tcp"
	}
	d := map[string]any{
		"v":    "2",
		"ps":   name,
		"add":  server,
		"port": portStr,
		"id":   uuid,
		"aid":  alterID,
		"net":  network,
		"type": "none",
		"host": "",
		"path": "",
		"tls":  "",
	}
	switch network {
	case "ws":
		wsOpts := mapValue(proxy["ws-opts"])
		d["path"] = proxyFirstString(wsOpts["path"])
		headers := mapValue(wsOpts["headers"])
		d["host"] = firstNonEmpty(proxyFirstString(headers["Host"]), proxyFirstString(headers["host"]))
	case "grpc":
		grpcOpts := mapValue(proxy["grpc-opts"])
		d["path"] = firstNonEmpty(proxyFirstString(grpcOpts["grpc-service-name"]), proxyFirstString(grpcOpts["serviceName"]))
	case "http", "h2":
		httpOpts := mapValue(proxy["http-opts"])
		d["path"] = proxyFirstString(httpOpts["path"])
		headers := mapValue(httpOpts["headers"])
		d["host"] = firstNonEmpty(proxyFirstString(headers["Host"]), proxyFirstString(headers["host"]))
	}
	if boolValue(proxy["tls"]) {
		d["tls"] = "tls"
		if sni := firstNonEmpty(proxyStrKey(proxy, "sni"), proxyStrKey(proxy, "servername")); sni != "" {
			d["sni"] = sni
		}
		if fp := firstNonEmpty(proxyStrKey(proxy, "client-fingerprint"), proxyStrKey(proxy, "fingerprint")); fp != "" {
			d["fp"] = fp
		}
		if alpn := proxyStrList(proxy["alpn"]); len(alpn) > 0 {
			d["alpn"] = strings.Join(alpn, ",")
		}
		if boolValue(proxy["skip-cert-verify"]) {
			d["skip-cert-verify"] = true
		}
	}
	body, err := json.Marshal(d)
	if err != nil {
		return ""
	}
	return "vmess://" + base64.StdEncoding.EncodeToString(body)
}

func vlessTrojanProxyMapToURI(scheme string, proxy map[string]any, name, server, portStr string) string {
	credential := strings.TrimSpace(ValueToString(proxy["uuid"]))
	if scheme == "trojan" {
		credential = strings.TrimSpace(ValueToString(proxy["password"]))
	}
	if credential == "" {
		return ""
	}
	q := url.Values{}
	if scheme == "vless" {
		reality := mapValue(proxy["reality-opts"])
		switch {
		case len(reality) > 0:
			q.Set("security", "reality")
			if publicKey := firstNonEmpty(proxyStrKey(reality, "public-key"), proxyStrKey(reality, "publicKey")); publicKey != "" {
				q.Set("pbk", publicKey)
			}
			if shortID := firstNonEmpty(proxyStrKey(reality, "short-id"), proxyStrKey(reality, "shortId")); shortID != "" {
				q.Set("sid", shortID)
			}
		case boolValue(proxy["tls"]):
			q.Set("security", "tls")
		}
		if flow := strings.TrimSpace(ValueToString(proxy["flow"])); flow != "" {
			q.Set("flow", flow)
		}
	}
	applyMapTLSQuery(q, proxy)
	applyMapTransport(q, proxy)
	return buildProxyURI(scheme, credential, server, portStr, name, q)
}

func hy2ProxyMapToURI(proxy map[string]any, name, server, portStr string) string {
	password := strings.TrimSpace(ValueToString(proxy["password"]))
	if password == "" {
		return ""
	}
	q := url.Values{}
	applyMapTLSQuery(q, proxy)
	if ports := firstNonEmpty(proxyStrKey(proxy, "ports"), proxyStrKey(proxy, "mport")); ports != "" {
		q.Set("ports", ports)
	}
	if obfs := strings.TrimSpace(ValueToString(proxy["obfs"])); obfs != "" {
		q.Set("obfs", obfs)
	}
	if obfsPassword := strings.TrimSpace(ValueToString(proxy["obfs-password"])); obfsPassword != "" {
		q.Set("obfs-password", obfsPassword)
	}
	return buildProxyURI("hy2", password, server, portStr, name, q)
}

func ssrProxyMapToURI(proxy map[string]any, name, server string, port int) string {
	cipher := strings.TrimSpace(ValueToString(proxy["cipher"]))
	password := strings.TrimSpace(ValueToString(proxy["password"]))
	protocol := strings.TrimSpace(ValueToString(proxy["protocol"]))
	obfs := strings.TrimSpace(ValueToString(proxy["obfs"]))
	if cipher == "" || password == "" || protocol == "" || obfs == "" {
		return ""
	}
	body := fmt.Sprintf("%s:%d:%s:%s:%s:%s", server, port, protocol, cipher, obfs,
		base64.StdEncoding.EncodeToString([]byte(password)))
	q := url.Values{}
	if obfsparam := strings.TrimSpace(ValueToString(proxy["obfsparam"])); obfsparam != "" {
		q.Set("obfsparam", obfsparam)
	}
	if protoparam := strings.TrimSpace(ValueToString(proxy["protoparam"])); protoparam != "" {
		q.Set("protoparam", protoparam)
	}
	// 参数必须拼进 base64 体内（标准 SSR URI 格式）：codec 在解码后体内切 "?"，
	// 拼在体外会导致 base64 解码失败、节点导入被静默跳过。
	if len(q) > 0 {
		body += "?" + q.Encode()
	}
	raw := "ssr://" + base64.StdEncoding.EncodeToString([]byte(body))
	if name != "" {
		raw += "#" + url.QueryEscape(name)
	}
	return raw
}

func userPassProxyMapToURI(scheme string, proxy map[string]any, name, server, portStr string) string {
	username := strings.TrimSpace(ValueToString(proxy["username"]))
	password := strings.TrimSpace(ValueToString(proxy["password"]))
	var user *url.Userinfo
	if username != "" && password != "" {
		user = url.UserPassword(username, password)
	} else if username != "" {
		user = url.User(username)
	}
	return buildProxyURIWithUser(scheme, user, server, portStr, name, nil)
}

// sshProxyMapToURI 转出 v2rayN 兼容的 ssh:// URI（pk/psk 参数承载私钥与口令）。
func sshProxyMapToURI(proxy map[string]any, name, server, portStr string) string {
	username := strings.TrimSpace(ValueToString(proxy["username"]))
	password := strings.TrimSpace(ValueToString(proxy["password"]))
	pk := strings.TrimSpace(ValueToString(proxy["private-key"]))
	if username == "" && pk == "" {
		return ""
	}
	q := url.Values{}
	if pk != "" {
		q.Set("pk", pk)
	}
	if psk := strings.TrimSpace(ValueToString(proxy["private-key-passphrase"])); psk != "" {
		q.Set("psk", psk)
	}
	var user *url.Userinfo
	if username != "" && password != "" {
		user = url.UserPassword(username, password)
	} else if username != "" {
		user = url.User(username)
	}
	return buildProxyURIWithUser("ssh", user, server, portStr, name, q)
}

func hysteriaProxyMapToURI(proxy map[string]any, name, server, portStr string) string {
	auth := strings.TrimSpace(ValueToString(proxy["auth_str"]))
	if auth == "" {
		auth = strings.TrimSpace(ValueToString(proxy["auth-str"]))
	}
	if auth == "" {
		return ""
	}
	q := url.Values{}
	applyMapTLSQuery(q, proxy)
	if obfs := strings.TrimSpace(ValueToString(proxy["obfs"])); obfs != "" {
		q.Set("obfs", obfs)
	}
	return buildProxyURI("hysteria", auth, server, portStr, name, q)
}

func tuicProxyMapToURI(proxy map[string]any, name, server, portStr string) string {
	uuid := strings.TrimSpace(ValueToString(proxy["uuid"]))
	if uuid == "" {
		return ""
	}
	q := url.Values{}
	applyMapTLSQuery(q, proxy)
	if cc := firstNonEmpty(proxyStrKey(proxy, "congestion-controller"), proxyStrKey(proxy, "congestion_control")); cc != "" {
		q.Set("congestion_control", cc)
	}
	if udpMode := firstNonEmpty(proxyStrKey(proxy, "udp-relay-mode"), proxyStrKey(proxy, "udp_relay_mode")); udpMode != "" {
		q.Set("udp_relay_mode", udpMode)
	}
	token := strings.TrimSpace(ValueToString(proxy["token"]))
	if token == "" {
		token = strings.TrimSpace(ValueToString(proxy["password"]))
	}
	var user *url.Userinfo
	if token != "" {
		user = url.UserPassword(uuid, token)
	} else {
		user = url.User(uuid)
	}
	return buildProxyURIWithUser("tuic", user, server, portStr, name, q)
}

// buildProxyURIWithUser 与 buildProxyURI 等价，但允许显式 userinfo（socks/http/tuic 需要 user:pass 形式）。
func buildProxyURIWithUser(scheme string, user *url.Userinfo, server, port, name string, q url.Values) string {
	u := &url.URL{
		Scheme:   scheme,
		User:     user,
		Host:     net.JoinHostPort(server, port),
		Fragment: name,
	}
	if len(q) > 0 {
		u.RawQuery = q.Encode()
	}
	return u.String()
}


