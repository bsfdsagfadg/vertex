package transform

import (
	"bytes"
	"encoding/base64"
	"testing"
)

// 本文件测试 SignatureResolver 语义，保留旧 functioncall_test.go 全部场景：
// 真实签名保留、无签名合成、base64 四分支（哨兵/合法/明文/URL-safe）。

// wantSentinelBase64 是 SkipThoughtSentinel 的 base64 预期值。
const wantSentinelBase64 = "c2tpcF90aG91Z2h0X3NpZ25hdHVyZV92YWxpZGF0b3I="

func TestSignatureResolver_EnsureBase64Sig(t *testing.T) {
	resolver := NewSignatureResolver()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "哨兵逐一编码",
			in:   SkipThoughtSentinel,
			want: wantSentinelBase64,
		},
		{
			name: "真实 Base64 签正规化不二次编码",
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
			if got := resolver.EnsureBase64Sig(tt.in); got != tt.want {
				t.Errorf("EnsureBase64Sig(%q) = %q，want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSignatureEnsure_URLSafeAndWhitespace(t *testing.T) {
	resolver := NewSignatureResolver()
	raw := []byte{0xFB, 0xEF, 0xBE, 0xAD, 0xDE}
	urlSafe := base64.RawURLEncoding.EncodeToString(raw)
	withWhitespace := "  " + urlSafe + " \n"
	got := resolver.EnsureBase64Sig(withWhitespace)
	decoded, err := base64.StdEncoding.DecodeString(got)
	if err != nil {
		t.Errorf("规范化输出不可解码，got %q，err %v", got, err)
	}
	if !bytes.Equal(decoded, raw) {
		t.Errorf("规范化后解码 = %v，want %v", decoded, raw)
	}
}

func TestSignatureApply_RealSignaturePreserved(t *testing.T) {
	p := &Part{
		FunctionCall:     &FunctionCall{Name: "get_weather"},
		Thought:          true,
		ThoughtSignature: "REAL_SIGNATURE_VALUE",
	}
	NewSignatureResolver().ApplyPart(p)
	if p.ThoughtSignature != "REAL_SIGNATURE_VALUE" {
		t.Errorf("真实签名应原样保留，got %q", p.ThoughtSignature)
	}
}

func TestSignatureApply_InjectSentinelOnFunctionCall(t *testing.T) {
	p := &Part{FunctionCall: &FunctionCall{Name: "get_weather"}, Thought: true}
	NewSignatureResolver().ApplyPart(p)
	if p.ThoughtSignature != SkipThoughtSentinel {
		t.Errorf("应注入 sentinel，got %q", p.ThoughtSignature)
	}
}

func TestSignatureApply_InjectSentinelOnThought(t *testing.T) {
	p := &Part{Thought: true, Text: "thinking"}
	NewSignatureResolver().ApplyPart(p)
	if p.ThoughtSignature != SkipThoughtSentinel {
		t.Errorf("thought part 应注入 sentinel，got %q", p.ThoughtSignature)
	}
}

func TestSignatureApply_WriteThoughtWithTextKeepsSig(t *testing.T) {
	// thought=true 且带 text：思考带文本块，签名应保留
	p := &Part{Text: "thinking text", Thought: true, ThoughtSignature: "sig"}
	NewSignatureResolver().ApplyPart(p)
	if p.ThoughtSignature != "sig" {
		t.Errorf("思考带文本 part 应保留签名，got %q", p.ThoughtSignature)
	}
}

func TestSignatureApply_StripOnPlainText(t *testing.T) {
	// 纯文本（无 thought 标记）part 应剔除 thought/签名
	p := &Part{Text: "hello", Thought: false, ThoughtSignature: "sig"}
	NewSignatureResolver().ApplyPart(p)
	if p.Thought || p.ThoughtSignature != "" {
		t.Errorf("纯文本 part 应剔除 thought/签名，got thought=%v sig=%q", p.Thought, p.ThoughtSignature)
	}
}

func TestSignatureApply_FunctionResponseStripsSignature(t *testing.T) {
	p := &Part{FunctionResponse: &FunctionResponse{Name: "get_weather"}, Thought: true, ThoughtSignature: "sig"}
	NewSignatureResolver().ApplyPart(p)
	if p.Thought || p.ThoughtSignature != "" {
		t.Errorf("functionResponse part 应剔除 thought/签名，got thought=%v sig=%q", p.Thought, p.ThoughtSignature)
	}
}

func TestSignatureApplyContents_NormalizesToBase64(t *testing.T) {
	contents := []Content{
		{Role: "model", Parts: []Part{
			{FunctionCall: &FunctionCall{Name: "get_weather"}},
			{Thought: true, ThoughtSignature: "invalid_plain_sig"},
		}},
	}
	NewSignatureResolver().ApplyContents(contents)
	parts := contents[0].Parts
	// functionCall part：注入哨兵并编码为 base64
	if parts[0].ThoughtSignature != wantSentinelBase64 {
		t.Errorf("FC part 应注入 base64 哨兵，got %q", parts[0].ThoughtSignature)
	}
	// 明文签名转码为合法 base64，可解码回原文
	decoded, err := base64.StdEncoding.DecodeString(parts[1].ThoughtSignature)
	if err != nil {
		t.Fatalf("第二个 part 签名非合法 Base64，got %q，err %v", parts[1].ThoughtSignature, err)
	}
	if string(decoded) != "invalid_plain_sig" {
		t.Errorf("明文签名应转码，解码 = %q", decoded)
	}
}

func TestBuildGeminiVariables_AppliesSignaturesInBuildVariables(t *testing.T) {
	gem := &GeminiRequest{
		Contents: []Content{
			{Role: "user", Parts: []Part{{Text: "check weather"}}},
			{Role: "model", Parts: []Part{
				{FunctionCall: &FunctionCall{Name: "get_weather", Args: map[string]any{}}},
			}},
			{Role: "user", Parts: []Part{
				{FunctionResponse: &FunctionResponse{Name: "get_weather", Response: map[string]any{"result": "sunny"}}},
			}},
		},
	}

	vars := BuildGeminiVariables("gemini-2.5-flash", gem, nil)
	contents, ok := vars["contents"].([]any)
	if !ok || len(contents) != 3 {
		t.Fatalf("contents 长度 = %d，want 3", len(contents))
	}
	// 第二个回合是 model：functionCall part 必须在 BuildGeminiVariables 后携带哨兵签名
	modelTurn := contents[1].(map[string]any)
	modelParts := modelTurn["parts"].([]any)
	if len(modelParts) != 1 {
		t.Fatalf("model 回合预期 1 个 part，got %v", modelParts)
	}
	p0 := modelParts[0].(map[string]any)
	if p0["thoughtSignature"] != wantSentinelBase64 {
		t.Errorf("model 回合 functionCall 签名应为 base64 哨兵，got %v", p0["thoughtSignature"])
	}
	// functionResponse 回合必须无签名
	frTurn := contents[2].(map[string]any)
	frParts := frTurn["parts"].([]any)
	p2 := frParts[0].(map[string]any)
	if sig, exists := p2["thoughtSignature"]; exists && sig != "" {
		t.Errorf("functionResponse part 不应带签名，got %v", sig)
	}
}