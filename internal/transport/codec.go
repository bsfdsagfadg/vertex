package transport

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

func padB64(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "-", "+"), "_", "/")
	if pad := len(s) % 4; pad != 0 {
		s += strings.Repeat("=", 4-pad)
	}
	return s
}

// ParseURI 解析各种协议的节点链接
func ParseURI(uri string) (map[string]any, error) {
	if strings.HasPrefix(uri, "vless://") {
		return parseSimple(uri, "vless")
	}
	if strings.HasPrefix(uri, "trojan://") {
		return parseSimple(uri, "trojan")
	}
	if strings.HasPrefix(uri, "vmess://") {
		return parseVmess(uri)
	}
	if strings.HasPrefix(uri, "ss://") {
		return parseShadowsocksURI(uri)
	}
	if strings.HasPrefix(uri, "hysteria2://") || strings.HasPrefix(uri, "hy2://") {
		return parseSimple(uri, "hysteria2")
	}
	if strings.HasPrefix(uri, "tuic://") {
		return parseSimple(uri, "tuic")
	}
	if strings.HasPrefix(uri, "socks5://") || strings.HasPrefix(uri, "socks5h://") || strings.HasPrefix(uri, "socks://") {
		return parseSocks(uri)
	}
	if strings.HasPrefix(uri, "http://") || strings.HasPrefix(uri, "https://") {
		return parseHTTP(uri)
	}
	if strings.HasPrefix(uri, "ssr://") || strings.HasPrefix(uri, "shadowsocksr://") {
		return parseShadowsocksR(uri)
	}
	if strings.HasPrefix(uri, "hysteria://") {
		return parseHysteria(uri)
	}
	if strings.HasPrefix(uri, "anytls://") {
		return parseAnyTLS(uri)
	}
	if strings.HasPrefix(uri, "clash://") {
		b, _ := base64.StdEncoding.DecodeString(padB64(uri[8:]))
		var d map[string]any
		_ = json.Unmarshal(b, &d)
		return d, nil
	}
	safeURI := uri
	if len(safeURI) > 10 {
		safeURI = safeURI[:10]
	}
	return nil, fmt.Errorf("unsupported or complex protocol: %s", safeURI)
}

