package api

import (
	"fmt"
	"strings"
	"testing"
)

// TestParseImportedNodesReportSocks4Lines 覆盖方案验收：253 行 socks4 文本
// → Unsupported==253、Reason 含 socks4、Imported==0；旧调用 parseImportedNodes 返回类型不变。
func TestParseImportedNodesReportSocks4Lines(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 253; i++ {
		fmt.Fprintf(&sb, "socks4://1.2.3.4:1080#socks4-node-%d\n", i)
	}
	text := sb.String()

	report := parseImportedNodesReport(text)
	if len(report.Imported) != 0 {
		t.Fatalf("socks4 不应被导入，Imported=%d", len(report.Imported))
	}
	if len(report.Unsupported) != 253 {
		t.Fatalf("Unsupported 明细应为 253 条，got %d", len(report.Unsupported))
	}
	first := report.Unsupported[0]
	if first.Type != "socks4" || !strings.Contains(first.Reason, "socks5") {
		t.Fatalf("unsupported 明细类型/原因不对: %#v", first)
	}
	if !strings.Contains(first.Line, "socks4://") {
		t.Fatalf("unsupported 明细应保留行内容: %q", first.Line)
	}
	stat := report.ProtocolStats["socks4"]
	if stat.Total != 253 || stat.Unsupported != 253 || stat.Imported != 0 {
		t.Fatalf("socks4 统计不对: %#v", stat)
	}

	// 旧调用签名不变，返回类型 []nodes.Node 且为 0 节点
	if got := parseImportedNodes(text); len(got) != 0 {
		t.Fatalf("parseImportedNodes 应返回 0 节点，got %d", len(got))
	}
}

func TestParseImportedNodesReportMixedLines(t *testing.T) {
	text := "vless://12345678-1234-1234-1234-123456789012@example.com:443#valid\n" +
		"socks4://1.2.3.4:1080#s4\n" +
		"this-is-not-a-node-line\n"

	report := parseImportedNodesReport(text)
	if len(report.Imported) != 1 {
		t.Fatalf("vless 行应导入成功，Imported=%d", len(report.Imported))
	}
	if report.Imported[0].Type != "vless" {
		t.Fatalf("Imported[0].Type 应为 vless，got %q", report.Imported[0].Type)
	}
	if len(report.Unsupported) != 1 || report.Unsupported[0].Type != "socks4" {
		t.Fatalf("socks4 行应进 unsupported，got %#v", report.Unsupported)
	}
	if len(report.Failed) != 1 {
		t.Fatalf("垃圾行应进 failed，got %#v", report.Failed)
	}
	if stat := report.ProtocolStats["vless"]; stat.Total != 1 || stat.Imported != 1 {
		t.Fatalf("vless 统计不对: %#v", stat)
	}
}

func TestParseImportedNodesReportBareHostPortFallback(t *testing.T) {
	text := "1.2.3.4:8080\n[2001:db8::1]:443\n"
	report := parseImportedNodesReport(text)
	if len(report.Imported) != 2 {
		t.Fatalf("裸行应归 socks5 导入，Imported=%d, unsupported=%#v, failed=%#v",
			len(report.Imported), report.Unsupported, report.Failed)
	}
	for _, n := range report.Imported {
		if n.Type != "socks5" {
			t.Fatalf("裸行导入类型应为 socks5，got %q", n.Type)
		}
	}
}

func TestParseImportedNodesReportDetailLimit(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < maxImportFailures+50; i++ {
		fmt.Fprintf(&sb, "socks4://1.2.3.4:1080#socks4-node-%d\n", i)
	}
	report := parseImportedNodesReport(sb.String())
	if len(report.Unsupported) > maxImportFailures {
		t.Fatalf("unsupported 明细应截断到 %d 条，got %d", maxImportFailures, len(report.Unsupported))
	}
	if stat := report.ProtocolStats["socks4"]; stat.Total != maxImportFailures+50 {
		t.Fatalf("截断不应影响统计完整性: %#v", stat)
	}
}

func TestParseClashYAMLNormalizesSocksAlias(t *testing.T) {
	yamlText := `
proxies:
  - { name: 's4', type: socks4, server: 1.2.3.4, port: 1080 }
  - { name: 's5-alias', type: socks, server: 1.2.3.4, port: 1081 }
`
	imported := parseClashYAMLToNodes(yamlText)
	if len(imported) != 2 {
		t.Fatalf("socks 别名应归一后导入，got %d: %#v", len(imported), imported)
	}
	for _, n := range imported {
		if n.Type != "socks5" {
			t.Fatalf("socks 别名应归一为 socks5，got %q (%q)", n.Type, n.Name)
		}
	}
}
