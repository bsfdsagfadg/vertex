package importer

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/infra/transport"
)

func TestParseImportedNodesEmpty(t *testing.T) {
	if got := ParseImportedNodes(""); len(got) != 0 {
		t.Errorf("空文本应返回 nil, got %v", got)
	}
	if got := ParseImportedNodes("   \n  "); len(got) != 0 {
		t.Errorf("纯空白应返回 nil, got %v", got)
	}
}

func TestParseImportedNodesPlainURILines(t *testing.T) {
	text := "vless://12345678-1234-1234-1234-123456789012@cf.example.com:443?security=tls&sni=edge.example.com&fp=chrome&type=ws&path=%2Fws&host=edge.example.com#demo1\n" +
		"ss://YWVzLTEyOC1nY206c2VjcmV0@1.2.3.4:8388#demo2\n" +
		"not-a-valid-line\n"
	imported := ParseImportedNodes(text)
	if len(imported) != 2 {
		t.Fatalf("期望 2 个有效节点, got %d: %+v", len(imported), imported)
	}
	if imported[0].Type != "vless" || imported[0].Name != "demo1" {
		t.Errorf("vless 节点不符: %+v", imported[0])
	}
	if imported[1].Type != "ss" || imported[1].Name != "demo2" {
		t.Errorf("ss 节点不符: %+v", imported[1])
	}
}

func TestParseImportedNodesBase64Subscription(t *testing.T) {
	// base64 订阅内容：两行标准 URI
	inner := "vless://12345678-1234-1234-1234-123456789012@a.example.com:443#sub1\nss://YWVzLTEyOC1nY206c2VjcmV0@b.example.com:8388#sub2"
	text := base64.StdEncoding.EncodeToString([]byte(inner))
	imported := ParseImportedNodes(text)
	if len(imported) != 2 {
		t.Fatalf("base64 订阅应解出 2 个节点, got %d: %+v", len(imported), imported)
	}
	if imported[0].Name != "sub1" || imported[1].Name != "sub2" {
		t.Errorf("订阅节点名不符: %+v", imported)
	}
}

func TestParseImportedNodeLineRejectsGarbage(t *testing.T) {
	if _, ok := ParseImportedNodeLine("hello world"); ok {
		t.Error("普通文本不应被当作节点")
	}
	if _, ok := ParseImportedNodeLine(""); ok {
		t.Error("空行不应被当作节点")
	}
}

func TestParseFlexibleImportedNodeLineFallsBackToV2RayN(t *testing.T) {
	payload := `{"ConfigType":5,"Remarks":"flex-demo","Address":"cf.example.com","Port":443,"Password":"12345678-1234-1234-1234-123456789012","Network":"ws","StreamSecurity":"tls","Sni":"edge.example.com"}`
	line := "v2rayn://vless/" + base64.RawURLEncoding.EncodeToString([]byte(payload))
	node, ok := ParseFlexibleImportedNodeLine(line)
	if !ok {
		t.Fatal("v2rayn 行应经 flexible 路径解析")
	}
	pn, err := transport.ParseURI(node.RawURI)
	if err != nil || pn == nil {
		t.Fatalf("ParseURI 失败: %v", err)
	}
	if pn.Type != "vless" || pn.TLS == nil || pn.TLS.ServerName != "edge.example.com" {
		t.Errorf("flexible v2rayn 解析不符: %#v", pn)
	}
}

func TestMaybeDecodeSubscriptionTextKeepsPlain(t *testing.T) {
	// 非 base64 文本原样返回
	text := "vless://12345678-1234-1234-1234-123456789012@a.example.com:443#keep"
	if got := maybeDecodeSubscriptionText(text); got != text {
		t.Errorf("纯文本应原样返回, got %q", got)
	}
	// 解码后不可导入的内容也应原样返回
	if got := maybeDecodeSubscriptionText(base64.StdEncoding.EncodeToString([]byte("just a note"))); got != base64.StdEncoding.EncodeToString([]byte("just a note")) {
		t.Errorf("不可导入的 base64 应原样返回, got %q", got)
	}
	if !strings.Contains(maybeDecodeSubscriptionText(base64.StdEncoding.EncodeToString([]byte("vless://12345678-1234-1234-1234-123456789012@a.example.com:443#x"))), "vless://") {
		t.Error("可导入的 base64 应解码返回")
	}
}