func parseSimple(uri, typ string) (map[string]any, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)

	}
	port, _ := strconv.Atoi(u.Port())
	if port == 0 {
		port = 443
	}
	q := u.Query()
	name := u.Fragment
	if dec, err := url.QueryUnescape(name); err == nil {
		name = dec
	}
	out := map[string]any{"name": name, "type": typ, "server": u.Hostname(), "port": port}

	username := ""
	if u.User != nil {
		username = u.User.Username()
	}
	switch typ {
	case "tuic":
		out["uuid"] = username
		if u.User != nil {
			if password, ok := u.User.Password(); ok {
				out["password"] = password
			}
		}
	case "trojan", "hysteria2":
		out["password"] = username
	default:
		out["uuid"] = username
	}

	sec := strings.ToLower(q.Get("security"))
	if sec == "reality" || sec == "tls" || typ == "trojan" || typ == "hysteria2" || typ == "tuic" {
		out["tls"] = true
		sni := firstNonEmpty(q.Get("sni"), u.Hostname())
		out["sni"] = sni
		out["servername"] = firstNonEmpty(q.Get("servername"), sni)
		if sec != "reality" && queryFlag(q, "allowInsecure", "insecure") {
			out["skip-cert-verify"] = true
		}
	}

	if sec == "reality" {
		if pubKey := firstNonEmpty(q.Get("pbk"), q.Get("public-key")); pubKey != "" {
			out["reality-opts"] = map[string]any{"public-key": pubKey, "short-id": firstNonEmpty(q.Get("sid"), q.Get("short-id"))}
		}
		if firstNonEmpty(q.Get("fp"), q.Get("client-fingerprint"), q.Get("fingerprint")) == "" {
			out["client-fingerprint"] = "chrome"
		}
	}

	if typ == "vless" || typ == "trojan" {
		if flow := q.Get("flow"); flow != "" {
			out["flow"] = flow
		}
		if fp := firstNonEmpty(q.Get("fp"), q.Get("client-fingerprint"), q.Get("fingerprint")); fp != "" {
			out["client-fingerprint"] = fp
		} else if out["tls"] == true {
			out["client-fingerprint"] = "chrome"
		}
		network := q.Get("type")
		if network == "ws" || network == "grpc" || network == "http" || network == "h2" || network == "xhttp" {
			out["network"] = network
			path := q.Get("path")
			if path == "" {
				path = "/"
			}
			host := q.Get("host")
			switch network {
			case "ws":
				wsOpts := map[string]any{"path": path}
				if host != "" {
					wsOpts["headers"] = map[string]any{"Host": host}
				}
				out["ws-opts"] = wsOpts
			case "grpc":
				if serviceName := q.Get("serviceName"); serviceName != "" {
					out["grpc-opts"] = map[string]any{"grpc-service-name": serviceName}
				}
			case "http":
				out["http-opts"] = map[string]any{
					"method": "GET", "path": []string{path},
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
				if mode := q.Get("mode"); mode != "" {
					xhttpOpts["mode"] = mode
				}
				out["xhttp-opts"] = xhttpOpts
			}
		}
		if alpn := q.Get("alpn"); alpn != "" {
			out["alpn"] = strings.Split(alpn, ",")
		}
		if q.Get("packetAddr") == "true" {
			out["packet-addr"] = true
		}
		if q.Get("xudp") == "true" {
			out["xudp"] = true
		}
	}
	if typ == "hysteria2" {
		if sni := firstNonEmpty(q.Get("sni"), q.Get("peer"), u.Hostname()); sni != "" {
			out["sni"] = sni
			out["servername"] = sni
		}
		if ports := firstNonEmpty(q.Get("ports"), q.Get("mport")); ports != "" {
			out["ports"] = ports
		}
		if obfs := q.Get("obfs"); obfs != "" {
			out["obfs"] = obfs
		}
		if obfsPassword := firstNonEmpty(q.Get("obfs-password"), q.Get("obfsPassword")); obfsPassword != "" {
			out["obfs-password"] = obfsPassword
		}
		if fp := firstNonEmpty(q.Get("fp"), q.Get("fingerprint")); fp != "" {
			if isCertPinningFingerprint(fp) {
				out["fingerprint"] = fp
			}
		}
		if alpn := q.Get("alpn"); alpn != "" {
			out["alpn"] = strings.Split(alpn, ",")
		}
	}
	return out, nil
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

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
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

func parseVmess(uri string) (map[string]any, error) {
	b64Str := uri[8:]
	if idx := strings.Index(b64Str, "?"); idx != -1 {
		b64Str = b64Str[:idx]
	}
	if idx := strings.Index(b64Str, "#"); idx != -1 {
		b64Str = b64Str[:idx]
	}
	b, err := base64.StdEncoding.DecodeString(padB64(b64Str))
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)

	}
	var d map[string]any
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("error: %w", err)

	}
	portStr := fmt.Sprintf("%v", d["port"])
	port, _ := strconv.Atoi(portStr)

	cipher := "auto"
	if scy, ok := d["scy"].(string); ok && strings.TrimSpace(scy) != "" && !strings.EqualFold(scy, "auto") {
		cipher = strings.TrimSpace(scy)
	}

	// 初始化 VMess 出站基本参数
	out := map[string]any{
		"name":   d["ps"],
		"type":   "vmess",
		"server": d["add"],
		"port":   port,
		"uuid":   d["id"],
		"cipher": cipher,
	}

	// 1. 映射 alterId (aid)
	if aidVal, ok := d["aid"]; ok {
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

	// 2. 补全 TLS 配置（兼容 bool/string/number 三种订阅字段类型）
	var tlsOn bool
	switch value := d["tls"].(type) {
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
		host, _ := d["host"].(string)
		sni, _ := d["sni"].(string)
		if sni == "" {
			sni = host
		}
		if sni == "" {
			sni, _ = d["add"].(string)
		}
		out["tls"] = true
		out["sni"] = sni
		out["servername"] = sni
		if fp, ok := d["fp"].(string); ok && fp != "" {
			out["client-fingerprint"] = fp
		} else {
			out["client-fingerprint"] = "chrome"
		}
		if alpn, ok := d["alpn"].(string); ok && alpn != "" {
			out["alpn"] = strings.Split(alpn, ",")
		}
		if insecure, ok := d["skip-cert-verify"].(bool); ok {
			out["skip-cert-verify"] = insecure
		} else if allowInsecure, ok := d["allowInsecure"].(string); ok && (allowInsecure == "1" || strings.EqualFold(allowInsecure, "true")) {
			out["skip-cert-verify"] = true
		} else {
			out["skip-cert-verify"] = false
		}
	}

	// 3. 补全 V2Ray 传输层配置（WS / gRPC，修复 IEPL 等节点的 WS 缺失）
	netType, _ := d["net"].(string)
	netType = strings.ToLower(strings.TrimSpace(netType))
	if netType != "" && netType != "tcp" {
		path, _ := d["path"].(string)
		host, _ := d["host"].(string)

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

func parseShadowsocksURI(uri string) (map[string]any, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)

	}
	if u.User == nil || u.Hostname() == "" {
		return parseSS(uri)
	}

	method, password, err := decodeSSUserInfo(u.User)
	if err != nil {
		return nil, err
	}
	port, _ := strconv.Atoi(u.Port())
	if port == 0 {
		return nil, fmt.Errorf("ss parse failed: invalid host:port")
	}
	name := u.Fragment
	if dec, err := url.QueryUnescape(name); err == nil {
		name = dec
	}

	out := map[string]any{
		"name":     name,
		"type":     "ss",
		"server":   u.Hostname(),
		"port":     port,
		"cipher":   normalizeSSCipher(method),
		"password": password,
	}
	applySSPlugin(out, u.Query().Get("plugin"))
	return out, nil
}

