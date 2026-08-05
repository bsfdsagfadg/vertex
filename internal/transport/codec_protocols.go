package transport

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// parseHysteria2 解析 hy2/hysteria2 节点：TLS 强制开启，sni>peer>server。
func parseHysteria2(uri string) (*ParsedNode, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)
	}
	q := u.Query()
	n := &ParsedNode{
		RawURI: uri,
		Type:   "hysteria2",
		Name:   parseFragment(u.Fragment),
		Server: u.Hostname(),
		Port:   parsePortOrDefault(u.Port(), 443),
	}
	if u.User != nil {
		n.Password = u.User.Username()
	}

	// hy2 强制启用 TLS；sni>peer>server（对齐 builder）
	tls := &TLSOptions{Enabled: true}
	tls.ServerName = firstNonEmpty(q.Get("sni"), q.Get("peer"), u.Hostname())
	if fp := firstNonEmpty(q.Get("fp"), q.Get("client-fingerprint"), q.Get("fingerprint")); fp != "" {
		tls.Fingerprint = fp
	}
	if queryFlag(q, "allowInsecure", "insecure") {
		tls.Insecure = true
	}
	if alpn := q.Get("alpn"); alpn != "" {
		tls.ALPN = strings.Split(alpn, ",")
	}
	n.TLS = tls

	if rawPorts := firstNonEmpty(q.Get("ports"), q.Get("mport")); rawPorts != "" {
		if serverPorts, ok := parseHysteria2Ports(rawPorts); ok {
			n.ServerPorts = []string(serverPorts)
		}
	}
	if obfs := q.Get("obfs"); obfs != "" {
		n.Obfs = obfs
		n.ObfsPassword = firstNonEmpty(q.Get("obfs-password"), q.Get("obfsPassword"))
	}
	applyCapability(n)
	return n, nil
}

// parseTuic 解析 tuic 节点：TLS 强制开启，sni>peer>server。
func parseTuic(uri string) (*ParsedNode, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)
	}
	q := u.Query()
	n := &ParsedNode{
		RawURI: uri,
		Type:   "tuic",
		Name:   parseFragment(u.Fragment),
		Server: u.Hostname(),
		Port:   parsePortOrDefault(u.Port(), 443),
	}
	if u.User != nil {
		n.UUID = u.User.Username()
		if pwd, ok := u.User.Password(); ok {
			n.Password = pwd
		}
	}

	// tuic 强制启用 TLS；sni>peer>server（对齐 builder）
	tls := &TLSOptions{Enabled: true}
	tls.ServerName = firstNonEmpty(q.Get("sni"), q.Get("peer"), u.Hostname())
	if queryFlag(q, "allowInsecure", "insecure") {
		tls.Insecure = true
	}
	if alpn := q.Get("alpn"); alpn != "" {
		tls.ALPN = strings.Split(alpn, ",")
	}
	n.TLS = tls

	if cc := q.Get("congestion_control"); cc != "" {
		n.CongestionControl = cc
	}
	if udpMode := q.Get("udp_relay_mode"); udpMode != "" {
		n.UDPRelayMode = udpMode
	}
	applyCapability(n)
	return n, nil
}

// parseSSH 解析 ssh 节点：支持密码与 pk/psk 私钥认证参数。
func parseSSH(uri string) (*ParsedNode, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)
	}
	q := u.Query()
	n := &ParsedNode{
		RawURI: uri,
		Type:   "ssh",
		Name:   parseFragment(u.Fragment),
		Server: u.Hostname(),
		Port:   parsePortOrDefault(u.Port(), 22),
	}
	if u.User != nil {
		n.Username = u.User.Username()
		if pwd, ok := u.User.Password(); ok {
			n.Password = pwd
		}
	}
	if pk := strings.TrimSpace(q.Get("pk")); pk != "" {
		n.SSHPrivateKey = pk
	}
	if psk := strings.TrimSpace(q.Get("psk")); psk != "" {
		n.SSHPrivateKeyPassphrase = psk
	}
	applyCapability(n)
	return n, nil
}

// parseShadowsocksR 解析 ssr 节点：Base64 主体 + 三键兼容参数读取。
func parseShadowsocksR(uri string) (*ParsedNode, error) {
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

	n := &ParsedNode{
		RawURI:   uri,
		Type:     "ssr",
		Name:     name,
		Server:   server,
		Port:     port,
		Cipher:   method,
		Password: string(pwdBytes),
		Protocol: protocol,
		Obfs:     obfs,
	}

	if params != "" {
		q, _ := url.ParseQuery(params)
		// 三键兼容读（C5）：obfsparam|obfs_param|obfs-param、protoparam|protocol_param|protocol-param
		n.ObfsParam = firstNonEmpty(q.Get("obfsparam"), q.Get("obfs_param"), q.Get("obfs-param"))
		n.ProtocolParam = firstNonEmpty(q.Get("protoparam"), q.Get("protocol_param"), q.Get("protocol-param"))
		if remarks := q.Get("remarks"); remarks != "" && name == "" {
			if dec, err := url.QueryUnescape(remarks); err == nil {
				n.Name = dec
			} else {
				n.Name = remarks
			}
		}
		// group 逻辑保留为忽略：IR 无 group 字段，旧 out["group"] 无消费者
	}
	applyCapability(n)
	return n, nil
}

