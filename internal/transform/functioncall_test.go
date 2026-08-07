package transform

import (
	"bytes"
	"encoding/base64"
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

// TestEncodeThoughtSignature 覆盖哨兵值、真实 Base64 签名、明文转码与 URL-safe 规范化四种场景。
func TestEncodeThoughtSignature(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "哨兵值直接编码",
			in:   skipThoughtSentinel,
			want: "c2tpcF90aG91Z2h0X3NpZ25hdHVyZV92YWxpZGF0b3I=",
		},
		{
			name: "真实 Base64 签名不二次编码",
			in:   "UkVBTF9TSUdOQVRVUkVfVkFMVUU=",
			want: "UkVBTF9TSUdOQVRVUkVfVkFMVUU=",
		},
		{
			name: "明文非 Base64 转码降级",
			in:   "invalid_plain_text_sig!",
			want: "aW52YWxpZF9wbGFpbl90ZXh0X3NpZyE=",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ensureBase64Signature(tt.in); got != tt.want {
				t.Errorf("ensureBase64Signature(%q) = %q，want %q", tt.in, got, tt.want)
			}
		})
	}

	// URL-safe 字符（'-'、'_'）与空白输入应被 NormalizeBase64 还原为可解码的标准 Base64。
	t.Run("URL-safe 与空格规范化", func(t *testing.T) {
		raw := []byte{0xFB, 0xEF, 0xBE, 0xAD, 0xDE}
		urlSafe := base64.RawURLEncoding.EncodeToString(raw)
		withWhitespace := "  " + urlSafe + " \n"
		got := ensureBase64Signature(withWhitespace)
		decoded, err := base64.StdEncoding.DecodeString(got)
		if err != nil {
			t.Errorf("规范化输出不可解码，got %q，err %v", got, err)
		}
		if !bytes.Equal(decoded, raw) {
			t.Errorf("规范化后解码 = %v，want %v", decoded, raw)
		}
	})

	// 真实签名但带合法 padding 的标准 Base64 与原始串一致，往返校验不误杀。
	t.Run("标准 Base64 往返校验通过", func(t *testing.T) {
		got := ensureBase64Signature("UkVBTF9TSUdOQVRVUkVfVkFMVUU=")
		if got != "UkVBTF9TSUdOQVRVUkVfVkFMVUU=" {
			t.Errorf("标准 Base64 应原样保留，got %q", got)
		}
	})
}

// TestEncodeThoughtSignature_PlainTextPath 验证 New API 明文签名经 EncodeThoughtSignature 的 parts 集成路径被转码为合法 Base64。
func TestEncodeThoughtSignature_PlainTextPath(t *testing.T) {
	contents := []any{
		map[string]any{
			"role": "model",
			"parts": []any{
				map[string]any{
					"thought":          "some thinking",
					"thoughtSignature": "invalid_plain_text_sig!",
				},
			},
		},
	}
	out := EncodeThoughtSignature(contents, 0)
	parts := out.([]any)[0].(map[string]any)["parts"].([]any)
	sig := parts[0].(map[string]any)["thoughtSignature"].(string)
	decoded, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		t.Fatalf("集成路径输出非合法 Base64，got %q，err %v", sig, err)
	}
	if string(decoded) != "invalid_plain_text_sig!" {
		t.Errorf("集成路径转码后解码 = %q，want 原文", decoded)
	}
}
