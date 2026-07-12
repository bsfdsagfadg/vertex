package transport

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

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
	if typ == "trojan" || typ == "hysteria2" {
		out["password"] = username
	} else {
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
	}

	if typ == "vless" || typ == "trojan" {
		if flow := q.Get("flow"); flow != "" {
			out["flow"] = flow
		}
		if fp := firstNonEmpty(q.Get("fp"), q.Get("client-fingerprint"), q.Get("fingerprint")); fp != "" {
			out["client-fingerprint"] = fp
		}
		network := q.Get("type")
		if network == "ws" || network == "grpc" || network == "http" || network == "xhttp" {
			out["network"] = network
			switch network {
			case "ws":
				path := q.Get("path")
				if path == "" {
					path = "/"
				}
				host := q.Get("host")
				wsOpts := map[string]any{"path": path}
				if host != "" {
					wsOpts["headers"] = map[string]any{"Host": host}
				}
				out["ws-opts"] = wsOpts
			case "grpc":
				if serviceName := q.Get("serviceName"); serviceName != "" {
					out["grpc-opts"] = map[string]any{"grpc-service-name": serviceName}
				}
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
			out["fingerprint"] = fp
		}
		if alpn := q.Get("alpn"); alpn != "" {
			out["alpn"] = strings.Split(alpn, ",")
		}
	}
	return out, nil
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

	// 初始化 VMess 出站基本参数
	out := map[string]any{
		"name":   d["ps"],
		"type":   "vmess",
		"server": d["add"],
		"port":   port,
		"uuid":   d["id"],
		"cipher": "auto",
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

	// 2. 补全 TLS 配置（极关键，修复免费-日本1等节点的 TLS 缺失）
	tlsStr, _ := d["tls"].(string)
	if strings.ToLower(tlsStr) == "tls" {
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
			out["fingerprint"] = fp
		}
		if alpn, ok := d["alpn"].(string); ok && alpn != "" {
			out["alpn"] = strings.Split(alpn, ",")
		}
		if insecure, ok := d["skip-cert-verify"].(bool); ok {
			out["skip-cert-verify"] = insecure
		} else if allowInsecure, ok := d["allowInsecure"].(string); ok && allowInsecure == "1" {
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
		case "http", "h2":
			hPath := path
			if hPath == "" {
				hPath = "/"
			}
			out["http-opts"] = map[string]any{
				"method":  "GET",
				"path":    []string{hPath},
				"headers": map[string][]string{"Host": {host}},
			}
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
		"cipher":   method,
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
		key, value, ok := strings.Cut(segment, "=")
		if !ok {
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
			"cipher":   method,
			"password": password,
		}, nil
	}
	return nil, fmt.Errorf("ss parse failed")
}

func parseSocks(uri string) (map[string]any, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)
	}
	port, _ := strconv.Atoi(u.Port())
	if port == 0 {
		port = 1080
	}
	name := u.Fragment
	if dec, err := url.QueryUnescape(name); err == nil {
		name = dec
	}
	out := map[string]any{"name": name, "type": "socks5", "server": u.Hostname(), "port": port}
	if u.User != nil {
		out["username"] = u.User.Username()
		out["password"], _ = u.User.Password()
	}
	return out, nil
}

func parseHTTP(uri string) (map[string]any, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)
	}
	port, _ := strconv.Atoi(u.Port())
	if port == 0 {
		port = 80
	}
	name := u.Fragment
	if dec, err := url.QueryUnescape(name); err == nil {
		name = dec
	}
	out := map[string]any{"name": name, "type": "http", "server": u.Hostname(), "port": port}
	if u.User != nil {
		out["username"] = u.User.Username()
		out["password"], _ = u.User.Password()
	}
	if strings.HasPrefix(uri, "https://") {
		out["tls"] = true
	}
	return out, nil
}

func parseShadowsocksR(uri string) (map[string]any, error) {
	prefix := "ssr://"
	if strings.HasPrefix(uri, "shadowsocksr://") {
		prefix = "shadowsocksr://"
	}
	body := uri[len(prefix):]

	name := ""
	if idx := strings.Index(body, "#"); idx != -1 {
		if dec, err := url.QueryUnescape(body[idx+1:]); err == nil {
			name = dec
		} else {
			name = body[idx+1:]
		}
		body = body[:idx]
	}

	b, err := base64.StdEncoding.DecodeString(padB64(body))
	if err != nil {
		return nil, fmt.Errorf("ssr: failed to decode body: %w", err)
	}

	decoded := string(b)

	params := ""
	if idx := strings.Index(decoded, "?"); idx != -1 {
		params = decoded[idx+1:]
		decoded = decoded[:idx]
	}

	parts := strings.SplitN(decoded, ":", 6)
	if len(parts) < 6 {
		return nil, fmt.Errorf("ssr: invalid body format: %s", decoded)
	}

	server := parts[0]
	port, _ := strconv.Atoi(parts[1])
	protocol := parts[2]
	method := parts[3]
	obfs := parts[4]
	pwdB64 := strings.TrimRight(parts[5], "/")

	pwdBytes, err := base64.StdEncoding.DecodeString(padB64(pwdB64))
	if err != nil {
		return nil, fmt.Errorf("ssr: failed to decode password: %w", err)
	}

	out := map[string]any{
		"name":     name,
		"type":     "ssr",
		"server":   server,
		"port":     port,
		"cipher":   method,
		"password": string(pwdBytes),
		"protocol": protocol,
		"obfs":     obfs,
	}

	if params != "" {
		q, _ := url.ParseQuery(params)
		if obfsParam := q.Get("obfsparam"); obfsParam != "" {
			out["obfs-param"] = obfsParam
		}
		if protoParam := q.Get("protoparam"); protoParam != "" {
			out["protocol-param"] = protoParam
		}
		if remarks := q.Get("remarks"); remarks != "" && name == "" {
			if dec, err := url.QueryUnescape(remarks); err == nil {
				out["name"] = dec
			} else {
				out["name"] = remarks
			}
		}
		if group := q.Get("group"); group != "" {
			out["group"] = group
		}
	}

	return out, nil
}

func parseHysteria(uri string) (map[string]any, error) {
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

	out := map[string]any{"name": name, "type": "hysteria", "server": u.Hostname(), "port": port}

	authStr := ""
	if u.User != nil {
		authStr = u.User.Username()
	}
	if authStr == "" {
		authStr = q.Get("auth")
	}
	if authStr != "" {
		out["auth_str"] = authStr
	}

	out["tls"] = true
	sni := firstNonEmpty(q.Get("sni"), q.Get("peer"), u.Hostname())
	out["sni"] = sni
	out["servername"] = sni

	if queryFlag(q, "allowInsecure", "insecure") {
		out["skip-cert-verify"] = true
	}

	if obfs := q.Get("obfs"); obfs != "" {
		out["obfs"] = obfs
	}
	if alpn := q.Get("alpn"); alpn != "" {
		out["alpn"] = strings.Split(alpn, ",")
	}

	return out, nil
}

func parseAnyTLS(uri string) (map[string]any, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)
	}
	port, _ := strconv.Atoi(u.Port())
	if port == 0 {
		port = 443
	}
	name := u.Fragment
	if dec, err := url.QueryUnescape(name); err == nil {
		name = dec
	}

	out := map[string]any{"name": name, "type": "anytls", "server": u.Hostname(), "port": port}

	if u.User != nil {
		out["password"] = u.User.Username()
	} else {
		out["password"] = ""
	}

	out["tls"] = true
	out["sni"] = u.Hostname()
	out["servername"] = u.Hostname()

	return out, nil
}
