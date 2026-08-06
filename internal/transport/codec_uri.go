package transport

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ParseURI 解析各种协议的节点链接，产出统一的 ParsedNode IR。
// clash:// 自定义包装格式已删除（C1）：clash:// 走 default 返回错误。
func ParseURI(uri string) (*ParsedNode, error) {
	if strings.HasPrefix(uri, "vless://") {
		return parseVless(uri)
	}
	if strings.HasPrefix(uri, "trojan://") {
		return parseTrojan(uri)
	}
	if strings.HasPrefix(uri, "vmess://") {
		return parseVmess(uri)
	}
	if strings.HasPrefix(uri, "ss://") {
		return parseShadowsocksURI(uri)
	}
	if strings.HasPrefix(uri, "hysteria2://") || strings.HasPrefix(uri, "hy2://") {
		return parseHysteria2(uri)
	}
	if strings.HasPrefix(uri, "tuic://") {
		return parseTuic(uri)
	}
	if strings.HasPrefix(uri, "socks5://") || strings.HasPrefix(uri, "socks5h://") || strings.HasPrefix(uri, "socks://") ||
		strings.HasPrefix(uri, "socks4://") || strings.HasPrefix(uri, "socks4a://") {
		return parseSocks(uri)
	}
	if strings.HasPrefix(uri, "http://") || strings.HasPrefix(uri, "https://") {
		return parseHTTP(uri)
	}
	if strings.HasPrefix(uri, "ssh://") {
		return parseSSH(uri)
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
	safeURI := uri
	if len(safeURI) > 10 {
		safeURI = safeURI[:10]
	}
	return nil, fmt.Errorf("unsupported or complex protocol: %s", safeURI)
}

func parseVless(uri string) (*ParsedNode, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)
	}
	q := u.Query()
	n := &ParsedNode{
		RawURI: uri,
		Type:   "vless",
		Name:   parseFragment(u.Fragment),
		Server: u.Hostname(),
		Port:   parsePortOrDefault(u.Port(), 443),
	}
	if u.User != nil {
		n.UUID = u.User.Username()
	}

	switch strings.ToLower(q.Get("security")) {
	case "reality":
		tls := &TLSOptions{Enabled: true}
		tls.ServerName = firstNonEmpty(q.Get("sni"), q.Get("servername"), u.Hostname())
		if pubKey := firstNonEmpty(q.Get("pbk"), q.Get("public-key")); pubKey != "" {
			tls.Reality = &RealityOptions{
				PublicKey: pubKey,
				ShortID:   firstNonEmpty(q.Get("sid"), q.Get("short-id")),
			}
		}
		if fp := firstNonEmpty(q.Get("fp"), q.Get("client-fingerprint"), q.Get("fingerprint")); fp != "" {
			tls.Fingerprint = fp
		} else {
			tls.Fingerprint = "chrome"
		}
		if alpn := q.Get("alpn"); alpn != "" {
			tls.ALPN = strings.Split(alpn, ",")
		}
		n.TLS = tls
	case "tls":
		tls := &TLSOptions{Enabled: true}
		tls.ServerName = firstNonEmpty(q.Get("sni"), q.Get("servername"), u.Hostname())
		if queryFlag(q, "allowInsecure", "insecure") {
			tls.Insecure = true
		}
		if fp := firstNonEmpty(q.Get("fp"), q.Get("client-fingerprint"), q.Get("fingerprint")); fp != "" {
			tls.Fingerprint = fp
		}
		if alpn := q.Get("alpn"); alpn != "" {
			tls.ALPN = strings.Split(alpn, ",")
		}
		n.TLS = tls
	}

	if flow := q.Get("flow"); flow != "" {
		n.Flow = flow
	}
	applyVlessTrojanTransport(n, q)
	if pe := q.Get("packet_encoding"); pe != "" {
		n.PacketEncoding = pe
	} else if q.Get("packetAddr") == "true" || q.Get("xudp") == "true" {
		n.PacketEncoding = "packetaddr"
	}
	applyCapability(n)
	return n, nil
}

