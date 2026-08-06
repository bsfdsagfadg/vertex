package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

const subscriptionFetchUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// ImportFail 记录一条未被成功导入的节点行的原因。Line 截断到 120 字符防超长。
type ImportFail struct {
	Line   string `json:"line"`
	Type   string `json:"type,omitempty"`
	Name   string `json:"name,omitempty"`
	Reason string `json:"reason"`
}

// ImportStat 记录某协议在导入中的分项统计。
type ImportStat struct {
	Total       int `json:"total"`
	Imported    int `json:"imported"`
	Unsupported int `json:"unsupported"`
}

// ImportReport 承载导入结果逐条反馈：成功、不支持的协议、解析失败行、按协议统计。
type ImportReport struct {
	Imported      []nodes.Node          `json:"imported"`
	Unsupported   []ImportFail          `json:"unsupported"`
	Failed        []ImportFail          `json:"failed"`
	ProtocolStats map[string]ImportStat `json:"protocol_stats"`
}

// maxImportFailures 控制 Unsupported/Failed 明细条数上限（订阅文本可能上万行），超出截断，统计仍完整。
const maxImportFailures = 1000

// truncateImportLine 截断单行到 120 字符，防止超长行刷爆响应。
func truncateImportLine(line string) string {
	if r := []rune(line); len(r) > 120 {
		return string(r[:120]) + "…"
	}
	return line
}

// rawLineScheme 从行首提取 scheme（:// 之前的小写部分，不含空白），
// 用于无法解析出完整节点时的协议归类。
func rawLineScheme(raw string) string {
	if idx := strings.Index(raw, "://"); idx > 0 {
		return strings.ToLower(strings.TrimSpace(raw[:idx]))
	}
	return ""
}

func addProtocolStat(stats map[string]ImportStat, protocol string, mutate func(*ImportStat)) {
	stat := stats[protocol]
	mutate(&stat)
	stats[protocol] = stat
}

func parseImportedNodes(text string) []nodes.Node {
	return parseImportedNodesReport(text).Imported
}

// parseImportedNodesReport 解析订阅文本并返回逐条反馈报告。
// 逐行路径：解析失败（非显式 unsupported）→ Failed；显式不支持的协议（socks4 等）或
// 能力预检不通过（ValidateNodeURI 非空）→ Unsupported（reason 用预检返回值）。
func parseImportedNodesReport(text string) ImportReport {
	report := ImportReport{ProtocolStats: map[string]ImportStat{}}
	text = strings.TrimSpace(text)
	if text == "" {
		return report
	}
	normalized := maybeDecodeSubscriptionText(text)

	// 结构化 Clash YAML / JSON 订阅走既有解析路径（完成后仅汇总导入成功）。
	if imported := parseClashYAMLToNodes(normalized); len(imported) > 0 {
		report.Imported = imported
		for _, n := range imported {
			addProtocolStat(report.ProtocolStats, n.Type, func(s *ImportStat) { s.Total++; s.Imported++ })
		}
		return report
	}
	if imported := parseJSONImportedNodes(normalized); len(imported) > 0 {
		report.Imported = imported
		for _, n := range imported {
			addProtocolStat(report.ProtocolStats, n.Type, func(s *ImportStat) { s.Total++; s.Imported++ })
		}
		return report
	}

	for _, line := range strings.Split(normalized, "\n") {
		raw := strings.TrimSpace(line)
		if raw == "" {
			continue
		}
		protocol := rawLineScheme(raw)
		if protocol == "" {
			protocol = "unknown"
		}
		addProtocolStat(report.ProtocolStats, protocol, func(s *ImportStat) { s.Total++ })

		node, ok := parseFlexibleImportedNodeLine(raw)
		if !ok {
			// 解析失败：socks4 等显式不支持的协议单独归类，其余为一般解析失败。
			if _, err := transport.ParseURI(raw); err != nil {
				var unsupported *transport.ErrProtocolUnsupported
				if errors.As(err, &unsupported) {
					report.appendUnsupported(ImportFail{
						Line: truncateImportLine(raw), Type: unsupported.Protocol, Reason: unsupported.Reason,
					})
					addProtocolStat(report.ProtocolStats, unsupported.Protocol, func(s *ImportStat) { s.Unsupported++ })
				} else {
					report.appendFailed(ImportFail{Line: truncateImportLine(raw), Reason: err.Error()})
				}
			}
			continue
		}

		// 能力预检：ValidateNodeURI 非空 → 不支持的协议（reason 用预检返回值）。
		if reason := transport.ValidateNodeURI(node.RawURI); reason != "" {
			report.appendUnsupported(ImportFail{
				Line: truncateImportLine(raw), Type: node.Type, Reason: reason,
			})
			addProtocolStat(report.ProtocolStats, protocol, func(s *ImportStat) { s.Unsupported++ })
			continue
		}
		report.Imported = append(report.Imported, node)
		addProtocolStat(report.ProtocolStats, protocol, func(s *ImportStat) { s.Imported++ })
	}
	return report
}