// parseHysteria 解析 hysteria 节点：auth 支持 userinfo 与 query 双来源。
func parseHysteria(uri string) (*ParsedNode, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)
	}
	q := u.Query()
	n := &ParsedNode{
		RawURI: uri,
		Type:   "hysteria",
		Name:   parseFragment(u.Fragment),
		Server: u.Hostname(),
		Port:   parsePortOrDefault(u.Port(), 443),
	}

	authStr := ""
	if u.User != nil {
		authStr = u.User.Username()
	}
	if authStr == "" {
		authStr = q.Get("auth")
	}
	if authStr != "" {
		n.AuthString = authStr
	}

	// hysteria 强制启用 TLS；sni>server
	tls := &TLSOptions{Enabled: true}
	tls.ServerName = firstNonEmpty(q.Get("sni"), u.Hostname())
	if queryFlag(q, "allowInsecure", "insecure") {
		tls.Insecure = true
	}
	if alpn := q.Get("alpn"); alpn != "" {
		tls.ALPN = strings.Split(alpn, ",")
	}
	n.TLS = tls

	if obfs := q.Get("obfs"); obfs != "" {
		n.Obfs = obfs
	}
	applyCapability(n)
	return n, nil
}

// parseAnyTLS 解析 anytls 节点：TLS 强制开启。
func parseAnyTLS(uri string) (*ParsedNode, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)
	}
	n := &ParsedNode{
		RawURI: uri,
		Type:   "anytls",
		Name:   parseFragment(u.Fragment),
		Server: u.Hostname(),
		Port:   parsePortOrDefault(u.Port(), 443),
	}
	if u.User != nil {
		n.Password = u.User.Username()
	}
	n.TLS = &TLSOptions{Enabled: true, ServerName: u.Hostname()}
	applyCapability(n)
	return n, nil
}

// applyVlessTrojanTransport vless/trojan 共用的传输层解析（type 参数）。
// tcp 类与空 → 保持 Transport=nil；xhttp/splithttp/h2 等保留原始 Type 供 capability 判定。
func applyVlessTrojanTransport(n *ParsedNode, q url.Values) {
	network := strings.ToLower(strings.TrimSpace(q.Get("type")))
	if network == "" || tcpAliases[network] {
		return
	}
	switch network {
	case "ws":
		path := q.Get("path")
		if path == "" {
			path = "/"
		}
		tr := &TransportOptions{Type: "ws", Path: path}
		if host := q.Get("host"); host != "" {
			tr.Host = host
			tr.Headers = map[string][]string{"Host": {host}}
		}
		n.Transport = tr
	case "grpc":
		tr := &TransportOptions{Type: "grpc"}
		if serviceName := q.Get("serviceName"); serviceName != "" {
			tr.ServiceName = serviceName
		}
		n.Transport = tr
	case "http":
		path := q.Get("path")
		if path == "" {
			path = "/"
		}
		tr := &TransportOptions{Type: "http", Path: path, Method: q.Get("method")}
		if host := q.Get("host"); host != "" {
			tr.Host = host
			tr.Headers = map[string][]string{"Host": {host}}
		}
		n.Transport = tr
	case "httpupgrade":
		path := q.Get("path")
		if path == "" {
			path = "/"
		}
		n.Transport = &TransportOptions{Type: "httpupgrade", Path: path, Host: q.Get("host")}
	case "quic":
		n.Transport = &TransportOptions{Type: "quic"}
	default:
		// xhttp/splithttp/h2 等：保留原始 Type，由 applyCapability 判定
		n.Transport = &TransportOptions{Type: network}
	}
}

// applySSPluginIR 把 ss 的 plugin 参数（如 "simple-obfs;obfs=http;obfs-host=x"）写入 IR：
// Plugin=小写原始名，PluginOptions=分段 join(";")（C2，对齐 builder 语义）。
func applySSPluginIR(n *ParsedNode, pluginRaw string) {
	pluginRaw = strings.TrimSpace(pluginRaw)
	if pluginRaw == "" {
		return
	}
	segments := strings.Split(pluginRaw, ";")
	n.Plugin = strings.ToLower(strings.TrimSpace(segments[0]))
	if len(segments) > 1 {
		n.PluginOptions = strings.Join(segments[1:], ";")
	}
}

// decodeSSUserInfo 解析 ss URL 的 userinfo：优先读 password 位，否则走纯凭据解码。
func decodeSSUserInfo(user *url.Userinfo) (string, string, error) {
	if user == nil {
		return "", "", fmt.Errorf("ss parse failed: missing userinfo")
	}
	if password, ok := user.Password(); ok {
		return user.Username(), password, nil
	}
	return decodeSSCredentials(user.Username())
}

// decodeSSCredentials 解码 ss 凭据：支持明文 method:password 与 Base64 混合形态。
func decodeSSCredentials(userInfo string) (string, string, error) {
	if colonIdx := strings.Index(userInfo, ":"); colonIdx != -1 {
		method := userInfo[:colonIdx]
		password := userInfo[colonIdx+1:]
		if isSSPlainMethod(method) {
			return method, password, nil
		}
		mBytes, errM := base64.StdEncoding.DecodeString(padB64(method))
		pBytes, errP := base64.StdEncoding.DecodeString(padB64(password))
		if errM == nil && errP == nil {
			return string(mBytes), string(pBytes), nil
		}
		return method, password, nil
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

// isSSPlainMethod 判定 method 是否为无需 Base64 解码的明文形态。
func isSSPlainMethod(method string) bool {
	if strings.Contains(method, "-") {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(method)) {
	case "aes-128-gcm", "aes-192-gcm", "aes-256-gcm",
		"chacha20-poly1305", "chacha20-ietf-poly1305", "chacha20poly1305",
		"aes-128-cfb", "aes-192-cfb", "aes-256-cfb",
		"rc4", "rc4-md5", "rc4-md5-6", "xchacha20-ietf-poly1305",
		"none", "plain", "table", "2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305":
		return true
	}
	return false
}
