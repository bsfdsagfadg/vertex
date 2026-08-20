package transform

import (
	"testing"
)

func TestFinalizeCleanedPartPreservesRealSignature(t *testing.T) {
	part := map[string]any{
		"functionCall":     map[string]any{"name": "get_weather"},
		"thoughtSignature": "REAL_SIGNATURE_VALUE",
		"thought":          "some thinking",
	}
	finalizeCleanedPart(part)
	if got := part["thoughtSignature"]; got != "REAL_SIGNATURE_VALUE" {
		t.Fatalf("real signature was overwritten: %v", got)
	}
}

func TestFinalizeCleanedPartDoesNotForgeMissingSignature(t *testing.T) {
	part := map[string]any{
		"functionCall": map[string]any{"name": "get_weather"},
		"thought":      "some thinking",
	}
	finalizeCleanedPart(part)
	if _, exists := part["thoughtSignature"]; exists {
		t.Fatalf("missing signature was forged: %v", part["thoughtSignature"])
	}
}

func TestEnsureBase64SignaturePreservesOpaqueValue(t *testing.T) {
	for _, signature := range []string{"UkVBTF9TSUdOQVRVUkVfVkFMVUU=", "invalid_plain_text_sig!", "  --opaque--  \n"} {
		if got := ensureBase64Signature(signature); got != signature {
			t.Fatalf("opaque signature changed: %q -> %q", signature, got)
		}
	}
}

func TestEncodeThoughtSignaturePreservesPlainTextPath(t *testing.T) {
	contents := []any{map[string]any{
		"role": "model",
		"parts": []any{map[string]any{
			"thought":          "some thinking",
			"thoughtSignature": "invalid_plain_text_sig!",
		}},
	}}
	out := EncodeThoughtSignature(contents, 0).([]any)
	parts := out[0].(map[string]any)["parts"].([]any)
	signature := parts[0].(map[string]any)["thoughtSignature"].(string)
	if signature != "invalid_plain_text_sig!" {
		t.Fatalf("opaque signature changed: %q", signature)
	}
}