// appendUnsupported 追加 unsupported 明细；超过上限截断，统计字段不受影响。
func (r *ImportReport) appendUnsupported(fail ImportFail) {
	if len(r.Unsupported) >= maxImportFailures {
		return
	}
	r.Unsupported = append(r.Unsupported, fail)
}

// appendFailed 追加解析失败明细；超过上限截断，统计字段不受影响。
func (r *ImportReport) appendFailed(fail ImportFail) {
	if len(r.Failed) >= maxImportFailures {
		return
	}
	r.Failed = append(r.Failed, fail)
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
		Text    string `json:"text"`
		Replace bool   `json:"replace"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	log.Printf("[Admin] [ImportNodes] 收到优选节点文件导入请求, 替换模式: %v", body.Replace)

	report := parseImportedNodesReport(strings.TrimSpace(body.Text))
	if body.Replace {
		log.Printf("[Admin] [ImportNodes] 替换模式，正在清除全部已有候选节点")
		for _, cn := range nodes.LoadNodes() {
			nodes.DeleteNode(cn.RawURI)
		}
	}

	log.Printf("[Admin] [ImportNodes] 正在合并导入的新节点数量: %d（unsupported: %d, failed: %d）",
		len(report.Imported), len(report.Unsupported), len(report.Failed))
	nodes.MergeNodes(report.Imported)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "count": len(report.Imported),
		"unsupported":    report.Unsupported,
		"failed":         report.Failed,
		"protocol_stats": report.ProtocolStats,
	})
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

	if body.Replace {
		log.Printf("[Admin] [ImportNodesJson] 替换模式，正在清除全部已有候选节点")
		for _, cn := range nodes.LoadNodes() {
			nodes.DeleteNode(cn.RawURI)
		}
	}

	// 旧版 nodes.json 无法逐行反馈，但可做能力预检：不支持的协议归入 unsupported。
	report := ImportReport{ProtocolStats: map[string]ImportStat{}}
	for _, n := range d.Nodes {
		addProtocolStat(report.ProtocolStats, n.Type, func(s *ImportStat) { s.Total++ })
		if reason := transport.ValidateNodeURI(n.RawURI); reason != "" {
			report.appendUnsupported(ImportFail{Line: truncateImportLine(n.RawURI), Type: n.Type, Name: n.Name, Reason: reason})
			addProtocolStat(report.ProtocolStats, n.Type, func(s *ImportStat) { s.Unsupported++ })
			continue
		}
		report.Imported = append(report.Imported, n)
		addProtocolStat(report.ProtocolStats, n.Type, func(s *ImportStat) { s.Imported++ })
	}

	log.Printf("[Admin] [ImportNodesJson] 正在合并导入的新节点数量: %d（unsupported: %d）",
		len(report.Imported), len(report.Unsupported))
	nodes.MergeNodes(report.Imported)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "count": len(report.Imported),
		"unsupported":   report.Unsupported,
		"failed":        report.Failed,
		"protocol_stats": report.ProtocolStats,
	})
}
