package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/nodes"
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

// proxyMapToURI 把 clash 风格 proxy map 转标准 URI（sing-box 支持的协议）。
// 缺关键字段（server/port/凭证）或 sing-box 不支持的 clash 类型（wireguard/snell 等）返回 ""，
// 调用方跳过不导入。转出 URI 保证能被 ParseURI 解析（parseImportedNodeLine 验证链覆盖）。
func proxyMapToURI(proxy map[string]any) string {
	if len(proxy) == 0 {
		return ""
	}
	typ := strings.ToLower(strings.TrimSpace(valueToString(proxy["type"])))
	if typ == "" {
		return ""
	}
	name := valueToString(proxy["name"])
	server := strings.TrimSpace(valueToString(proxy["server"]))
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
		password := strings.TrimSpace(valueToString(proxy["password"]))
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
			return strings.TrimSpace(valueToString(x[0]))
		}
	}
	return strings.TrimSpace(valueToString(v))
}

func proxyStrList(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			if s := strings.TrimSpace(valueToString(item)); s != "" {
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
	network := strings.ToLower(strings.TrimSpace(valueToString(proxy["network"])))
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
	cipher := strings.TrimSpace(valueToString(proxy["cipher"]))
	password := strings.TrimSpace(valueToString(proxy["password"]))
	if cipher == "" || password == "" {
		return ""
	}
	userinfo := base64.StdEncoding.EncodeToString([]byte(cipher + ":" + password))
	raw := "ss://" + userinfo + "@" + net.JoinHostPort(server, portStr)
	q := url.Values{}
	if plugin := strings.TrimSpace(valueToString(proxy["plugin"])); plugin != "" {
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
				if v := strings.TrimSpace(valueToString(opts[item.key])); v != "" {
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
	uuid := strings.TrimSpace(valueToString(proxy["uuid"]))
	if uuid == "" {
		return ""
	}
	alterID := intValue(proxy["alterId"])
	if alterID == 0 {
		alterID = intValue(proxy["aid"])
	}
	network := strings.ToLower(strings.TrimSpace(valueToString(proxy["network"])))
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
	credential := strings.TrimSpace(valueToString(proxy["uuid"]))
	if scheme == "trojan" {
		credential = strings.TrimSpace(valueToString(proxy["password"]))
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
		if flow := strings.TrimSpace(valueToString(proxy["flow"])); flow != "" {
			q.Set("flow", flow)
		}
	}
	applyMapTLSQuery(q, proxy)
	applyMapTransport(q, proxy)
	return buildProxyURI(scheme, credential, server, portStr, name, q)
}

func hy2ProxyMapToURI(proxy map[string]any, name, server, portStr string) string {
	password := strings.TrimSpace(valueToString(proxy["password"]))
	if password == "" {
		return ""
	}
	q := url.Values{}
	applyMapTLSQuery(q, proxy)
	if ports := firstNonEmpty(proxyStrKey(proxy, "ports"), proxyStrKey(proxy, "mport")); ports != "" {
		q.Set("ports", ports)
	}
	if obfs := strings.TrimSpace(valueToString(proxy["obfs"])); obfs != "" {
		q.Set("obfs", obfs)
	}
	if obfsPassword := strings.TrimSpace(valueToString(proxy["obfs-password"])); obfsPassword != "" {
		q.Set("obfs-password", obfsPassword)
	}
	return buildProxyURI("hy2", password, server, portStr, name, q)
}

func ssrProxyMapToURI(proxy map[string]any, name, server string, port int) string {
	cipher := strings.TrimSpace(valueToString(proxy["cipher"]))
	password := strings.TrimSpace(valueToString(proxy["password"]))
	protocol := strings.TrimSpace(valueToString(proxy["protocol"]))
	obfs := strings.TrimSpace(valueToString(proxy["obfs"]))
	if cipher == "" || password == "" || protocol == "" || obfs == "" {
		return ""
	}
	body := fmt.Sprintf("%s:%d:%s:%s:%s:%s", server, port, protocol, cipher, obfs,
		base64.StdEncoding.EncodeToString([]byte(password)))
	q := url.Values{}
	if obfsparam := strings.TrimSpace(valueToString(proxy["obfsparam"])); obfsparam != "" {
		q.Set("obfsparam", obfsparam)
	}
	if protoparam := strings.TrimSpace(valueToString(proxy["protoparam"])); protoparam != "" {
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
	username := strings.TrimSpace(valueToString(proxy["username"]))
	password := strings.TrimSpace(valueToString(proxy["password"]))
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
	username := strings.TrimSpace(valueToString(proxy["username"]))
	password := strings.TrimSpace(valueToString(proxy["password"]))
	pk := strings.TrimSpace(valueToString(proxy["private-key"]))
	if username == "" && pk == "" {
		return ""
	}
	q := url.Values{}
	if pk != "" {
		q.Set("pk", pk)
	}
	if psk := strings.TrimSpace(valueToString(proxy["private-key-passphrase"])); psk != "" {
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
	auth := strings.TrimSpace(valueToString(proxy["auth_str"]))
	if auth == "" {
		auth = strings.TrimSpace(valueToString(proxy["auth-str"]))
	}
	if auth == "" {
		return ""
	}
	q := url.Values{}
	applyMapTLSQuery(q, proxy)
	if obfs := strings.TrimSpace(valueToString(proxy["obfs"])); obfs != "" {
		q.Set("obfs", obfs)
	}
	return buildProxyURI("hysteria", auth, server, portStr, name, q)
}

func tuicProxyMapToURI(proxy map[string]any, name, server, portStr string) string {
	uuid := strings.TrimSpace(valueToString(proxy["uuid"]))
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
	token := strings.TrimSpace(valueToString(proxy["token"]))
	if token == "" {
		token = strings.TrimSpace(valueToString(proxy["password"]))
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
	if typ == "wireguard" {
		return strings.TrimSpace(valueToString(obj["private-key"])) != "" &&
			(strings.TrimSpace(valueToString(obj["server"])) != "" || len(sliceValue(obj["peers"])) > 0)
	}
	return strings.TrimSpace(valueToString(obj["server"])) != "" && intValue(obj["port"]) > 0
}

func buildImportedNodeFromProxyMap(proxy map[string]any) (nodes.Node, bool) {
	if len(proxy) == 0 {
		return nodes.Node{}, false
	}
	raw := proxyMapToURI(proxy)
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
