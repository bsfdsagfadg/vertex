package api

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/nodes"
)

func TestNodeIdentityFromIR(t *testing.T) {
	uriA := "vless://12345678-1234-1234-1234-123456789012@cf.example.com:443?security=tls#name-a"
	uriB := "vless://12345678-1234-1234-1234-123456789012@cf.example.com:443?security=tls#name-b"
	keyA, okA := nodeIdentityFromIR(uriA)
	keyB, okB := nodeIdentityFromIR(uriB)
	if !okA || !okB || keyA != keyB {
		t.Fatalf("same server/cred with different fragments should share a key: %q vs %q (ok=%v,%v)", keyA, keyB, okA, okB)
	}
	if keyA != "vless://12345678-1234-1234-1234-123456789012@cf.example.com:443" {
		t.Fatalf("unexpected identity key: %q", keyA)
	}

	payload, err := json.Marshal(map[string]any{
		"add":  "cf.example.com",
		"port": 443,
		"id":   "aa11bb22-cc33-dd44-ee55-ff6601122334",
		"ps":   "test",
	})
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	vmessKey, okV := nodeIdentityFromIR("vmess://" + base64.StdEncoding.EncodeToString(payload))
	if !okV {
		t.Fatalf("vmess identity should parse")
	}
	if vmessKey == keyA {
		t.Fatalf("vmess and vless with different credentials must not share a key")
	}
	if !strings.HasPrefix(vmessKey, "vmess://") {
		t.Fatalf("unexpected vmess identity key: %q", vmessKey)
	}
}

func TestDedupNodesUsesIRIdentity(t *testing.T) {
	// 依赖 api 包 init 已注册 nodes.NodeIdentityFunc
	uriA := "vless://12345678-1234-1234-1234-123456789012@cf.example.com:443?security=tls#name-a"
	uriB := "vless://12345678-1234-1234-1234-123456789012@cf.example.com:443?security=tls#name-b"
	nodes.MergeNodes([]nodes.Node{{RawURI: uriA, Name: "a"}, {RawURI: uriB, Name: "b"}})
	removed := nodes.DedupNodes()
	if removed != 1 {
		t.Fatalf("expected 1 removed via IR identity, got %d", removed)
	}
	for _, n := range nodes.LoadNodes() {
		nodes.DeleteNode(n.RawURI)
	}
}