func decodeSSUserInfo(user *url.Userinfo) (string, string, error) {
	if user == nil {
		return "", "", fmt.Errorf("ss parse failed: missing userinfo")
	}
	if password, ok := user.Password(); ok {
		return user.Username(), password, nil
	}
	return decodeSSCredentials(user.Username())
}

func decodeSSCredentials(userInfo string) (string, string, error) {
	if colonIdx := strings.Index(userInfo, ":"); colonIdx != -1 {
		mBytes, errM := base64.StdEncoding.DecodeString(padB64(userInfo[:colonIdx]))
		pBytes, errP := base64.StdEncoding.DecodeString(padB64(userInfo[colonIdx+1:]))
		if errM == nil && errP == nil {
			return string(mBytes), string(pBytes), nil
		}
		return userInfo[:colonIdx], userInfo[colonIdx+1:], nil
	}

	b, err := base64.StdEncoding.DecodeString(padB64(userInfo))
	if err == nil {
		parts := strings.SplitN(string(b), ":", 2)
		if len(parts) == 2 {
			return parts[0], parts[1], nil
		}
	}
	return "", "", fmt.Errorf("ss parse failed: invalid userinfo (cannot decode method or password)")
}

func applySSPlugin(out map[string]any, pluginRaw string) {
	pluginRaw = strings.TrimSpace(pluginRaw)
	if pluginRaw == "" {
		return
	}

	segments := strings.Split(pluginRaw, ";")
	plugin := strings.ToLower(strings.TrimSpace(segments[0]))
	rawOpts := map[string]string{}
	for _, segment := range segments[1:] {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		key, value, ok := strings.Cut(segment, "=")
		if !ok {
			rawOpts[strings.ToLower(segment)] = "true"
			continue
		}
		rawOpts[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
	}

	switch plugin {
	case "simple-obfs", "obfs-local", "obfs":
		out["plugin"] = "obfs"
		opts := map[string]any{}
		if mode := firstNonEmpty(rawOpts["obfs"], rawOpts["mode"]); mode != "" {
			opts["mode"] = mode
		}
		if host := firstNonEmpty(rawOpts["obfs-host"], rawOpts["host"]); host != "" {
			opts["host"] = host
		}
		if len(opts) > 0 {
			out["plugin-opts"] = opts
		}
	case "v2ray-plugin":
		out["plugin"] = plugin
		opts := map[string]any{}
		mode := rawOpts["mode"]
		if mode == "" {
			mode = "websocket"
		}
		opts["mode"] = mode
		for _, key := range []string{"host", "path", "fingerprint"} {
			if value := rawOpts[key]; value != "" {
				opts[key] = value
			}
		}
		for _, key := range []string{"tls", "mux", "skip-cert-verify"} {
			if value := rawOpts[key]; value == "true" || value == "1" {
				opts[key] = true
			}
		}
		out["plugin-opts"] = opts
	default:
		out["plugin"] = plugin
		if len(rawOpts) > 0 {
			opts := make(map[string]any, len(rawOpts))
			for key, value := range rawOpts {
				opts[key] = value
			}
			out["plugin-opts"] = opts
		}
	}
}

func normalizeSSCipher(method string) string {
	if strings.EqualFold(strings.TrimSpace(method), "chacha20-poly1305") {
		return "chacha20-ietf-poly1305"
	}
	return method
}

func parseSS(uri string) (map[string]any, error) {
	body := uri[5:]
	if idx := strings.Index(body, "#"); idx != -1 {
		body = body[:idx]
	}
	if idx := strings.Index(body, "@"); idx != -1 {
		userInfo := body[:idx]
		hp := strings.Split(body[idx+1:], ":")
		if len(hp) < 2 {
			return nil, fmt.Errorf("ss parse failed: invalid host:port")
		}
		port, _ := strconv.Atoi(hp[1])

		var method, password string

		// 适配两种形式的 Shadowsocks Base64 用户信息表达
		if colonIdx := strings.Index(userInfo, ":"); colonIdx != -1 {
			// 形式 A: base64(method) : base64(password)
			mBytes, errM := base64.StdEncoding.DecodeString(padB64(userInfo[:colonIdx]))
			pBytes, errP := base64.StdEncoding.DecodeString(padB64(userInfo[colonIdx+1:]))
			if errM == nil && errP == nil {
				method = string(mBytes)
				password = string(pBytes)
			}
		}

		if method == "" || password == "" {
			// 形式 B: 传统的整个 method:password 一起进行 base64 编码
			b, err := base64.StdEncoding.DecodeString(padB64(userInfo))
			if err == nil {
				parts := strings.SplitN(string(b), ":", 2)
				if len(parts) == 2 {
					method = parts[0]
					password = parts[1]
				}
			}
		}

		if method == "" || password == "" {
			return nil, fmt.Errorf("ss parse failed: invalid userinfo (cannot decode method or password)")
		}

		name := ""
		if parts := strings.Split(hp[1], "#"); len(parts) > 1 {
			if dec, err := url.QueryUnescape(parts[1]); err == nil {
				name = dec
			} else {
				name = parts[1]
			}
		}

		return map[string]any{
			"name":     name,
			"type":     "ss",
			"server":   hp[0],
			"port":     port,
			"cipher":   normalizeSSCipher(method),
			"password": password,
		}, nil
	}
	return nil, fmt.Errorf("ss parse failed")
}

func parseSocks(uri string) (map[string]any, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("socks parse failed: %w", err)
	}
	port, err := parseProxyPort(u, 1080)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"name": nameFromURL(u), "type": "socks5", "server": u.Hostname(), "port": port, "udp": true,
	}
	if u.User != nil {
		out["username"] = u.User.Username()
		if password, ok := u.User.Password(); ok {
			out["password"] = password
		}
	}
	return out, nil
}

