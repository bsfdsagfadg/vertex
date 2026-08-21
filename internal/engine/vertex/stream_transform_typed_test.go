package vertex

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/engine/transform"
	"github.com/bsfdsagfadg/vertex/internal/infra/jsonx"
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

// ── 双轨等价对照（计划 §3.4 关键安全网）──
// processStreamingObjectLegacy 是旧 map 版 processStreamingObject 的忠实复刻，
// 仅存在于测试文件（计划明示允许），用于对 8+ fixtures 逐帧断言字节等价。

// processStreamingObjectLegacy 复刻 Task A 之前的 map 实现（f069a88 原文）。
func processStreamingObjectLegacy(obj map[string]any, emit func(*transform.GeminiChunk) bool, seenFinish ...*bool) (bool, error) {
	var sf *bool
	if len(seenFinish) > 0 {
		sf = seenFinish[0]
	}
	results, _ := obj["results"].([]any)
	for _, rRaw := range results {
		result, ok := rRaw.(map[string]any)
		if !ok {
			continue
		}

		// results 内的错误处理。
		if errs, ok := result["errors"].([]any); ok && len(errs) > 0 {
			errMsg := ""
			if first, ok := errs[0].(map[string]any); ok {
				errMsg = toStr(first["message"])
			} else {
				errMsg = toStr(errs[0])
			}
			if strings.Contains(errMsg, "Failed to verify action") ||
				strings.Contains(errMsg, "The caller does not have permission") {
				return false, NewAuthenticationError(errMsg, nil)
			}
			if parsed := parseErrorResponse(map[string]any{"errors": errs}); parsed != nil {
				return false, parsed
			}
		}

		// 如果上一帧已标记 finishReason（STOP）但缺少 usageMetadata，尝试在当前帧收集。
		if sf != nil && *sf {
			if data, ok := result["data"].(map[string]any); ok {
				if _, hasUM := data["usageMetadata"]; hasUM {
					if chunk := extractChunk(data); chunk != nil {
						_ = emit(mapToGeminiChunk(chunk))
					} else {
						metaChunk := map[string]any{}
						for _, key := range []string{"usageMetadata", "modelVersion", "responseId", "promptFeedback", "createTime"} {
							if v, ok := data[key]; ok && isTruthyAny(v) {
								metaChunk[key] = v
							}
						}
						if len(metaChunk) > 0 {
							_ = emit(mapToGeminiChunk(metaChunk))
						}
					}
					return true, nil
				}
			}
			return false, nil
		}

		data, ok := result["data"].(map[string]any)
		if !ok {
			continue
		}

		// unwrap data.ui.streamGenerateContentAnonymous（匿名端点把载荷包在这里面）。
		if ui, ok := data["ui"].(map[string]any); ok {
			if innerRaw, exists := ui["streamGenerateContentAnonymous"]; exists {
				switch inner := innerRaw.(type) {
				case map[string]any:
					data = inner
				case []any:
					outerMeta := map[string]any{}
					for _, key := range []string{"usageMetadata", "modelVersion", "responseId", "promptFeedback"} {
						if v, ok := data[key]; ok && isTruthyAny(v) {
							outerMeta[key] = v
						}
					}
					for _, itemRaw := range inner {
						if item, ok := itemRaw.(map[string]any); ok {
							itemCopy := shallowCopy(item)
							for k, v := range outerMeta {
								if _, exists := itemCopy[k]; !exists {
									itemCopy[k] = v
								}
							}
							if chunk := extractChunk(itemCopy); chunk != nil {
								typedChunk := mapToGeminiChunk(chunk)
								if done := emitAndCheckFinish(typedChunk, emit); done {
									if typedChunk.UsageMetadata != nil || sf == nil {
										return true, nil
									}
									*sf = true
									return false, nil
								}
							}
						}
					}
					continue
				default:
					continue
				}
			}
		}

		if chunk := extractChunk(data); chunk != nil {
			typedChunk := mapToGeminiChunk(chunk)
			if done := emitAndCheckFinish(typedChunk, emit); done {
				if typedChunk.UsageMetadata != nil || sf == nil {
					return true, nil
				}
				*sf = true
				return false, nil
			}
		}
	}
	return false, nil
}

// collectEquivalence 对单条帧跑双轨，返回 (emit 序列, stop, err 串)。
func collectEquivalenceTyped(raw string) ([]string, bool, string) {
	var chunks []string
	stop, err := processStreamingObject([]byte(raw), func(ch *transform.GeminiChunk) bool {
		b, merr := jsonx.Marshal(ch)
		if merr != nil {
			chunks = append(chunks, "ERR:"+merr.Error())
			return true
		}
		chunks = append(chunks, string(b))
		return true
	})
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	return chunks, stop, errStr
}

func collectEquivalenceLegacy(raw string) ([]string, bool, string) {
	var chunks []string
	stop, err := processStreamingObjectLegacy(parseJSONObject([]byte(raw)), func(ch *transform.GeminiChunk) bool {
		b, merr := jsonx.Marshal(ch)
		if merr != nil {
			chunks = append(chunks, "ERR:"+merr.Error())
			return true
		}
		chunks = append(chunks, string(b))
		return true
	})
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	return chunks, stop, errStr
}