func parseTrojan(uri string) (*ParsedNode, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)
	}
	q := u.Query()
	n := &ParsedNode{
		RawURI: uri,
		Type:   "trojan",
		Name:   parseFragment(u.Fragment),
		Server: u.Hostname(),
		Port:   parsePortOrDefault(u.Port(), 443),
	}
	if u.User != nil {
		n.Password = u.User.Username()
	}

	// trojan 强制启用 TLS（等价 builder forceTLS=true）
	tls := &TLSOptions{Enabled: true}
	tls.ServerName = firstNonEmpty(q.Get("sni"), q.Get("servername"), u.Hostname())
	if queryFlag(q, "allowInsecure", "insecure") {
		tls.Insecure = true
	}
	if fp := firstNonEmpty(q.Get("fp"), q.Get("client-fingerprint"), q.Get("fingerprint")); fp != "" {
		tls.Fingerprint = fp
	}
	if alpn := q.Get("alpn"); alpn != "" {
		tls.ALPN = strings.Split(alpn, ",")
	}
	n.TLS = tls

	if flow := q.Get("flow"); flow != "" {
		n.Flow = flow
	}
	applyVlessTrojanTransport(n, q)
	applyCapability(n)
	return n, nil
}

func parseVmess(uri string) (*ParsedNode, error) {
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

	port, _ := strconv.Atoi(fmt.Sprintf("%v", d["port"]))
	n := &ParsedNode{
		RawURI:   uri,
		Type:     "vmess",
		Name:     anyToString(d["ps"]),
		Server:   anyToString(d["add"]),
		Port:     port,
		UUID:     anyToString(d["id"]),
		Cipher:   "auto",
		Security: firstNonEmpty(anyToString(d["scy"]), "auto"),
	}

	// alterId (aid) 兼容 float64/int/string
	if aidVal, ok := d["aid"]; ok {
		switch v := aidVal.(type) {
		case float64:
			n.AlterID = int(v)
		case int:
			n.AlterID = v
		case string:
			if num, err := strconv.Atoi(v); err == nil {
				n.AlterID = num
			}
		}
	}

	// TLS：d["tls"]=="tls" 才启用；sni>host>server（host 双用途 C9）
	host, _ := d["host"].(string)
	if tlsStr, _ := d["tls"].(string); strings.ToLower(tlsStr) == "tls" {
		sni, _ := d["sni"].(string)
		if sni == "" {
			sni = host
		}
		if sni == "" {
			sni = n.Server
		}
		tls := &TLSOptions{Enabled: true, ServerName: sni}
		if fp, ok := d["fp"].(string); ok && fp != "" {
			tls.Fingerprint = fp
		}
		if alpn, ok := d["alpn"].(string); ok && alpn != "" {
			tls.ALPN = strings.Split(alpn, ",")
		}
		if insecure, ok := d["skip-cert-verify"].(bool); ok {
			tls.Insecure = insecure
		} else if allowInsecure, ok := d["allowInsecure"].(string); ok && allowInsecure == "1" {
			tls.Insecure = true
		}
		n.TLS = tls
	}

	// 传输层：tcp/none/raw/tcpheader/空 → nil（tcpAliases，与 vless/trojan 一致）；h2→http；ws 空 host 不填 Headers；grpc 用 path 作 ServiceName
	netType, _ := d["net"].(string)
	netType = strings.ToLower(strings.TrimSpace(netType))
	if netType != "" && !tcpAliases[netType] {
		path, _ := d["path"].(string)
		switch netType {
		case "ws":
			tr := &TransportOptions{Type: "ws", Path: path}
			if host != "" {
				tr.Host = host
				tr.Headers = map[string][]string{"Host": {host}}
			}
			if rawED := firstNonEmpty(anyToString(d["ed"]), anyToString(d["max_early_data"]), anyToString(d["max-early-data"]), anyToString(d["maxEarlyData"])); rawED != "" {
				if ed, err := strconv.ParseUint(rawED, 10, 32); err == nil {
					tr.MaxEarlyData = uint32(ed)
				}
			}
			if edHeader := firstNonEmpty(anyToString(d["early_data_header_name"]), anyToString(d["early-data-header-name"]), anyToString(d["earlyDataHeaderName"])); edHeader != "" {
				tr.EarlyDataHeaderName = edHeader
			}
			n.Transport = tr
		case "grpc":
			n.Transport = &TransportOptions{Type: "grpc", ServiceName: path}
		case "http", "h2":
			hPath := path
			if hPath == "" {
				hPath = "/"
			}
			tr := &TransportOptions{Type: "http", Path: hPath, Method: "GET"}
			if host != "" {
				tr.Host = host
				tr.Headers = map[string][]string{"Host": {host}}
			}
			n.Transport = tr
		default:
			n.Transport = &TransportOptions{Type: netType}
		}
	}
	applyCapability(n)
	return n, nil
}

