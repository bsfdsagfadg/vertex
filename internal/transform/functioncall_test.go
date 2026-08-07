package transform

import (
	"testing"
)

// TestFinalizeCleanedPart_PreservesRealSignature 验证带真实签名与 functionCall 的 part 签名字样保留、不被哨兵覆盖。
func TestFinalizeCleanedPart_PreservesRealSignature(t *testing.T) {
	part := map[string]any{
		"functionCall":     map[string]any{"name": "get_weather"},
		"thoughtSignature": "REAL_SIGNATURE_VALUE",
		"thought":          "some thinking",
	}
	finalizeCleanedPart(part)
	if got := part["thoughtSignature"]; got != "REAL_SIGNATURE_VALUE" {
		t.Errorf("真实签名应原样保留，got %v", got)
	}
}

// TestFinalizeCleanedPart_InjectSentinelWhenNoSig 验证无真实签名且含 FC/thought 时注入哨兵。
func TestFinalizeCleanedPart_InjectSentinelWhenNoSig(t *testing.T) {
	part := map[string]any{
		"functionCall": map[string]any{"name": "get_weather"},
		"thought":      "some thinking",
	}
	finalizeCleanedPart(part)
	if got := part["thoughtSignature"]; got != skipThoughtSentinel {
		t.Errorf("应注入 sentinel，got %v", got)
	}
}
