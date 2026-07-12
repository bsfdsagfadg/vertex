package transport

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
)

func buildOutbound(uri string) (option.Outbound, error) {
	h := sha256.Sum256([]byte(uri))
	tag := fmt.Sprintf("node-%x", h[:8])

	switch {
	case strings.HasPrefix(uri, "vless://"):
		u, err := url.Parse(uri)
		if err != nil {
			return option.Outbound{}, fmt.Errorf("vless: %w", err)
		}
		q := u.Query()
		opts, err := buildVLESSOutbound(u, q)
		if err != nil {
			return option.Outbound{}, err
		}
		return option.Outbound{Type: C.TypeVLESS, Tag: tag, Options: &opts}, nil

	case strings.HasPrefix(uri, "vmess://"):
		opts, err := buildVMessOutbound(uri)
		if err != nil {
			return option.Outbound{}, err
		}
		return option.Outbound{Type: C.TypeVMess, Tag: tag, Options: &opts}, nil

	case strings.HasPrefix(uri, "ss://"):
		u, err := url.Parse(uri)
		if err != nil {
			return option.Outbound{}, fmt.Errorf("ss: %w", err)
		}
		opts, err := buildShadowsocksOutbound(u)
		if err != nil {
			return option.Outbound{}, err
		}
		return option.Outbound{Type: C.TypeShadowsocks, Tag: tag, Options: &opts}, nil

	case strings.HasPrefix(uri, "trojan://"):
		u, err := url.Parse(uri)
		if err != nil {
			return option.Outbound{}, fmt.Errorf("trojan: %w", err)
		}
		q := u.Query()
		opts, err := buildTrojanOutbound(u, q)
		if err != nil {
			return option.Outbound{}, err
		}
		return option.Outbound{Type: C.TypeTrojan, Tag: tag, Options: &opts}, nil

	case strings.HasPrefix(uri, "hysteria2://"), strings.HasPrefix(uri, "hy2://"):
		u, err := url.Parse(uri)
		if err != nil {
			return option.Outbound{}, fmt.Errorf("hysteria2: %w", err)
		}
		q := u.Query()
		opts, err := buildHysteria2Outbound(u, q)
		if err != nil {
			return option.Outbound{}, err
		}
		return option.Outbound{Type: C.TypeHysteria2, Tag: tag, Options: &opts}, nil

	case strings.HasPrefix(uri, "tuic://"):
		u, err := url.Parse(uri)
		if err != nil {
			return option.Outbound{}, fmt.Errorf("tuic: %w", err)
		}
		q := u.Query()
		opts, err := buildTUICOutbound(u, q)
		if err != nil {
			return option.Outbound{}, err
		}
		return option.Outbound{Type: C.TypeTUIC, Tag: tag, Options: &opts}, nil

	case strings.HasPrefix(uri, "socks5://"), strings.HasPrefix(uri, "socks5h://"), strings.HasPrefix(uri, "socks://"):
		u, err := url.Parse(uri)
		if err != nil {
			return option.Outbound{}, fmt.Errorf("socks: %w", err)
		}
		opts, err := buildSOCKSOutbound(u)
		if err != nil {
			return option.Outbound{}, err
		}
		return option.Outbound{Type: C.TypeSOCKS, Tag: tag, Options: &opts}, nil

	case strings.HasPrefix(uri, "http://"), strings.HasPrefix(uri, "https://"):
		u, err := url.Parse(uri)
		if err != nil {
			return option.Outbound{}, fmt.Errorf("http: %w", err)
		}
		opts, err := buildHTTPOutbound(u)
		if err != nil {
			return option.Outbound{}, err
		}
		return option.Outbound{Type: C.TypeHTTP, Tag: tag, Options: &opts}, nil

	case strings.HasPrefix(uri, "ssr://"), strings.HasPrefix(uri, "shadowsocksr://"):
		u, err := url.Parse(uri)
		if err != nil {
			return option.Outbound{}, fmt.Errorf("ssr: %w", err)
		}
		opts, err := buildShadowsocksROutbound(u)
		if err != nil {
			return option.Outbound{}, err
		}
		return option.Outbound{Type: C.TypeShadowsocksR, Tag: tag, Options: &opts}, nil

	case strings.HasPrefix(uri, "hysteria://"):
		u, err := url.Parse(uri)
		if err != nil {
			return option.Outbound{}, fmt.Errorf("hysteria: %w", err)
		}
		q := u.Query()
		opts, err := buildHysteriaOutbound(u, q)
		if err != nil {
			return option.Outbound{}, err
		}
		return option.Outbound{Type: C.TypeHysteria, Tag: tag, Options: &opts}, nil

	case strings.HasPrefix(uri, "anytls://"):
		u, err := url.Parse(uri)
		if err != nil {
			return option.Outbound{}, fmt.Errorf("anytls: %w", err)
		}
		opts, err := buildAnyTLSOutbound(u)
		if err != nil {
			return option.Outbound{}, err
		}
		return option.Outbound{Type: C.TypeAnyTLS, Tag: tag, Options: &opts}, nil

	default:
		return option.Outbound{}, fmt.Errorf("unsupported protocol: %s", uri[:min(len(uri), 10)])
	}
}

