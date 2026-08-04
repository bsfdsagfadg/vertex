package api

import (
	"strconv"

	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

// init 注册节点删除缓存失效与去重身份注入：
// - DeleteNodeCallback 一处覆盖四个删除路径（DeleteNode/DedupNodes/DeleteDisabled/BatchDeleteNodes）；
// - ResetStateCallback 让全量重置联动清空 transport 解析缓存，规避 nodes→transport 循环依赖；
// - NodeIdentityFunc 让 nodes 包经 IR 计算去重键，规避 nodes→transport 循环依赖；
// - IsSupportedFunc 查询能力标注，使不可用/不支持节点自动被 SelectForParallel 过滤并标记禁用。
func init() {
	nodes.DeleteNodeCallback = transport.InvalidateParsedNode
	nodes.ResetStateCallback = transport.ClearParsedNodeCache
	nodes.NodeIdentityFunc = nodeIdentityFromIR
	nodes.IsSupportedFunc = func(rawURI string) bool {
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
