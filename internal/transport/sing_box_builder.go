package transport

import (
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
)

// buildOutbound 薄壳：解析（命中 IR 缓存）→ capability 早失败 → 从 IR 构建。
// dialer 三处调用点签名不变（startEntryBox/newSecondHopBox/TestEntryProxy），透明命中缓存。
func buildOutbound(uri string) (option.Outbound, error) {
	n, err := GetOrParse(uri)
	if err != nil {
		return option.Outbound{}, err
	}
	if !n.Supported {
		return option.Outbound{}, fmt.Errorf("unsupported: %s", n.UnsupportedReason)
	}
	return buildOutboundFromNode(n)
}

// buildOutboundFromNode 从 ParsedNode IR 构建 outbound，不触碰 URI 字符串。
// 各协议凭证校验对齐旧 buildXxxOutbound 行为（仅校验，不重新解析）。
func buildOutboundFromNode(n *ParsedNode) (option.Outbound, error) {
	if n == nil {
		return option.Outbound{}, fmt.Errorf("nil parsed node")
	}
	if n.Server == "" || n.Port <= 0 {
		return option.Outbound{}, fmt.Errorf("%s: missing server or port", n.Type)
	}
	h := sha256.Sum256([]byte(n.RawURI))
	tag := fmt.Sprintf("node-%x", h[:8])

	var outType string
	var outOpts any
	switch n.Type {
	case "vless":
		if n.UUID == "" {
			return option.Outbound{}, fmt.Errorf("vless: missing uuid")
		}
		outType = C.TypeVLESS
		opts := option.VLESSOutboundOptions{
			ServerOptions: option.ServerOptions{Server: n.Server, ServerPort: uint16(n.Port)},
			UUID:          n.UUID,
			Flow:          n.Flow,
		}
		if n.PacketEncoding != "" {
			pe := n.PacketEncoding
			opts.PacketEncoding = &pe
		}
		opts.TLS = tlsFromIR(n.TLS)
		opts.Transport = transportFromIR(n.Transport)
		outOpts = &opts

	case "vmess":
		if n.UUID == "" {
			return option.Outbound{}, fmt.Errorf("vmess: missing uuid")
		}
		security := n.Security
		if security == "" {
			security = "auto"
		}
		outType = C.TypeVMess
		opts := option.VMessOutboundOptions{
			ServerOptions: option.ServerOptions{Server: n.Server, ServerPort: uint16(n.Port)},
			UUID:          n.UUID,
			Security:      security,
			AlterId:       n.AlterID,
		}
		opts.TLS = tlsFromIR(n.TLS)
		opts.Transport = transportFromIR(n.Transport)
		outOpts = &opts

	case "ss":
		if n.Cipher == "" || n.Password == "" {
			return option.Outbound{}, fmt.Errorf("ss: missing method or password")
		}
		outType = C.TypeShadowsocks
		opts := option.ShadowsocksOutboundOptions{
			ServerOptions: option.ServerOptions{Server: n.Server, ServerPort: uint16(n.Port)},
			Method:        n.Cipher,
			Password:      n.Password,
		}
		if n.Plugin != "" {
			opts.Plugin = n.Plugin
			opts.PluginOptions = n.PluginOptions
		}
		outOpts = &opts

	case "trojan":
		if n.Password == "" {
			return option.Outbound{}, fmt.Errorf("trojan: missing password")
		}
		outType = C.TypeTrojan
		opts := option.TrojanOutboundOptions{
			ServerOptions: option.ServerOptions{Server: n.Server, ServerPort: uint16(n.Port)},
			Password:      n.Password,
		}
		opts.TLS = tlsFromIR(n.TLS)
		opts.Transport = transportFromIR(n.Transport)
		outOpts = &opts

	case "hysteria2":
		if n.Password == "" {
			return option.Outbound{}, fmt.Errorf("hysteria2: missing password")
		}
		outType = C.TypeHysteria2
		opts := option.Hysteria2OutboundOptions{
			ServerOptions: option.ServerOptions{Server: n.Server, ServerPort: uint16(n.Port)},
			Password:      n.Password,
			UpMbps:        100,
			DownMbps:      100,
			ServerPorts:   badoption.Listable[string](n.ServerPorts),
		}
		if n.Obfs != "" {
			opts.Obfs = &option.Hysteria2Obfs{
				Type:     n.Obfs,
				Password: n.ObfsPassword,
			}
		}
		opts.TLS = tlsFromIR(n.TLS)
		outOpts = &opts

	case "tuic":
		outType = C.TypeTUIC
		opts := option.TUICOutboundOptions{
			ServerOptions: option.ServerOptions{Server: n.Server, ServerPort: uint16(n.Port)},
			UUID:          n.UUID,
			Password:      n.Password,
		}
		if n.CongestionControl != "" {
			opts.CongestionControl = n.CongestionControl
		}
		if n.UDPRelayMode != "" {
			opts.UDPRelayMode = n.UDPRelayMode
		}
		opts.TLS = tlsFromIR(n.TLS)
		outOpts = &opts

	case "socks5":
		outType = C.TypeSOCKS
		opts := option.SOCKSOutboundOptions{
			ServerOptions: option.ServerOptions{Server: n.Server, ServerPort: uint16(n.Port)},
		}
		if n.Password != "" {
			opts.Username = n.Username
			opts.Password = n.Password
		}
		if n.SOCKSVersion != "" && n.SOCKSVersion != "5" {
			opts.Version = n.SOCKSVersion
		}
		outOpts = &opts

	case "http":
		outType = C.TypeHTTP
		opts := option.HTTPOutboundOptions{
			ServerOptions: option.ServerOptions{Server: n.Server, ServerPort: uint16(n.Port)},
		}
		opts.TLS = tlsFromIR(n.TLS)
		if n.Password != "" {
			opts.Username = n.Username
			opts.Password = n.Password
		}
		outOpts = &opts

	case "ssr":
		if n.Cipher == "" || n.Password == "" {
			return option.Outbound{}, fmt.Errorf("ssr: missing method or password")
		}
		outType = C.TypeShadowsocksR
		opts := option.ShadowsocksROutboundOptions{
			ServerOptions: option.ServerOptions{Server: n.Server, ServerPort: uint16(n.Port)},
			Method:        n.Cipher,
			Password:      n.Password,
			Obfs:          n.Obfs,
			ObfsParam:     n.ObfsParam,
			Protocol:      n.Protocol,
			ProtocolParam: n.ProtocolParam,
		}
		outOpts = &opts

	case "hysteria":
		outType = C.TypeHysteria
		opts := option.HysteriaOutboundOptions{
			ServerOptions: option.ServerOptions{Server: n.Server, ServerPort: uint16(n.Port)},
			AuthString:    n.AuthString,
			UpMbps:        100,
			DownMbps:      100,
			Obfs:          n.Obfs,
		}
		opts.TLS = tlsFromIR(n.TLS)
		outOpts = &opts

	case "anytls":
		outType = C.TypeAnyTLS
		opts := option.AnyTLSOutboundOptions{
			ServerOptions: option.ServerOptions{Server: n.Server, ServerPort: uint16(n.Port)},
			Password:      n.Password,
		}
		opts.TLS = tlsFromIR(n.TLS)
		outOpts = &opts

	case "ssh":
		outType = C.TypeSSH
		opts := option.SSHOutboundOptions{
			ServerOptions: option.ServerOptions{Server: n.Server, ServerPort: uint16(n.Port)},
			User:          n.Username,
			Password:      n.Password,
		}
		if n.SSHPrivateKey != "" {
			opts.PrivateKey = badoption.Listable[string]{n.SSHPrivateKey}
		}
		if n.SSHPrivateKeyPassphrase != "" {
			opts.PrivateKeyPassphrase = n.SSHPrivateKeyPassphrase
		}
		outOpts = &opts

	default:
		return option.Outbound{}, fmt.Errorf("unsupported node type: %s", n.Type)
	}

	return option.Outbound{Type: outType, Tag: tag, Options: outOpts}, nil
}

