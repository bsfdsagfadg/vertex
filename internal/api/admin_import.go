package api

import (
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
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

	out, err := transport.ParseURI(raw)
	if err != nil {
		return nodes.Node{}, false
	}

	nodeType := strings.TrimSpace(valueToString(out["type"]))
	if nodeType == "" {
		return nodes.Node{}, false
	}

	nodeName := extractImportedNodeName(raw, out)
	if nodeName == "" {
		nodeName = raw[:min(len(raw), 40)]
	}
	return nodes.Node{Type: nodeType, Name: nodeName, RawURI: raw}, true
}

func extractImportedNodeName(raw string, out map[string]any) string {
	if name := strings.TrimSpace(valueToString(out["name"])); name != "" {
		return name
	}

	if strings.HasPrefix(raw, "vmess://") {
		b64Str := raw[8:]
		if idx := strings.Index(b64Str, "?"); idx != -1 {
			b64Str = b64Str[:idx]
		}
		if idx := strings.Index(b64Str, "#"); idx != -1 {
			b64Str = b64Str[:idx]
		}
		b64Str = strings.ReplaceAll(strings.ReplaceAll(b64Str, "-", "+"), "_", "/")
		if pad := len(b64Str) % 4; pad != 0 {
			b64Str += strings.Repeat("=", 4-pad)
		}
		if b, err := base64.StdEncoding.DecodeString(b64Str); err == nil {
			var d map[string]any
			if errUnm := json.Unmarshal(b, &d); errUnm == nil {
				if ps, ok := d["ps"].(string); ok {
					return strings.TrimSpace(ps)
				}
			}
		}
	}

	if idx := strings.Index(raw, "#"); idx != -1 {
		escapedName := raw[idx+1:]
		if dec, err := url.QueryUnescape(escapedName); err == nil {
			return strings.TrimSpace(dec)
		}
		return strings.TrimSpace(escapedName)
	}

	return ""
}

func parseFlexibleImportedNodeLine(line string) (nodes.Node, bool) {
	if node, ok := parseImportedNodeLine(line); ok {
		return node, true
	}
	return parseV2RayNNodeLine(line)
}

func (adm *AdminHandler) adminImportNodes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text      string `json:"text"`
		Replace   bool   `json:"replace"`
		SingleURI bool   `json:"single_uri"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	if body.SingleURI {
		// Strict single-node mode is intentionally separate from subscription/file
		// import: reject multiline payloads and URL-shaped subscription sources.
		text := strings.TrimSpace(body.Text)
		if body.Replace {
			writeJSON(w, http.StatusBadRequest, adminErr("单节点导入不允许 replace=true"))
			return
		}
		if text == "" {
			writeJSON(w, http.StatusBadRequest, adminErr("请提供一个节点 URI"))
			return
		}
		if strings.ContainsAny(text, "\r\n") {
			writeJSON(w, http.StatusBadRequest, adminErr("一次只能导入一个节点；多节点内容请前往订阅管理"))
			return
		}
		lower := strings.ToLower(text)
		if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
			writeJSON(w, http.StatusBadRequest, adminErr("订阅 URL 请前往订阅管理；单节点导入仅接受代理 URI"))
			return
		}
		node, ok := parseFlexibleImportedNodeLine(text)
		if !ok || strings.TrimSpace(node.RawURI) == "" {
			writeJSON(w, http.StatusBadRequest, adminErr("格式不支持或不是完整节点 URI"))
			return
		}
		if configNodeExists(node.RawURI) {
			writeJSON(w, http.StatusConflict, adminErr("节点已存在"))
			return
		}
		if err := nodes.ImportManualNodes([]nodes.Node{node}, false); err != nil {
			writeJSON(w, http.StatusInternalServerError, adminErr("导入节点失败"))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": 1})
		return
	}
	log.Printf("[Admin] [ImportNodes] 收到优选节点文件导入请求, 替换模式: %v", body.Replace)

	newNodes := parseImportedNodes(strings.TrimSpace(body.Text))
	log.Printf("[Admin] [ImportNodes] 正在合并导入的新节点数量: %d", len(newNodes))
	if err := nodes.ImportManualNodes(newNodes, body.Replace); err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("导入节点失败: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": len(newNodes)})
}

func configNodeExists(rawURI string) bool {
	for _, n := range nodes.LoadNodes() {
		if strings.TrimSpace(n.RawURI) == strings.TrimSpace(rawURI) {
			return true
		}
	}
	return false
}

func (adm *AdminHandler) adminImportNodesJson(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text    string `json:"text"`
		Replace bool   `json:"replace"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	log.Printf("[Admin] [ImportNodesJson] 收到旧版 nodes.json 导入请求, 替换模式: %v", body.Replace)

	var d struct {
		Nodes []nodes.Node `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(body.Text), &d); err != nil {
		writeJSON(w, http.StatusBadRequest, adminErr("JSON 解析失败: "+err.Error()))
		return
	}

	log.Printf("[Admin] [ImportNodesJson] 正在合并导入的新节点数量: %d", len(d.Nodes))
	if err := nodes.ImportManualNodes(d.Nodes, body.Replace); err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("导入旧版节点失败: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": len(d.Nodes)})
}
