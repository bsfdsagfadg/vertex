package vertex

import (
	"encoding/json"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/jsonx"
	"github.com/bsfdsagfadg/vertex/internal/transform"
)

// ── typed 直解链单元测试（stream_transform.go 快路径，子场景拆分）──

// TestRawTruthy_MatchesJsonxTruthy 对照 jsonx.Truthy 全类别验证 rawTruthy 等价。
func TestRawTruthy_MatchesJsonxTruthy(t *testing.T) {
	cases := []string{
		`null`, `false`, `true`, `""`, `"x"`, `{}`, `{"a":1}`, `[]`, `[1]`,
		`0`, `0.5`, `-0`, `0e0`, `0.0`, `-0.0`, `5`, `"0"`, `"false"`,
	}
	for _, c := range cases {
		raw := json.RawMessage(c)
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatalf("fixture %q 解码失败: %v", c, err)
		}
		if got := rawTruthy(raw); got != jsonx.Truthy(v) {
			t.Errorf("rawTruthy(%q)=%v, jsonx.Truthy=%v 不一致", c, got, jsonx.Truthy(v))
		}
	}
}

func TestCleanTypedPart_EmptyDefaults(t *testing.T) {
	p := &transform.Part{
		FileData:         &transform.FileData{},
		FunctionCall:     &transform.FunctionCall{},
		FunctionResponse: &transform.FunctionResponse{},
		InlineData:       &transform.InlineData{},
	}
	if cleanTypedPart(p) {
		t.Error("empty defaults should be dropped")
	}
}

func TestCleanTypedPart_KeepsTextThought(t *testing.T) {
	p := &transform.Part{Text: "hi", Thought: true}
	if !cleanTypedPart(p) {
		t.Error("text+thought part should be kept")
	}
	p2 := &transform.Part{ThoughtSignature: "sig_x"}
	if !cleanTypedPart(p2) {
		t.Error("thoughtSignature-only part should be kept")
	}
}

func TestCleanTypedPart_FunctionCallStringArgs(t *testing.T) {
	p := &transform.Part{FunctionCall: &transform.FunctionCall{Name: "search", Args: `{"q":"hello"}`}}
	if !cleanTypedPart(p) {
		t.Fatal("expected kept part")
	}
	args, ok := p.FunctionCall.Args.(map[string]any)
	if !ok {
		t.Fatalf("args should be map after normalization, got %T", p.FunctionCall.Args)
	}
	if args["q"] != "hello" {
		t.Errorf("args.q=%v, want hello", args["q"])
	}
}

func TestCleanTypedPart_FunctionCallEmptyArgs(t *testing.T) {
	p := &transform.Part{FunctionCall: &transform.FunctionCall{Name: "no_args", Args: ""}}
	if !cleanTypedPart(p) {
		t.Fatal("expected kept part when name present")
	}
	if m, ok := p.FunctionCall.Args.(map[string]any); !ok || len(m) != 0 {
		t.Errorf("空 args 应转为空 map, got %#v", p.FunctionCall.Args)
	}
}

func TestCleanTypedPart_FunctionResponseString(t *testing.T) {
	p := &transform.Part{FunctionResponse: &transform.FunctionResponse{Name: "search", Response: "result text"}}
	if !cleanTypedPart(p) {
		t.Fatal("expected kept part")
	}
	if m, ok := p.FunctionResponse.Response.(map[string]any); !ok || m["result"] != "result text" {
		t.Errorf("response should be wrapped as {\"result\":...}, got %#v", p.FunctionResponse.Response)
	}
}

func TestCleanTypedPart_FileDataEmptyURI(t *testing.T) {
	p := &transform.Part{FileData: &transform.FileData{FileURI: "gs://b/f", MimeType: "image/png"}}
	if !cleanTypedPart(p) {
		t.Fatal("fileData with uri should be kept")
	}
	p2 := &transform.Part{FileData: &transform.FileData{}}
	if cleanTypedPart(p2) {
		t.Error("empty fileData should be dropped")
	}
}

func TestDecodeChunkTyped_Normal(t *testing.T) {
	payload := []byte(`{"candidates":[{"content":{"parts":[{"text":"Hello"}],"role":"model"},"finishReason":"STOP","index":0}],"usageMetadata":{"totalTokenCount":3}}`)
	ch := decodeChunkTyped(payload)
	if ch == nil {
		t.Fatal("expected chunk")
	}
	if len(ch.Candidates) != 1 || ch.Candidates[0].Content.Parts[0].Text != "Hello" {
		t.Errorf("text extraction failed: %+v", ch.Candidates)
	}
	if ch.UsageMetadata == nil || ch.UsageMetadata.TotalTokenCount != 3 {
		t.Errorf("usageMetadata extraction failed: %+v", ch.UsageMetadata)
	}
}

func TestDecodeChunkTyped_MalformedTextFallsBackToLegacy(t *testing.T) {
	// text 为数组（畸形嵌套）→ 快路径解码失败 → legacy 回退递归提取。
	payload := []byte(`{"candidates":[{"content":{"parts":[{"text":[{"text":"nested"},{"text":" text"}]}],"role":"model"},"finishReason":"STOP"}]}`)
	ch := decodeChunkTyped(payload)
	if ch == nil {
		t.Fatal("expected chunk via legacy fallback")
	}
	if got := ch.Candidates[0].Content.Parts[0].Text; got != "nested text" {
		t.Errorf("text=%q, want 'nested text'", got)
	}
}

func TestDecodeChunkTyped_EmptyCandidatesList(t *testing.T) {
	ch := decodeChunkTyped([]byte(`{"candidates":[]}`))
	if ch == nil {
		t.Fatal("空 candidates 列表应返回 chunk，不应为 nil")
	}
	if ch.Candidates == nil || len(ch.Candidates) != 0 {
		t.Errorf("candidates=%v, want non-nil empty list", ch.Candidates)
	}
}

func TestDecodeChunkTyped_CompletelyEmpty(t *testing.T) {
	if ch := decodeChunkTyped([]byte(`{}`)); ch != nil {
		t.Errorf("空帧应返回 nil, got %+v", ch)
	}
}

func TestDecodeChunkTyped_MetaTruthyFilter(t *testing.T) {
	// usageMetadata 为 {}（假值）→ 必须 nil（等价 isTruthyAny 过滤），不得输出空对象。
	ch := decodeChunkTyped([]byte(`{"candidates":[{"content":{"parts":[{"text":"x"}],"role":"model"}}],"usageMetadata":{}}`))
	if ch == nil {
		t.Fatal("expected chunk")
	}
	if ch.UsageMetadata != nil {
		t.Errorf("假值 usageMetadata 应置 nil, got %+v", ch.UsageMetadata)
	}
	// usageMetadata 为 null → 同样 nil。
	ch2 := decodeChunkTyped([]byte(`{"candidates":[{"content":{"parts":[{"text":"x"}],"role":"model"}}],"usageMetadata":null}`))
	if ch2 == nil || ch2.UsageMetadata != nil {
		t.Errorf("null usageMetadata 应置 nil, ch=%+v", ch2)
	}
}

func TestDecodeChunkTyped_RoleDefaultsToModel(t *testing.T) {
	// 缺 role 时对齐 legacy extractChunk 的 model 默认值。
	ch := decodeChunkTyped([]byte(`{"candidates":[{"content":{"parts":[{"text":"x"}]}}]}`))
	if ch == nil {
		t.Fatal("expected chunk")
	}
	if got := ch.Candidates[0].Content.Role; got != "model" {
		t.Errorf("role=%q, want model（对齐 legacy 默认值）", got)
	}
}