// tlsFromIR 从 IR 的 TLSOptions 转换。Reality 指纹已在解析期补齐 chrome，此处不再兜底。
func tlsFromIR(t *TLSOptions) *option.OutboundTLSOptions {
	if t == nil || !t.Enabled {
		return nil
	}
	o := &option.OutboundTLSOptions{Enabled: true, ServerName: t.ServerName}
	if t.Insecure {
		o.Insecure = true
	}
	if len(t.ALPN) > 0 {
		o.ALPN = t.ALPN
	}
	if t.Fingerprint != "" {
		o.UTLS = &option.OutboundUTLSOptions{Enabled: true, Fingerprint: t.Fingerprint}
	}
	if t.Reality != nil {
		o.Reality = &option.OutboundRealityOptions{
			Enabled:   true,
			PublicKey: t.Reality.PublicKey,
			ShortID:   t.Reality.ShortID,
		}
	}
	return o
}

// transportFromIR 从 IR 的 TransportOptions 转换。
// tcp 类在解析期已置 nil；xhttp 类在薄壳 !Supported 早失败，此处只需处理 sing-box 支持的类型。
func transportFromIR(t *TransportOptions) *option.V2RayTransportOptions {
	if t == nil || t.Type == "" {
		return nil
	}
	tr := &option.V2RayTransportOptions{Type: t.Type}
	switch t.Type {
	case "ws":
		path := t.Path
		if path == "" {
			path = "/"
		}
		tr.WebsocketOptions = option.V2RayWebsocketOptions{Path: path}
		if t.Host != "" {
			tr.WebsocketOptions.Headers = badoption.HTTPHeader{"Host": {t.Host}}
		}
	case "grpc":
		if t.ServiceName != "" {
			tr.GRPCOptions = option.V2RayGRPCOptions{ServiceName: t.ServiceName}
		}
	case "http":
		path := t.Path
		if path == "" {
			path = "/"
		}
		tr.HTTPOptions = option.V2RayHTTPOptions{Path: path, Method: t.Method}
		if t.Host != "" {
			tr.HTTPOptions.Host = badoption.Listable[string]{t.Host}
		}
	case "httpupgrade":
		path := t.Path
		if path == "" {
			path = "/"
		}
		tr.HTTPUpgradeOptions = option.V2RayHTTPUpgradeOptions{Path: path, Host: t.Host}
	case "quic":
		tr.QUICOptions = option.V2RayQUICOptions{}
	}
	return tr
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// parseHysteria2Ports 将订阅源中的端口范围写法（如 "50000-53000"、"30000,30002"）
// 转换为 sing-box ServerPorts 接受的冒号范围格式（如 "50000:53000"）。
// 无法解析时返回 false，调用方应回退使用主端口 ServerPort。
func parseHysteria2Ports(raw string) (badoption.Listable[string], bool) {
	items := strings.Split(raw, ",")
	var out badoption.Listable[string]
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if strings.Contains(item, ":") {
			if validPortRange(item) {
				out = append(out, item)
			}
			continue
		}
		if dashIdx := strings.Index(item, "-"); dashIdx != -1 {
			start, err1 := strconv.Atoi(strings.TrimSpace(item[:dashIdx]))
			end, err2 := strconv.Atoi(strings.TrimSpace(item[dashIdx+1:]))
			if err1 == nil && err2 == nil && start >= 1 && start <= 65535 && end >= start && end <= 65535 {
				out = append(out, fmt.Sprintf("%d:%d", start, end))
			}
			continue
		}
		if p, err := strconv.Atoi(item); err == nil && p >= 1 && p <= 65535 {
			out = append(out, fmt.Sprintf("%d:%d", p, p))
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

func validPortRange(s string) bool {
	startStr, endStr, _ := strings.Cut(s, ":")
	start, err1 := strconv.Atoi(startStr)
	end, err2 := strconv.Atoi(endStr)
	if err1 != nil || err2 != nil {
		return false
	}
	return start >= 1 && start <= 65535 && end >= start && end <= 65535
}

func normalizeSSMethod(method string) string {
	switch strings.ToLower(strings.TrimSpace(method)) {
	case "chacha20-poly1305", "chacha20poly1305", "chacha20-ietf":
		return "chacha20-ietf-poly1305"
	case "aes-128-poly1305":
		return "aes-128-gcm"
	case "aes-256-poly1305":
		return "aes-256-gcm"
	}
	return method
}

func padB64(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "-", "+"), "_", "/")
	if pad := len(s) % 4; pad != 0 {
		s += strings.Repeat("=", 4-pad)
	}
	return s
}
