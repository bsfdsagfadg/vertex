package importer

import (
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

func ParseImportedNodes(text string) []nodes.Node {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	normalized := maybeDecodeSubscriptionText(text)
	if imported := ParseClashYAMLToNodes(normalized); len(imported) > 0 {
		return imported
	}
	if imported := ParseJSONImportedNodes(normalized); len(imported) > 0 {
		return imported
	}

	var imported []nodes.Node
	for _, line := range strings.Split(normalized, "\n") {
		if node, ok := ParseFlexibleImportedNodeLine(line); ok {
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
	if strings.Contains(decoded, "proxies:") || hasImportableNodeLine(decoded) || len(ParseJSONImportedNodes(decoded)) > 0 {
		return decoded
	}
	return text
}

func hasImportableNodeLine(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if _, ok := ParseFlexibleImportedNodeLine(line); ok {
			return true
		}
	}
	return false
}

func ParseImportedNodeLine(line string) (nodes.Node, bool) {
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

func ParseFlexibleImportedNodeLine(line string) (nodes.Node, bool) {
	if node, ok := ParseImportedNodeLine(line); ok {
		return node, true
	}
	return ParseV2RayNNodeLine(line)
}