func buildVLESSOutbound(u *url.URL, q url.Values) (option.VLESSOutboundOptions, error) {
	server, port := extractServerPort(u)
	if server == "" || port == 0 {
		return option.VLESSOutboundOptions{}, fmt.Errorf("vless: missing server or port")
	}
	uuid := ""
	if u.User != nil {
		uuid = u.User.Username()
	}
	if uuid == "" {
		return option.VLESSOutboundOptions{}, fmt.Errorf("vless: missing uuid")
	}
	flow := q.Get("flow")

	opts := option.VLESSOutboundOptions{
		ServerOptions: option.ServerOptions{Server: server, ServerPort: port},
		UUID:          uuid,
		Flow:          flow,
	}

	if tlsOpts := parseTLSOptions(q, server, false); tlsOpts != nil {
		opts.TLS = tlsOpts
	}
	if transport := parseV2RayTransport(q); transport != nil {
		opts.Transport = transport
	}
	if pe := q.Get("packet_encoding"); pe != "" {
		opts.PacketEncoding = &pe
	} else if q.Get("packetAddr") == "true" || q.Get("xudp") == "true" {
		pe := "packetaddr"
		opts.PacketEncoding = &pe
	}

	return opts, nil
}

func buildVMessOutbound(uri string) (option.VMessOutboundOptions, error) {
	b64Str := uri[8:]
	if idx := strings.Index(b64Str, "?"); idx != -1 {
		b64Str = b64Str[:idx]
	}
	if idx := strings.Index(b64Str, "#"); idx != -1 {
		b64Str = b64Str[:idx]
	}
	b, err := base64.StdEncoding.DecodeString(padB64(b64Str))
	if err != nil {
		return option.VMessOutboundOptions{}, fmt.Errorf("vmess: base64 decode: %w", err)
	}
	var d map[string]any
	if err := json.Unmarshal(b, &d); err != nil {
		return option.VMessOutboundOptions{}, fmt.Errorf("vmess: json: %w", err)
	}

	server, _ := d["add"].(string)
	portStr := fmt.Sprintf("%v", d["port"])
	port, _ := strconv.Atoi(portStr)
	if server == "" || port == 0 {
		return option.VMessOutboundOptions{}, fmt.Errorf("vmess: missing server or port")
	}
	uuid, _ := d["id"].(string)
	if uuid == "" {
		return option.VMessOutboundOptions{}, fmt.Errorf("vmess: missing uuid")
	}

	opts := option.VMessOutboundOptions{
		ServerOptions: option.ServerOptions{Server: server, ServerPort: uint16(port)},
		UUID:          uuid,
		Security:      "auto",
	}

	if aidVal, ok := d["aid"]; ok {
		switch v := aidVal.(type) {
		case float64:
			opts.AlterId = int(v)
		case int:
			opts.AlterId = v
		case string:
			if n, err := strconv.Atoi(v); err == nil {
				opts.AlterId = n
			}
		}
	}

	if scy, ok := d["scy"].(string); ok && scy != "" {
		opts.Security = scy
	}

	tlsStr, _ := d["tls"].(string)
	if strings.ToLower(tlsStr) == "tls" {
		sni, _ := d["sni"].(string)
		if sni == "" {
			sni, _ = d["host"].(string)
		}
		if sni == "" {
			sni = server
		}
		tlsOpts := &option.OutboundTLSOptions{
			Enabled:    true,
			ServerName: sni,
		}
		if fp, ok := d["fp"].(string); ok && fp != "" {
			tlsOpts.UTLS = &option.OutboundUTLSOptions{Enabled: true, Fingerprint: fp}
		}
		if alpnStr, ok := d["alpn"].(string); ok && alpnStr != "" {
			tlsOpts.ALPN = strings.Split(alpnStr, ",")
		}
		if insecure, ok := d["skip-cert-verify"].(bool); ok && insecure {
			tlsOpts.Insecure = true
		} else if allowInsecure, ok := d["allowInsecure"].(string); ok && allowInsecure == "1" {
			tlsOpts.Insecure = true
		}
		opts.TLS = tlsOpts
	}

	netType, _ := d["net"].(string)
	netType = strings.ToLower(strings.TrimSpace(netType))
	if netType != "" && netType != "tcp" {
		q := url.Values{}
		q.Set("type", netType)
		if path, ok := d["path"].(string); ok && path != "" {
			q.Set("path", path)
		}
		if host, ok := d["host"].(string); ok && host != "" {
			q.Set("host", host)
		}
		if netType == "grpc" {
			if path, ok := d["path"].(string); ok && path != "" {
				q.Set("serviceName", path)
			}
		}
		opts.Transport = parseV2RayTransport(q)
	}

	return opts, nil
}

