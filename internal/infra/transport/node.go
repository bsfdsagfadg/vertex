package transport

// ParsedNode 是代理节点的中间表示（IR）：URI 解析的唯一产物。
// 导入期解析一次并缓存，展示/去重/拨号全部读 IR。
// RawURI 是身份键，与 exitpool.Node.RawURI 严格一致（不 trim、不归一）。
type ParsedNode struct {
	RawURI string // 身份键，与 exitpool.Node.RawURI 相同
	Type   string // vless/vmess/ss/trojan/hysteria2/hysteria/tuic/socks5/http/ssr/anytls
	Name   string
	Server string
	Port   int

	UUID     string // vless/vmess/tuic
	Password string // trojan/hy2/tuic(userinfo 的 password 部分)/ss/anytls
	Username string // socks/http（无密码时留空）
	Cipher   string // ss/ssr（ss 已归一）/vmess（恒 "auto"）
	Security string // vmess scy，默认 "auto"
	AlterID  int    // vmess

	// SOCKSVersion 仅 socks 使用：socks4://→"4"、socks4a://→"4a"、socks5/socks5h/socks→"5"。
	// sing-box 出站 Version 选项原样透传（SOCKSOutboundOptions.Version）。
	SOCKSVersion string

	// SSHPrivateKey / SSHPrivateKeyPassphrase 仅 ssh 使用（v2rayN 分享格式 pk/psk 参数）。
	SSHPrivateKey           string
	SSHPrivateKeyPassphrase string

	TLS       *TLSOptions
	Transport *TransportOptions // nil=裸 TCP；xhttp 等保留原始 Type 供 capability 判定

	Flow              string   // vless/trojan
	PacketEncoding    string   // vless（packet_encoding 或 packetAddr/xudp=true→"packetaddr"）
	ServerPorts       []string // hysteria2（归一为 "a:b" 格式）
	Obfs              string   // hysteria/hy2
	ObfsPassword      string   // hy2
	AuthString        string   // hysteria
	CongestionControl string   // tuic
	UDPRelayMode      string   // tuic
	Plugin            string   // ss（小写原始插件名）
	PluginOptions     string   // ss（分段 join(";")）
	Protocol          string   // ssr
	ProtocolParam     string   // ssr
	ObfsParam         string   // ssr

	Supported         bool   // 能力标注，解析尾部统一填充
	UnsupportedReason string // 如 "transport xhttp not supported by sing-box"
}

type TLSOptions struct {
	Enabled     bool
	ServerName  string // sni
	Insecure    bool
	ALPN        []string
	Fingerprint string // UTLS 指纹；Reality 无显式 fp 时补 "chrome"
	Reality     *RealityOptions
	ECH         *ECHOptions // v2rayN 风格 ech 参数（Encrypted Client Hello）
}

// ECHOptions 记录 v2rayN 风格 ech 参数（Encrypted Client Hello）。
// PublicName 为 ECH 公钥名（TLS 外层 SNI）；ConfigURL 为 ECH 配置拉取用的
// DoH 地址（"" 表示 URI 未提供，注入 DNS 段时使用默认 DoH）。
type ECHOptions struct {
	PublicName string
	ConfigURL  string
}

type RealityOptions struct {
	PublicKey string
	ShortID   string
}

type TransportOptions struct {
	Type                string // ws/grpc/http/httpupgrade/quic；tcp/none/raw/tcpheader→解析期置 nil
	Path                string
	Host                string
	Headers             map[string][]string
	ServiceName         string // grpc
	Method              string // http
	MaxEarlyData        uint32 // ws 0-RTT early data
	EarlyDataHeaderName string // ws early data header
}

// sing-box v1.13.19（go.mod 锁定）实际支持的 V2Ray 传输类型。
// 升级 sing-box 需复核该清单。
var supportedTransports = map[string]bool{
	"ws": true, "grpc": true, "http": true, "httpupgrade": true, "quic": true,
}

// 等价裸 TCP，解析期降级为 Transport=nil。
var tcpAliases = map[string]bool{
	"tcp": true, "none": true, "raw": true, "tcpheader": true, "": true,
}

// applyCapability 是全局唯一 capability 判定点（各解析器尾部调用）。
// capability 只判 transport 层：协议层 sing-box v1.13.19 全支持。
func applyCapability(n *ParsedNode) {
	if n.Transport == nil {
		n.Supported = true
		return
	}
	if supportedTransports[n.Transport.Type] {
		n.Supported = true
		return
	}
	n.Supported = false
	n.UnsupportedReason = "transport " + n.Transport.Type + " not supported by sing-box"
}
