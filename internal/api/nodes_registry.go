package api

import (
	"strconv"

	"github.com/bsfdsagfadg/vertex/internal/entrynodes"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

// InitRegistry 注册节点与前置代理节点的删除缓存失效、能力校验与去重身份注入：
// - DeleteNodeCallback / DeleteNodesBatchCallback 覆盖节点删除路径，联动失效 transport 解析缓存；
// - ResetStateCallback / ResetEntryState 联动清空 transport 解析缓存，规避循环依赖；
// - NodeIdentityFunc / EntryIdentityFunc 让节点经 IR 计算去重键（type://cred@server:port）；
// - IsSupportedFunc / EntryIsSupportedFunc 查询能力标注，过滤不支持的节点。
// 须在任何节点/前置节点池操作之前调用（幂等，可重复调用）。
func InitRegistry() {
	nodes.DeleteNodeCallback = transport.InvalidateParsedNode
	nodes.DeleteNodesBatchCallback = transport.InvalidateParsedNodesBatch
	nodes.ResetStateCallback = transport.ClearParsedNodeCache
	nodes.NodeIdentityFunc = nodeIdentityFromIR
	nodes.IsSupportedFunc = func(rawURI string) bool {
		pn, err := transport.GetOrParse(rawURI)
		return err == nil && pn != nil && pn.Supported
	}

	entrynodes.EntryDeleteCallback = transport.InvalidateParsedNode
	entrynodes.EntryIdentityFunc = nodeIdentityFromIR
	entrynodes.EntryIsSupportedFunc = func(rawURI string) bool {
		pn, err := transport.GetOrParse(rawURI)
		return err == nil && pn != nil && pn.Supported
	}
}

// nodeIdentityFromIR 从 IR 计算去重键（type://cred@server:port），语义对齐旧 parseNodeIdentity。
// 解析失败时返回 (rawURI, false)，DedupNodes 回退 rawURI 键（与现状一致）。
func nodeIdentityFromIR(rawURI string) (string, bool) {
	n, err := transport.GetOrParse(rawURI)
	if err != nil || n == nil {
		return rawURI, false
	}
	return n.Type + "://" + credIdentity(n) + "@" + n.Server + ":" + strconv.Itoa(n.Port), true
}

func credIdentity(n *transport.ParsedNode) string {
	switch n.Type {
	case "vless", "vmess", "tuic":
		return n.UUID
	case "ss", "ssr":
		return n.Cipher + ":" + n.Password
	case "trojan", "hysteria2", "anytls":
		return n.Password
	case "socks5", "http", "ssh":
		return n.Username + ":" + n.Password
	case "hysteria":
		return n.AuthString
	default:
		return ""
	}
}