func buildShadowsocksOutbound(u *url.URL) (option.ShadowsocksOutboundOptions, error) {
	if u.User == nil || u.Hostname() == "" {
		return buildShadowsocksFragment(ssBody(u.String()))
	}

	method, password, err := decodeSSUserInfo(u.User)
	if err != nil {
		return option.ShadowsocksOutboundOptions{}, err
	}
	server, port, err := serverPort(u)
	if err != nil {
		return option.ShadowsocksOutboundOptions{}, err
	}

	opts := option.ShadowsocksOutboundOptions{
		ServerOptions: option.ServerOptions{Server: server, ServerPort: port},
		Method:        method,
		Password:      password,
	}

	if plugin := u.Query().Get("plugin"); plugin != "" {
		applySSPluginOpts(&opts, plugin)
	}

	return opts, nil
}

func buildShadowsocksFragment(body string) (option.ShadowsocksOutboundOptions, error) {
	body = ssBodyFragment(body)
	if idx := strings.Index(body, "@"); idx != -1 {
		userInfo := body[:idx]
		hp := strings.Split(body[idx+1:], ":")
		if len(hp) < 2 {
			return option.ShadowsocksOutboundOptions{}, fmt.Errorf("ss: invalid host:port")
		}
		port, _ := strconv.Atoi(strings.Split(hp[1], "#")[0])
		if port == 0 {
			return option.ShadowsocksOutboundOptions{}, fmt.Errorf("ss: invalid port")
		}
		method, password := "", ""

		if colonIdx := strings.Index(userInfo, ":"); colonIdx != -1 {
			mBytes, errM := base64.StdEncoding.DecodeString(padB64(userInfo[:colonIdx]))
			pBytes, errP := base64.StdEncoding.DecodeString(padB64(userInfo[colonIdx+1:]))
			if errM == nil && errP == nil {
				method = string(mBytes)
				password = string(pBytes)
			}
		}
		if method == "" || password == "" {
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
			return option.ShadowsocksOutboundOptions{}, fmt.Errorf("ss: cannot decode credentials")
		}
		return option.ShadowsocksOutboundOptions{
			ServerOptions: option.ServerOptions{Server: hp[0], ServerPort: uint16(port)},
			Method:        method,
			Password:      password,
		}, nil
	}
	return option.ShadowsocksOutboundOptions{}, fmt.Errorf("ss: invalid format")
}

