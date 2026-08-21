package api

import (
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/infra/transport"
	"github.com/bsfdsagfadg/vertex/internal/node/exitpool"
)

func TestDedupNodesUsesIRIdentity(t *testing.T) {
	// 自持实例验证「节点域 × IR 身份解析」集成语义（与 main.go 内联装配实现一致）
	resolver := transport.NewIdentityResolver(transport.NewIRCache())
	mgr := exitpool.NewManager(nil, resolver, exitpool.Hooks{})

	uriA := "vless://12345678-1234-1234-1234-123456789012@cf.example.com:443?security=tls#name-a"
	uriB := "vless://12345678-1234-1234-1234-123456789012@cf.example.com:443?security=tls#name-b"
	mgr.MergeNodes([]exitpool.Node{{RawURI: uriA, Name: "a"}, {RawURI: uriB, Name: "b"}})
	removed := mgr.DedupNodes()
	if removed != 1 {
		t.Fatalf("expected 1 removed via IR identity, got %d", removed)
	}
	for _, n := range mgr.LoadNodes() {
		mgr.DeleteNode(n.RawURI)
	}
}
