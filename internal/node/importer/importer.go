package importer

import (
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/infra/transport"
	"github.com/bsfdsagfadg/vertex/internal/node/exitpool"
)

// Service 是导入引擎的实例化门面：无内部状态，供 api ServerDeps 以窄接口注入消费。
type Service struct{}

// NewService 构造导入服务门面。
func NewService() *Service { return &Service{} }

// Parse 委托包级多格式纯解析入口（URI / Clash / V2Ray / JSON 等自动嗅探）。
func (*Service) Parse(text string) []exitpool.Node { return ParseImportedNodes(text) }

func ParseImportedNodes(text string) []exitpool.Node {
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

	var imported []exitpool.Node
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

func ParseImportedNodeLine(line string) (exitpool.Node, bool) {
	raw := strings.TrimSpace(line)
	if raw == "" {
		return exitpool.Node{}, false
	}

	pn, err := transport.ParseURI(raw)
	if err != nil || pn == nil {
		return exitpool.Node{}, false
	}

	nodeType := strings.TrimSpace(pn.Type)
	if nodeType == "" {
		return exitpool.Node{}, false
	}

	nodeName := strings.TrimSpace(pn.Name)
	if nodeName == "" {
		nodeName = raw[:min(len(raw), 40)]
	}
	// 不支持节点标记（RecordTest）与 IR 缓存预热均已上移至 api 调用方——importer 保持纯解析，
	// 仅产出 Disabled 标志。
	disabled := !pn.Supported

	return exitpool.Node{Type: nodeType, Name: nodeName, RawURI: raw, Disabled: disabled}, true
}

func ParseFlexibleImportedNodeLine(line string) (exitpool.Node, bool) {
	if node, ok := ParseImportedNodeLine(line); ok {
		return node, true
	}
	return ParseV2RayNNodeLine(line)
}