func buildTrojanOutbound(u *url.URL, q url.Values) (option.TrojanOutboundOptions, error) {
	server, port := extractServerPort(u)
	if server == "" || port == 0 {
		return option.TrojanOutboundOptions{}, fmt.Errorf("trojan: missing server or port")
	}
	password := ""
	if u.User != nil {
		password = u.User.Username()
	}
	if password == "" {
		return option.TrojanOutboundOptions{}, fmt.Errorf("trojan: missing password")
	}

	opts := option.TrojanOutboundOptions{
		ServerOptions: option.ServerOptions{Server: server, ServerPort: port},
		Password:      password,
	}

	tlsOpts := parseTLSOptions(q, server, true)
	if tlsOpts != nil {
		opts.TLS = tlsOpts
	}
	if transport := parseV2RayTransport(q); transport != nil {
		opts.Transport = transport
	}

	return opts, nil
}

func buildHysteria2Outbound(u *url.URL, q url.Values) (option.Hysteria2OutboundOptions, error) {
	server, port := extractServerPort(u)
	if server == "" || port == 0 {
		return option.Hysteria2OutboundOptions{}, fmt.Errorf("hysteria2: missing server or port")
	}
	password := ""
	if u.User != nil {
		password = u.User.Username()
	}
	if password == "" {
		return option.Hysteria2OutboundOptions{}, fmt.Errorf("hysteria2: missing password")
	}

	opts := option.Hysteria2OutboundOptions{
		ServerOptions: option.ServerOptions{Server: server, ServerPort: port},
		Password:      password,
		UpMbps:        100,
		DownMbps:      100,
	}

	sni := firstNonEmpty(q.Get("sni"), q.Get("peer"), server)
	tlsOpts := &option.OutboundTLSOptions{
		Enabled:    true,
		ServerName: sni,
	}
	if fp := q.Get("fp"); fp != "" {
		tlsOpts.UTLS = &option.OutboundUTLSOptions{Enabled: true, Fingerprint: fp}
	}
	if q.Get("allowInsecure") == "1" || q.Get("insecure") == "1" {
		tlsOpts.Insecure = true
	}
	if alpn := q.Get("alpn"); alpn != "" {
		tlsOpts.ALPN = strings.Split(alpn, ",")
	}
	opts.TLS = tlsOpts

	if ports := firstNonEmpty(q.Get("ports"), q.Get("mport")); ports != "" {
		opts.ServerPorts = badoption.Listable[string]{ports}
	}
	if obfs := q.Get("obfs"); obfs != "" {
		opts.Obfs = &option.Hysteria2Obfs{
			Type:     obfs,
			Password: firstNonEmpty(q.Get("obfs-password"), q.Get("obfsPassword")),
		}
	}

	return opts, nil
}

func buildTUICOutbound(u *url.URL, q url.Values) (option.TUICOutboundOptions, error) {
	server, port := extractServerPort(u)
	if server == "" || port == 0 {
		return option.TUICOutboundOptions{}, fmt.Errorf("tuic: missing server or port")
	}
	uuid := ""
	password := ""
	if u.User != nil {
		uuid = u.User.Username()
		if pwd, ok := u.User.Password(); ok {
			password = pwd
		}
	}

	opts := option.TUICOutboundOptions{
		ServerOptions: option.ServerOptions{Server: server, ServerPort: port},
		UUID:          uuid,
		Password:      password,
	}

	sni := firstNonEmpty(q.Get("sni"), q.Get("peer"), server)
	tlsOpts := &option.OutboundTLSOptions{
		Enabled:    true,
		ServerName: sni,
	}
	if alpn := q.Get("alpn"); alpn != "" {
		tlsOpts.ALPN = strings.Split(alpn, ",")
	}
	if q.Get("allowInsecure") == "1" || q.Get("insecure") == "1" {
		tlsOpts.Insecure = true
	}
	opts.TLS = tlsOpts

	if cc := q.Get("congestion_control"); cc != "" {
		opts.CongestionControl = cc
	}
	if udpMode := q.Get("udp_relay_mode"); udpMode != "" {
		opts.UDPRelayMode = udpMode
	}

	return opts, nil
}

