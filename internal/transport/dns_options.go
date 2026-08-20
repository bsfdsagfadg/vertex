package transport

import (
	"net"
	"net/url"
	"strconv"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
)

// defaultEntryDoHURL 是非 ECH 节点与 ech 参数未携带 DoH 地址时的兜底 DoH（国内可达）。
const defaultEntryDoHURL = "https://dns.alidns.com/dns-query"

// defaultBootstrapDNS 是 DoH 服务器域名的解析兜底（对齐 v2rayN「Bootstrap DNS 建议用 IP」语义）：
// 直连阿里公共 DNS UDP 53，绕开系统 DNS——实测本机系统 DNS 对 dns.alidns.com 的 A 记录
// 定向返回空响应（AAAA 正常），导致 DoH 拨号仅拿到 IPv6 地址而失败。
const defaultBootstrapDNS = "223.5.5.5"

// dnsOptionsForNode 为任意节点构建 box 级 DNS 段（对齐 v2rayN 全局 DNS 语义）：
//   - 节点无 TLS（或 TLS 未启用）时返回 nil（保持系统 DNS 现状）；
//   - 注入 https DoH（ECH 节点用 URI ConfigURL，否则默认 alidns）；
//   - 附加 UDP bootstrap（223.5.5.5 公共 DNS，直连解析 DoH 服务器域名，杜绝自查询循环与本地污染）；
//   - DoH URL 的 path 与 query 原样透传（带 token 等参数的私有 DoH 可正常工作）；
//   - DoH 服务器为 IP 时返回 nil（无法携带 SNI，TLS 证书校验必然失败）；
//   - Final 指向 DoH、Strategy 固定 prefer_ipv4。
//
// 返回 nil 的四种情况：节点为 nil / 无 TLS / TLS 未启用 / DoH 地址非法（非 https、host 为空或为 IP）。
func dnsOptionsForNode(n *ParsedNode) *option.DNSOptions {
	if n == nil || n.TLS == nil || !n.TLS.Enabled {
		return nil
	}
	doh := defaultEntryDoHURL
	if n.TLS.ECH != nil && n.TLS.ECH.PublicName != "" && n.TLS.ECH.ConfigURL != "" {
		doh = n.TLS.ECH.ConfigURL
	}
	u, err := url.Parse(doh)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || net.ParseIP(u.Hostname()) != nil {
		return nil
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	portNum, _ := strconv.Atoi(port)
	path := u.Path
	if path == "" || path == "/" {
		path = "/dns-query"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	httpsOpts := &option.RemoteHTTPSDNSServerOptions{
		RemoteTLSDNSServerOptions: option.RemoteTLSDNSServerOptions{
			RemoteDNSServerOptions: option.RemoteDNSServerOptions{
				DNSServerAddressOptions: option.DNSServerAddressOptions{
					Server:     u.Hostname(),
					ServerPort: uint16(portNum),
				},
			},
		},
		Path: path,
	}
	httpsOpts.DomainResolver = &option.DomainResolveOptions{Server: "bootstrap"}
	return &option.DNSOptions{
		RawDNSOptions: option.RawDNSOptions{
			Servers: []option.DNSServerOptions{
				{
					Type: C.DNSTypeUDP,
					Tag:  "bootstrap",
					Options: &option.RemoteDNSServerOptions{
						DNSServerAddressOptions: option.DNSServerAddressOptions{
							Server:     defaultBootstrapDNS,
							ServerPort: 53,
						},
					},
				},
				{
					Type:    C.DNSTypeHTTPS,
					Tag:     "doh",
					Options: httpsOpts,
				},
			},
			Final: "doh",
			DNSClientOptions: option.DNSClientOptions{
				Strategy: option.DomainStrategy(C.DomainStrategyPreferIPv4),
			},
		},
	}
}
