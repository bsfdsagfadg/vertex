package transport

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestIRIdentityResolver_SharesKeyByCred(t *testing.T) {
	resolver := NewIdentityResolver(NewIRCache())
	uriA := "vless://12345678-1234-1234-1234-123456789012@cf.example.com:443?security=tls#name-a"
	uriB := "vless://12345678-1234-1234-1234-123456789012@cf.example.com:443?security=tls#name-b"
	keyA, okA := resolver.Identity(uriA)
	keyB, okB := resolver.Identity(uriB)
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
	vmessKey, okV := resolver.Identity("vmess://" + base64.StdEncoding.EncodeToString(payload))
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

func TestIRIdentityResolver_Fallbacks(t *testing.T) {
	resolver := NewIdentityResolver(NewIRCache())

	// 解析失败 → (rawURI, false)，调用方回退 rawURI 键
	key, ok := resolver.Identity("unknown://bad-scheme")
	if ok || key != "unknown://bad-scheme" {
		t.Fatalf("expected (rawURI,false) fallback, got (%q,%v)", key, ok)
	}

	// 不支持的能力标注 → Supported=false
	if resolver.Supported("vless://u@example.com:443?type=xhttp") {
		t.Fatal("xhttp transport 应判不支持")
	}
	if !resolver.Supported("vless://u@example.com:443") {
		t.Fatal("裸 tcp 应判支持")
	}
}
