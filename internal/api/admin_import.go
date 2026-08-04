package api

import (
	"log"
	"net/http"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

const subscriptionFetchUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

func parseImportedNodes(text string) []nodes.Node {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	normalized := maybeDecodeSubscriptionText(text)
	if imported := parseClashYAMLToNodes(normalized); len(imported) > 0 {
		return imported
	}
	if imported := parseJSONImportedNodes(normalized); len(imported) > 0 {
		return imported
	}

	var imported []nodes.Node
	for _, line := range strings.Split(normalized, "\n") {
		if node, ok := parseFlexibleImportedNodeLine(line); ok {
			imported = append(imported, node)
		}
	}
	return imported
}

func maybeDecodeSubscriptionText(text string) string {
	b, err := decodeSubBase64(text)
	if err != nil {
		return text
	}

	decoded := strings.TrimSpace(string(b))
	if decoded == "" {
		return text
	}
	if strings.Contains(decoded, "proxies:") || hasImportableNodeLine(decoded) || len(parseJSONImportedNodes(decoded)) > 0 {
		return decoded
	}
	return text
}

func hasImportableNodeLine(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if _, ok := parseFlexibleImportedNodeLine(line); ok {
			return true
		}
	}
	return false
}

func parseImportedNodeLine(line string) (nodes.Node, bool) {
	raw := strings.TrimSpace(line)
	if raw == "" {
		return nodes.Node{}, false
	}

	pn, err := transport.ParseURI(raw)
	if err != nil || pn == nil {
		return nodes.Node{}, false
	}
	transport.CacheParsedNode(pn) // 导入期预热 IR 缓存

	nodeType := strings.TrimSpace(pn.Type)
	if nodeType == "" {
		return nodes.Node{}, false
	}

	nodeName := strings.TrimSpace(pn.Name)
	if nodeName == "" {
		nodeName = raw[:min(len(raw), 40)]
	}
	disabled := !pn.Supported
	if !pn.Supported {
		reason := "unsupported: " + pn.UnsupportedReason
		nodes.RecordTest(raw, false, 0, reason)
	}

	return nodes.Node{Type: nodeType, Name: nodeName, RawURI: raw, Disabled: disabled}, true
}

func parseFlexibleImportedNodeLine(line string) (nodes.Node, bool) {
	if node, ok := parseImportedNodeLine(line); ok {
		return node, true
	}
	return parseV2RayNNodeLine(line)
}

func (adm *AdminHandler) adminImportNodes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text    string `json:"text"`
		Replace bool   `json:"replace"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	log.Printf("[Admin] [ImportNodes] 收到优选节点文件导入请求, 替换模式: %v", body.Replace)

	newNodes := parseImportedNodes(strings.TrimSpace(body.Text))
	if body.Replace {
		log.Printf("[Admin] [ImportNodes] 替换模式，正在清除全部已有候选节点")
		for _, cn := range nodes.LoadNodes() {
			nodes.DeleteNode(cn.RawURI)
		}
	}

	log.Printf("[Admin] [ImportNodes] 正在合并导入的新节点数量: %d", len(newNodes))
	nodes.MergeNodes(newNodes)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": len(newNodes)})
}


