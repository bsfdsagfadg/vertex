package transform

import (
	"bytes"
	"encoding/base64"
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

func TestFinalizeCleanedPartInjectsSentinelWithoutSignature(t *testing.T) {
	part := map[string]any{
		"functionCall": map[string]any{"name": "get_weather"},
		"thought":      "some thinking",
	}
	finalizeCleanedPart(part)
	if got := part["thoughtSignature"]; got != skipThoughtSentinel {
		t.Fatalf("missing sentinel signature: %v", got)
	}
}

func TestEnsureBase64Signature(t *testing.T) {
	valid := "UkVBTF9TSUdOQVRVUkVfVkFMVUU="
	if got := ensureBase64Signature(valid); got != valid {
		t.Fatalf("valid signature was changed: %q", got)
	}
	plain := "invalid_plain_text_sig!"
	if got := ensureBase64Signature(plain); got != base64.StdEncoding.EncodeToString([]byte(plain)) {
		t.Fatalf("plain signature was not encoded: %q", got)
	}
	raw := []byte{0xfb, 0xef, 0xbe, 0xad, 0xde}
	urlSafe := "  " + base64.RawURLEncoding.EncodeToString(raw) + " \n"
	got := ensureBase64Signature(urlSafe)
	decoded, err := base64.StdEncoding.DecodeString(got)
	if err != nil || !bytes.Equal(decoded, raw) {
		t.Fatalf("URL-safe signature was not normalized: %q err=%v", got, err)
	}
}

func TestEncodeThoughtSignatureEncodesPlainTextPath(t *testing.T) {
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
	decoded, err := base64.StdEncoding.DecodeString(signature)
	if err != nil || string(decoded) != "invalid_plain_text_sig!" {
		t.Fatalf("plain signature path was not encoded: %q err=%v", signature, err)
	}
}