func parseHTTP(uri string) (map[string]any, error) {
	u, err := url.Parse(uri)
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
		"name": nameFromURL(u), "type": "http", "server": u.Hostname(), "port": port,
	}
	if u.User != nil {
		out["username"] = u.User.Username()
		if password, ok := u.User.Password(); ok {
			out["password"] = password
		}
	}
	if strings.EqualFold(u.Scheme, "https") {
		out["tls"] = true
		out["sni"] = firstNonEmpty(u.Query().Get("sni"), u.Hostname())
		if queryFlag(u.Query(), "allowInsecure", "insecure") {
			out["skip-cert-verify"] = true
		}
	}
	return out, nil
}

func parseShadowsocksR(uri string) (map[string]any, error) {
	prefix := "ssr://"
	if strings.HasPrefix(uri, "shadowsocksr://") {
		prefix = "shadowsocksr://"
	}
	body := strings.TrimSpace(uri[len(prefix):])
	if hash := strings.Index(body, "#"); hash >= 0 {
		body = body[:hash]
	}
	decodedBytes, err := base64.StdEncoding.DecodeString(padB64(body))
	if err != nil {
		return nil, fmt.Errorf("ssr parse failed: decode body: %w", err)
	}
	decoded := string(decodedBytes)
	params := ""
	if queryIndex := strings.Index(decoded, "/?"); queryIndex >= 0 {
		params = decoded[queryIndex+2:]
		decoded = decoded[:queryIndex]
	} else if queryIndex := strings.Index(decoded, "?"); queryIndex >= 0 {
		params = decoded[queryIndex+1:]
		decoded = strings.TrimSuffix(decoded[:queryIndex], "/")
	}
	parts := strings.SplitN(decoded, ":", 6)
	if len(parts) != 6 {
		return nil, fmt.Errorf("ssr parse failed: invalid body")
	}
	port, err := strconv.Atoi(parts[1])
	if err != nil || port <= 0 || port > 65535 || strings.TrimSpace(parts[0]) == "" {
		return nil, fmt.Errorf("ssr parse failed: invalid server or port")
	}
	passwordBytes, err := base64.StdEncoding.DecodeString(padB64(strings.TrimRight(parts[5], "/")))
	if err != nil {
		return nil, fmt.Errorf("ssr parse failed: decode password: %w", err)
	}
	out := map[string]any{
		"name": "", "type": "ssr", "server": parts[0], "port": port,
		"protocol": parts[2], "cipher": parts[3], "obfs": parts[4], "password": string(passwordBytes), "udp": true,
	}
	if params != "" {
		query, _ := url.ParseQuery(params)
		if value := decodeSSRParam(query.Get("obfsparam")); value != "" {
			out["obfs-param"] = value
		}
		if value := decodeSSRParam(query.Get("protoparam")); value != "" {
			out["protocol-param"] = value
		}
		if value := decodeSSRParam(query.Get("remarks")); value != "" {
			out["name"] = value
		}
	}
	return out, nil
}