func buildSOCKSOutbound(u *url.URL) (option.SOCKSOutboundOptions, error) {
	server, port := extractServerPort(u)
	if server == "" || port == 0 {
		return option.SOCKSOutboundOptions{}, fmt.Errorf("socks: missing server or port")
	}

	opts := option.SOCKSOutboundOptions{
		ServerOptions: option.ServerOptions{Server: server, ServerPort: port},
	}

	if u.User != nil {
		opts.Username = u.User.Username()
		opts.Password, _ = u.User.Password()
	}

	return opts, nil
}

func buildHTTPOutbound(u *url.URL) (option.HTTPOutboundOptions, error) {
	server, port := extractServerPort(u)
	if server == "" || port == 0 {
		return option.HTTPOutboundOptions{}, fmt.Errorf("http: missing server or port")
	}

	opts := option.HTTPOutboundOptions{
		ServerOptions: option.ServerOptions{Server: server, ServerPort: port},
	}
	if u.Scheme == "https" {
		opts.TLS = &option.OutboundTLSOptions{
			Enabled:    true,
			ServerName: server,
		}
	}
	if u.User != nil {
		opts.Username = u.User.Username()
		opts.Password, _ = u.User.Password()
	}

	return opts, nil
}

func buildShadowsocksROutbound(u *url.URL) (option.ShadowsocksROutboundOptions, error) {
	server, port := extractServerPort(u)
	if server == "" || port == 0 {
		return option.ShadowsocksROutboundOptions{}, fmt.Errorf("ssr: missing server or port")
	}

	method, password := "", ""
	if u.User != nil {
		method = u.User.Username()
		password, _ = u.User.Password()
	}
	if method == "" || password == "" {
		return option.ShadowsocksROutboundOptions{}, fmt.Errorf("ssr: missing method or password")
	}

	q := u.Query()
	opts := option.ShadowsocksROutboundOptions{
		ServerOptions: option.ServerOptions{Server: server, ServerPort: port},
		Method:        method,
		Password:      password,
		Obfs:          q.Get("obfs"),
		ObfsParam:     q.Get("obfs_param"),
		Protocol:      q.Get("protocol"),
		ProtocolParam: q.Get("protocol_param"),
	}

	return opts, nil
}

func buildHysteriaOutbound(u *url.URL, q url.Values) (option.HysteriaOutboundOptions, error) {
	server, port := extractServerPort(u)
	if server == "" || port == 0 {
		return option.HysteriaOutboundOptions{}, fmt.Errorf("hysteria: missing server or port")
	}
	authStr := ""
	if u.User != nil {
		authStr = u.User.Username()
	}

	opts := option.HysteriaOutboundOptions{
		ServerOptions: option.ServerOptions{Server: server, ServerPort: port},
		AuthString:    authStr,
		UpMbps:        100,
		DownMbps:      100,
	}

	tlsOpts := &option.OutboundTLSOptions{
		Enabled:    true,
		ServerName: firstNonEmpty(q.Get("sni"), server),
	}
	if q.Get("allowInsecure") == "1" || q.Get("insecure") == "1" {
		tlsOpts.Insecure = true
	}
	opts.TLS = tlsOpts

	if obfs := q.Get("obfs"); obfs != "" {
		opts.Obfs = obfs
	}
	if alpn := q.Get("alpn"); alpn != "" {
		tlsOpts.ALPN = strings.Split(alpn, ",")
		opts.TLS = tlsOpts
	}

	return opts, nil
}

func buildAnyTLSOutbound(u *url.URL) (option.AnyTLSOutboundOptions, error) {
	server, port := extractServerPort(u)
	if server == "" || port == 0 {
		return option.AnyTLSOutboundOptions{}, fmt.Errorf("anytls: missing server or port")
	}
	password := ""
	if u.User != nil {
		password = u.User.Username()
	}

	opts := option.AnyTLSOutboundOptions{
		ServerOptions: option.ServerOptions{Server: server, ServerPort: port},
		Password:      password,
	}
	opts.TLS = &option.OutboundTLSOptions{
		Enabled:    true,
		ServerName: server,
	}

	return opts, nil
}

