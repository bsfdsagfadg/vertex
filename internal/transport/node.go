package transport

import "sync"

// ParsedNode 是代理节点的中间表示（IR）：URI 解析的唯一产物。
// 导入期解析一次并缓存，展示/去重/拨号全部读 IR。
// RawURI 是身份键，与 nodes.Node.RawURI 严格一致（不 trim、不归一）。
type ParsedNode struct {
	RawURI string // 身份键，与 nodes.Node.RawURI 相同
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

// sing-box v1.13.14（go.mod 锁定）实际支持的 V2Ray 传输类型。
// 升级 sing-box 需复核该清单。
var supportedTransports = map[string]bool{
	"ws": true, "grpc": true, "http": true, "httpupgrade": true, "quic": true,
}

// 等价裸 TCP，解析期降级为 Transport=nil。
var tcpAliases = map[string]bool{
	"tcp": true, "none": true, "raw": true, "tcpheader": true, "": true,
}

// applyCapability 是全局唯一 capability 判定点（各解析器尾部调用）。
// capability 只判 transport 层：协议层 sing-box v1.13.14 全支持。
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

var (
	irCache   = make(map[string]*ParsedNode)
	irCacheMu sync.RWMutex
)

// irCacheMax 缓存条目上限：缓存仅解析成功的合法 URI（受节点总数与导入限额约束），
// 上限为纯防御，防恶意海量不重复 URI 注入导致无界增长；超限时整体重建（缓存仅是性能优化）。
const irCacheMax = 16384

// CacheParsedNode 导入时预热；n 为 nil 时不缓存。
func CacheParsedNode(n *ParsedNode) {
	if n == nil {
		return
	}
	irCacheMu.Lock()
	if len(irCache) >= irCacheMax {
		irCache = make(map[string]*ParsedNode)
	}
	irCache[n.RawURI] = n
	irCacheMu.Unlock()
}

// ClearParsedNodeCache 全量清空解析缓存（幂等），供全量重置/测试隔离使用。
func ClearParsedNodeCache() {
	irCacheMu.Lock()
	irCache = make(map[string]*ParsedNode)
	irCacheMu.Unlock()
}

// InvalidateParsedNode 节点删除时清理（api 层经 nodes.DeleteNodeCallback 注册调用）。
func InvalidateParsedNode(uri string) {
	irCacheMu.Lock()
	delete(irCache, uri)
	irCacheMu.Unlock()
}

// GetOrParse 优先读缓存，未命中则解析并缓存。只缓存成功结果（C16），失败每次重试。
func GetOrParse(uri string) (*ParsedNode, error) {
	irCacheMu.RLock()
	n := irCache[uri]
	irCacheMu.RUnlock()
	if n != nil {
		return n, nil
	}
	parsed, err := ParseURI(uri)
	if err != nil {
		return nil, err
	}
	CacheParsedNode(parsed)
	return parsed, nil
}

// peekCache 测试辅助：同包读取缓存（不导出）。
func peekCache(uri string) *ParsedNode {
	irCacheMu.RLock()
	defer irCacheMu.RUnlock()
	return irCache[uri]
}