func parseShadowsocksURI(uri string) (*ParsedNode, error) {
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

	n := &ParsedNode{
		RawURI:   uri,
		Type:     "ss",
		Name:     parseFragment(u.Fragment),
		Server:   u.Hostname(),
		Port:     port,
		Cipher:   normalizeSSMethod(method),
		Password: password,
	}
	applySSPluginIR(n, u.Query().Get("plugin"))
	applyCapability(n)
	return n, nil
}

func parseSS(uri string) (*ParsedNode, error) {
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

		method, password, err := decodeSSCredentials(userInfo)
		if err != nil || method == "" || password == "" {
			return nil, fmt.Errorf("ss parse failed: invalid userinfo (cannot decode method or password)")
		}

		n := &ParsedNode{
			RawURI:   uri,
			Type:     "ss",
			Server:   hp[0],
			Port:     port,
			Cipher:   normalizeSSMethod(method),
			Password: password,
		}
		applyCapability(n)
		return n, nil
	}
	return nil, fmt.Errorf("ss parse failed")
}

func parseSocks(uri string) (*ParsedNode, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)
	}
	n := &ParsedNode{
		RawURI:       uri,
		Type:         "socks5",
		Name:         parseFragment(u.Fragment),
		Server:       u.Hostname(),
		Port:         parsePortOrDefault(u.Port(), 1080),
		SOCKSVersion: "5",
	}
	switch {
	case strings.HasPrefix(uri, "socks4a://"):
		n.SOCKSVersion = "4a"
	case strings.HasPrefix(uri, "socks4://"):
		n.SOCKSVersion = "4"
	}
	if u.User != nil {
		// 仅 userinfo 有 password 时填 Password（对齐 builder 现状）；仅 username 时保留 Username
		// （proxyMapToURI 转出的 user@host 形式回读不失真，去重 identity 保持对称）
		if pwd, ok := u.User.Password(); ok && pwd != "" {
			n.Username = u.User.Username()
			n.Password = pwd
		} else if u.User.Username() != "" {
			n.Username = u.User.Username()
		}
	}
	applyCapability(n)
	return n, nil
}

func parseHTTP(uri string) (*ParsedNode, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)
	}
	n := &ParsedNode{
		RawURI: uri,
		Type:   "http",
		Name:   parseFragment(u.Fragment),
		Server: u.Hostname(),
		Port:   parsePortOrDefault(u.Port(), 80),
	}
	if u.User != nil {
		// 仅 userinfo 有 password 时填 Password（对齐 builder 现状）；仅 username 时保留 Username
		if pwd, ok := u.User.Password(); ok && pwd != "" {
			n.Username = u.User.Username()
			n.Password = pwd
		} else if u.User.Username() != "" {
			n.Username = u.User.Username()
		}
	}
	if strings.HasPrefix(uri, "https://") {
		n.TLS = &TLSOptions{Enabled: true, ServerName: u.Hostname()}
	}
	applyCapability(n)
	return n, nil
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

func parseFragment(fragment string) string {
	if dec, err := url.QueryUnescape(fragment); err == nil {
		return dec
	}
	return fragment
}

func parsePortOrDefault(port string, def int) int {
	if port == "" {
		return def
	}
	p, err := strconv.Atoi(port)
	if err != nil || p == 0 {
		return def
	}
	return p
}

func anyToString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", x)
	}
}