// TestStreamTypedVsLegacy_Equivalence 计划 §3.4 双轨对照安全网：
// ① 正常文本帧（UNSPECIFIED 首帧）；② 多候选帧；③ functionCall 字符串 args 帧；
// ④ candidates: [] 帧；⑤ usageMetadata 帧；⑥ 畸形嵌套 text 数组帧（命中回退）；
// ⑦ streamGenerateContentAnonymous 数组且带外层 meta；⑧ errors 帧（verify-fail 与普通错误）；
// ⑨ 数组空 item + 外层 meta（合并顺序回归）；⑩ 纯 finishReason 无 content 帧（不得被丢弃）。
func TestStreamTypedVsLegacy_Equivalence(t *testing.T) {
	fixtures := []string{
		// ① 正常文本帧（带 UNSPECIFIED 首帧）
		`{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":{"candidates":[{"content":{"parts":[{"text":"Hello"}],"role":"model"},"finishReason":"FINISH_REASON_UNSPECIFIED","index":0}]}}}}]}`,
		// ② 多候选帧
		`{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":{"candidates":[{"content":{"parts":[{"text":"A"}],"role":"model"},"index":0},{"content":{"parts":[{"text":"B"}],"role":"model"},"index":1}]}}}}]}`,
		// ③ functionCall 字符串 args 帧
		`{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":{"candidates":[{"content":{"parts":[{"functionCall":{"name":"search","args":"{\"q\":\"hello\"}"}}],"role":"model"},"index":0}]}}}}]}`,
		// ④ candidates: [] 帧
		`{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":{"candidates":[]}}}}]}`,
		// ⑤ usageMetadata 帧
		`{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":{"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":2,"totalTokenCount":3}}}}}]}`,
		// ⑥ 畸形嵌套 text 数组帧（必须命中回退）
		`{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":{"candidates":[{"content":{"parts":[{"text":[{"text":"nested"},{"text":" text"}]}],"role":"model"},"finishReason":"STOP"}]}}}}]}`,
		// ⑦ streamGenerateContentAnonymous 数组 + 外层 meta
		`{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":[{"candidates":[{"content":{"parts":[{"text":"Hi"}],"role":"model"},"finishReason":"FINISH_REASON_UNSPECIFIED"}]},{"candidates":[{"content":{"parts":[{"text":"Bye"}],"role":"model"}}]}]},"usageMetadata":{"promptTokenCount":5,"totalTokenCount":9},"modelVersion":"gemini-3.7","responseId":"r-42"}}]}`,
		// ⑧a errors 帧（verify-fail）
		`{"results":[{"errors":[{"message":"Failed to verify action"}]}]}`,
		// ⑧b errors 帧（普通错误）
		`{"results":[{"errors":[{"code":13,"message":"Internal error encountered"}]}]}`,
		// ⑨ 数组空 item + 外层 meta（legacy 先合并后 extract，typed 不得丢帧）
		`{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":[{},{}]},"usageMetadata":{"promptTokenCount":7,"totalTokenCount":12}}}]}`,
		// ⑩ 纯 finishReason 无 content 候选帧（legacy 原样透传，typed 不得丢弃导致流挂死）
		`{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":{"candidates":[{"finishReason":"STOP","index":0}]}}}}]}`,
	}

	for i, raw := range fixtures {
		raw := raw
		t.Run(fmt.Sprintf("fixture_%02d", i+1), func(t *testing.T) {
			typedChunks, typedStop, typedErr := collectEquivalenceTyped(raw)
			legacyChunks, legacyStop, legacyErr := collectEquivalenceLegacy(raw)
			if (typedErr == "") != (legacyErr == "") {
				t.Fatalf("错误形态不一致: typed err=%q, legacy err=%q", typedErr, legacyErr)
			}
			if typedErr != legacyErr {
				t.Fatalf("错误内容不一致:\n  typed:  %q\n  legacy: %q", typedErr, legacyErr)
			}
			if typedStop != legacyStop {
				t.Fatalf("stop 不一致: typed=%v, legacy=%v", typedStop, legacyStop)
			}
			typedSeq := strings.Join(typedChunks, "\n")
			legacySeq := strings.Join(legacyChunks, "\n")
			if typedSeq != legacySeq {
				t.Fatalf("emit 序列不一致:\n  typed(%d):\n%s\n  legacy(%d):\n%s",
					len(typedChunks), typedSeq, len(legacyChunks), legacySeq)
			}
		})
	}
}

// TestProcessStreamingObject_NullCandidateFrame 上游 candidates:[null] 异常帧
// 不得击穿 typed 快路径（空指针候选丢弃），且不泄漏 nil 候选给下游消费者。
func TestProcessStreamingObject_NullCandidateFrame(t *testing.T) {
	raw := `{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":{"candidates":[null]}}}}]}`
	var got []*transform.GeminiChunk
	stop, err := processStreamingObject([]byte(raw), func(ch *transform.GeminiChunk) bool {
		got = append(got, ch)
		return true
	})
	if err != nil || stop {
		t.Fatalf("null 候选帧不应报错/结束: err=%v stop=%v", err, stop)
	}
	if len(got) != 1 {
		t.Fatalf("期望 1 个 chunk, got %d", len(got))
	}
	for _, c := range got[0].Candidates {
		if c == nil {
			t.Fatal("nil 候选泄漏到输出")
		}
	}
}

// TestProcessStreamingObject_NullPlusRealCandidate 空指针候选与真实候选共存时，
// 真实候选的 finishReason 必须仍能驱动流结束（chunkFinishReasonTyped 防御）。
func TestProcessStreamingObject_NullPlusRealCandidate(t *testing.T) {
	raw := `{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":{"candidates":[null,{"content":{"parts":[{"text":"ok"}],"role":"model"},"finishReason":"STOP","index":0}]}}}}]}`
	var got []*transform.GeminiChunk
	stop, err := processStreamingObject([]byte(raw), func(ch *transform.GeminiChunk) bool {
		got = append(got, ch)
		return true
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !stop {
		t.Fatal("真实候选 STOP 应驱动 stop=true")
	}
	if len(got) != 1 || len(got[0].Candidates) != 1 {
		t.Fatalf("期望 1 个 chunk 且 1 个候选, got %+v", got)
	}
}