func decodeSSRParam(value string) string {
	if value == "" {
		return ""
	}
	if decoded, err := base64.StdEncoding.DecodeString(padB64(value)); err == nil {
		return strings.TrimSpace(string(decoded))
	}
	if decoded, err := url.QueryUnescape(value); err == nil {
		return strings.TrimSpace(decoded)
	}
	return strings.TrimSpace(value)
}

func parseHysteria(uri string) (map[string]any, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("hysteria parse failed: %w", err)
	}
	port, err := parseProxyPort(u, 443)
	if err != nil {
		return nil, err
	}
	query := u.Query()
	up := normalizeHysteriaSpeed(firstNonEmpty(query.Get("up"), query.Get("upmbps"), query.Get("up-speed")))
	down := normalizeHysteriaSpeed(firstNonEmpty(query.Get("down"), query.Get("downmbps"), query.Get("down-speed")))
	if up == "" || down == "" {
		return nil, fmt.Errorf("hysteria parse failed: up/down speed is required")
	}
	auth := query.Get("auth")
	if u.User != nil && u.User.Username() != "" {
		auth = u.User.Username()
	}
	out := map[string]any{
		"name": nameFromURL(u), "type": "hysteria", "server": u.Hostname(), "port": port,
		"up": up, "down": down, "auth-str": auth,
		"sni": firstNonEmpty(query.Get("sni"), query.Get("peer"), u.Hostname()),
	}
	if queryFlag(query, "allowInsecure", "insecure") {
		out["skip-cert-verify"] = true
	}
	if obfs := query.Get("obfs"); obfs != "" {
		out["obfs"] = obfs
	}
	if alpn := query.Get("alpn"); alpn != "" {
		out["alpn"] = strings.Split(alpn, ",")
	}
	return out, nil
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

func parseAnyTLS(uri string) (map[string]any, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("anytls parse failed: %w", err)
	}
	port, err := parseProxyPort(u, 443)
	if err != nil {
		return nil, err
	}
	password := ""
	if u.User != nil {
		password = u.User.Username()
	}
	if password == "" {
		return nil, fmt.Errorf("anytls parse failed: password is required")
	}
	query := u.Query()
	out := map[string]any{
		"name": nameFromURL(u), "type": "anytls", "server": u.Hostname(), "port": port,
		"password": password, "sni": firstNonEmpty(query.Get("sni"), u.Hostname()), "udp": true,
	}
	if queryFlag(query, "allowInsecure", "insecure") {
		out["skip-cert-verify"] = true
	}
	if fp := firstNonEmpty(query.Get("fp"), query.Get("fingerprint")); fp != "" {
		out["client-fingerprint"] = fp
	}
	if alpn := query.Get("alpn"); alpn != "" {
		out["alpn"] = strings.Split(alpn, ",")
	}
	return out, nil
}

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