func parseTLSOptions(q url.Values, server string, forceTLS bool) *option.OutboundTLSOptions {
	sec := strings.ToLower(q.Get("security"))
	if !forceTLS && sec != "tls" && sec != "reality" {
		return nil
	}
	tlsOpts := &option.OutboundTLSOptions{
		Enabled: true,
	}
	if sec == "reality" || forceTLS || sec == "tls" {
		sni := firstNonEmpty(q.Get("sni"), q.Get("servername"), server)
		tlsOpts.ServerName = sni
	}
	if sec == "reality" {
		tlsOpts.Reality = &option.OutboundRealityOptions{
			Enabled:   true,
			PublicKey: firstNonEmpty(q.Get("pbk"), q.Get("public-key")),
			ShortID:   firstNonEmpty(q.Get("sid"), q.Get("short-id")),
		}
	}
	if !forceTLS && sec != "reality" {
		if q.Get("allowInsecure") == "1" || q.Get("insecure") == "1" {
			tlsOpts.Insecure = true
		}
	}
	if fp := firstNonEmpty(q.Get("fp"), q.Get("client-fingerprint"), q.Get("fingerprint")); fp != "" {
		tlsOpts.UTLS = &option.OutboundUTLSOptions{Enabled: true, Fingerprint: fp}
	}
	if alpn := q.Get("alpn"); alpn != "" {
		tlsOpts.ALPN = strings.Split(alpn, ",")
	}
	return tlsOpts
}

func parseV2RayTransport(q url.Values) *option.V2RayTransportOptions {
	network := q.Get("type")
	if network == "" {
		return nil
	}
	transport := &option.V2RayTransportOptions{Type: network}
	switch network {
	case "ws":
		path := q.Get("path")
		if path == "" {
			path = "/"
		}
		transport.WebsocketOptions = option.V2RayWebsocketOptions{
			Path: path,
		}
		if host := q.Get("host"); host != "" {
			transport.WebsocketOptions.Headers = badoption.HTTPHeader{
				"Host": {host},
			}
		}
	case "grpc":
		if serviceName := q.Get("serviceName"); serviceName != "" {
			transport.GRPCOptions = option.V2RayGRPCOptions{
				ServiceName: serviceName,
			}
		}
	case "http", "httpupgrade":
		path := q.Get("path")
		if path == "" {
			path = "/"
		}
		transport.HTTPUpgradeOptions = option.V2RayHTTPUpgradeOptions{
			Host: q.Get("host"),
			Path: path,
		}
	}
	return transport
}

func extractServerPort(u *url.URL) (string, uint16) {
	hostname := u.Hostname()
	port, _ := strconv.Atoi(u.Port())
	if port == 0 {
		port = 443
	}
	return hostname, uint16(port)
}

func serverPort(u *url.URL) (string, uint16, error) {
	hostname := u.Hostname()
	port, err := strconv.Atoi(u.Port())
	if err != nil || port == 0 {
		return "", 0, fmt.Errorf("invalid port")
	}
	return hostname, uint16(port), nil
}

func ssBody(uri string) string {
	body := uri[5:]
	if idx := strings.Index(body, "#"); idx != -1 {
		body = body[:idx]
	}
	return body
}

func ssBodyFragment(body string) string {
	if idx := strings.Index(body, "#"); idx != -1 {
		body = body[:idx]
	}
	return body
}

func applySSPluginOpts(opts *option.ShadowsocksOutboundOptions, pluginRaw string) {
	pluginRaw = strings.TrimSpace(pluginRaw)
	if pluginRaw == "" {
		return
	}
	segments := strings.Split(pluginRaw, ";")
	opts.Plugin = strings.ToLower(strings.TrimSpace(segments[0]))
	if len(segments) > 1 {
		opts.PluginOptions = strings.Join(segments[1:], ";")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func padB64(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "-", "+"), "_", "/")
	if pad := len(s) % 4; pad != 0 {
		s += strings.Repeat("=", 4-pad)
	}
	return s
}
