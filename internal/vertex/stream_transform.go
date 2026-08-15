package vertex

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/jsonx"
	"github.com/bsfdsagfadg/vertex/internal/transform"
)

// finishReasonUnspecified 是匿名 batchGraphql 每帧都携带的 protobuf 默认值（无意义）。
//
// 流式最关键红线（红线⑤）：匿名端点每个增量帧都带 finishReason="FINISH_REASON_UNSPECIFIED"，
// 只有真正结束时才给 STOP/MAX_TOKENS/SAFETY/PROHIBITED_CONTENT 等真实值。
// **绝不能据 UNSPECIFIED 发 finish 事件或结束流**：首帧就命中会立即截断（血泪教训）。
// 只有 finishReason 非空且 != FINISH_REASON_UNSPECIFIED 才主动结束流（否则上游不及时关连接
// 会挂死到 180s 超时）。
const finishReasonUnspecified = "FINISH_REASON_UNSPECIFIED"

// isTruthyAny 委托 jsonx.Truthy（统一真值语义，见 jsonx.Truthy）。
func isTruthyAny(v any) bool { return jsonx.Truthy(v) }

// 上游 batchGraphql 响应外层 envelope（匿名端点结构）。
// RawMessage 字段经 json.Unmarshal 填充时为字节拷贝，与扫描器内部缓冲区生命周期解耦。
type streamingResult struct {
	Errors json.RawMessage `json:"errors"`
	Data   json.RawMessage `json:"data"`
}

type streamingEnvelope struct {
	Results json.RawMessage `json:"results"`
}

// streamingData 是 results[].data 载荷（含 ui.streamGenerateContentAnonymous 解包位）。
// UI 用 RawMessage 承载，保持"ui 非对象即不 unwrap"的现状语义。
type streamingData struct {
	UI json.RawMessage `json:"ui"`

	UsageMetadata  json.RawMessage `json:"usageMetadata"`
	ModelVersion   string          `json:"modelVersion"`
	ResponseID     string          `json:"responseId"`
	PromptFeedback json.RawMessage `json:"promptFeedback"`
	CreateTime     string          `json:"createTime"`
}

// processStreamingObject 从单个上游 JSON 对象（原始字节）提取增量 chunk。
//
// 先识别 results 内的错误（"Failed to verify action" → AuthenticationError 触发重试），
// 再 unwrap data.ui.streamGenerateContentAnonymous，最后强类型解码清洗后 emit。
// 返回 (stop, err)：emit 出真实 finishReason 即 stop=true（结束扫描）；客户端断开由 ctx.Err() 路径处理；上游错误即 err 非 nil。
// seenFinish 用于跨帧追踪 finishReason 已发出但 usageMetadata 延迟到达的情况。
//
// 热路径全程零 map 树分配：envelope 用 RawMessage 解包，chunk 载荷强类型直解；
// 仅畸形嵌套帧回退 legacy map 路径（见 decodeChunkTyped）。
func processStreamingObject(raw []byte, emit func(*transform.GeminiChunk) bool, seenFinish ...*bool) (bool, error) {
	var sf *bool
	if len(seenFinish) > 0 {
		sf = seenFinish[0]
	}

	var env streamingEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		// 畸形完整帧（花括号配平但 JSON 非法）：可重试的上游协议错误（network 类），
		// 不静默跳过——否则流末尾会误报空响应。
		return false, NewNetworkError(fmt.Errorf("streamScan: invalid JSON object from upstream (protocol error), raw_len=%d, preview=%s",
			len(raw), truncateStr(string(raw), 200)))
	}

	// results 缺失或非数组 → 空循环（等价现状类型断言失败）。
	var results []streamingResult
	if len(env.Results) == 0 || json.Unmarshal(env.Results, &results) != nil {
		return false, nil
	}

	for _, result := range results {
		// results 内的错误处理（errors 必须是非空数组才触发，等价现状类型断言）。
		var errs []any
		if len(result.Errors) > 0 && json.Unmarshal(result.Errors, &errs) == nil && len(errs) > 0 {
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
			var data map[string]json.RawMessage
			if err := json.Unmarshal(result.Data, &data); err != nil {
				return false, nil // 等价现状 data 非 map → 直接返回
			}
			if _, hasUM := data["usageMetadata"]; hasUM {
				if ch := decodeChunkTyped(result.Data); ch != nil {
					_ = emit(ch)
				} else if meta := metaChunkTyped(data); meta != nil {
					_ = emit(meta)
				}
				return true, nil
			}
			return false, nil
		}

		var data streamingData
		if err := json.Unmarshal(result.Data, &data); err != nil {
			continue // 等价现状 data 非 map → 跳过该 result
		}

		// unwrap data.ui.streamGenerateContentAnonymous（匿名端点把载荷包在这里面）。
		payload := result.Data
		if len(data.UI) > 0 && data.UI[0] == '{' {
			var ui struct {
				StreamGenerateContentAnonymous json.RawMessage `json:"streamGenerateContentAnonymous"`
			}
			if err := json.Unmarshal(data.UI, &ui); err != nil {
				continue
			}
			if inner := ui.StreamGenerateContentAnonymous; len(inner) > 0 {
				switch inner[0] {
				case '{':
					payload = inner // 对象分支：以内层载荷走主路径
				case '[':
					// 数组分支：外层 meta（4 键，不含 createTime）补入每个 item。
					var items []json.RawMessage
					if err := json.Unmarshal(inner, &items); err != nil {
						continue
					}
					for _, itemRaw := range items {
						var itemProbe map[string]json.RawMessage
						if err := json.Unmarshal(itemRaw, &itemProbe); err != nil {
							continue // 等价 legacy item 非 map 跳过
						}
						ch := decodeChunkTyped(itemRaw)
						if ch == nil {
							continue
						}
						mergeMetaTyped(ch, itemProbe, &data)
						if done := emitAndCheckFinish(ch, emit); done {
							if ch.UsageMetadata != nil || sf == nil {
								return true, nil
							}
							*sf = true
							return false, nil
						}
					}
					continue
				default:
					continue // null 等无意义载荷（等价现状 default: continue）
				}
			}
		}

		if ch := decodeChunkTyped(payload); ch != nil {
			if done := emitAndCheckFinish(ch, emit); done {
				if ch.UsageMetadata != nil || sf == nil {
					return true, nil
				}
				*sf = true
				return false, nil
			}
		}
	}
	return false, nil
}

// decodeChunkTyped 把上游 chunk 载荷单趟解码为强类型 GeminiChunk（快路径）。
//
// 结构不匹配（畸形嵌套：text 非字符串、args 为数字等）时自动回退 legacy map 路径
// （parseJSONObject → extractChunk → mapToGeminiChunk），完整保留现状清洗语义。
// 返回 nil 表示空帧（等价 extractChunk 返回 nil）。
func decodeChunkTyped(payload []byte) *transform.GeminiChunk {
	var probe struct {
		UsageMetadata  json.RawMessage `json:"usageMetadata"`
		PromptFeedback json.RawMessage `json:"promptFeedback"`
	}
	if err := json.Unmarshal(payload, &probe); err != nil {
		return decodeChunkLegacy(payload)
	}
	var chunk transform.GeminiChunk
	if err := json.Unmarshal(payload, &chunk); err != nil {
		return decodeChunkLegacy(payload)
	}
	// meta 真值过滤（等价 isTruthyAny）：null/空对象等假值 → nil，不进入输出。
	if !rawTruthy(probe.UsageMetadata) {
		chunk.UsageMetadata = nil
	}
	if !rawTruthy(probe.PromptFeedback) {
		chunk.PromptFeedback = nil
	}
	// parts 清洗（等价 cleanStreamParts）+ role 归一（等价 extractChunk 的 model 默认值）。
	for _, cand := range chunk.Candidates {
		if cand.Content == nil || cand.Content.Parts == nil {
			continue
		}
		parts := cand.Content.Parts[:0]
		for i := range cand.Content.Parts {
			if cleanTypedPart(&cand.Content.Parts[i]) {
				parts = append(parts, cand.Content.Parts[i])
			}
		}
		cand.Content.Parts = parts
		if cand.Content.Role == "" {
			cand.Content.Role = "model"
		}
	}
	if chunkEmpty(&chunk) {
		return nil
	}
	return &chunk
}

// decodeChunkLegacy 走旧 map 路径（parseJSONObject → extractChunk → mapToGeminiChunk）。
func decodeChunkLegacy(payload []byte) *transform.GeminiChunk {
	obj := parseJSONObject(payload)
	if obj == nil {
		return nil
	}
	chunk := extractChunk(obj)
	if chunk == nil {
		return nil
	}
	return mapToGeminiChunk(chunk)
}

// chunkEmpty 判定 chunk 是否为空帧（等价 extractChunk 返回 nil 的情形）。
// 注意：非 nil 空 candidates 列表（上游发 []）不算空帧，必须保留。
func chunkEmpty(ch *transform.GeminiChunk) bool {
	if ch == nil {
		return true
	}
	return ch.Candidates == nil && ch.UsageMetadata == nil && ch.PromptFeedback == nil &&
		ch.ModelVersion == "" && ch.ResponseID == "" && ch.CreateTime == ""
}

// rawTruthy 判定 RawMessage 真值，语义与 jsonx.Truthy / isTruthyAny 完全一致
// （空字节 / null / false / 0 / 空对象 / 空数组 / 空串为假，其余为真）。
// 解码失败（畸形）一律判假（等价现状跳过）。
func rawTruthy(raw json.RawMessage) bool {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return false
	}
	return jsonx.Truthy(v)
}

// metaChunkTyped 从帧顶层键构造纯元数据帧（等价 map 版 5 键 truthy 过滤）。
func metaChunkTyped(data map[string]json.RawMessage) *transform.GeminiChunk {
	var chunk transform.GeminiChunk
	if raw, ok := data["usageMetadata"]; ok && rawTruthy(raw) {
		_ = json.Unmarshal(raw, &chunk.UsageMetadata)
	}
	if raw, ok := data["modelVersion"]; ok && rawTruthy(raw) {
		_ = json.Unmarshal(raw, &chunk.ModelVersion)
	}
	if raw, ok := data["responseId"]; ok && rawTruthy(raw) {
		_ = json.Unmarshal(raw, &chunk.ResponseID)
	}
	if raw, ok := data["promptFeedback"]; ok && rawTruthy(raw) {
		_ = json.Unmarshal(raw, &chunk.PromptFeedback)
	}
	if raw, ok := data["createTime"]; ok && rawTruthy(raw) {
		_ = json.Unmarshal(raw, &chunk.CreateTime)
	}
	if chunkEmpty(&chunk) {
		return nil
	}
	return &chunk
}

// mergeMetaTyped 把外层 meta（4 键，不含 createTime）补入数组 item——
// item 已有对应键则不覆盖（等价 legacy 浅拷贝+缺失合并）。
func mergeMetaTyped(ch *transform.GeminiChunk, itemProbe map[string]json.RawMessage, outer *streamingData) {
	if _, exists := itemProbe["usageMetadata"]; !exists && rawTruthy(outer.UsageMetadata) {
		_ = json.Unmarshal(outer.UsageMetadata, &ch.UsageMetadata)
	}
	if _, exists := itemProbe["modelVersion"]; !exists && outer.ModelVersion != "" {
		ch.ModelVersion = outer.ModelVersion
	}
	if _, exists := itemProbe["responseId"]; !exists && outer.ResponseID != "" {
		ch.ResponseID = outer.ResponseID
	}
	if _, exists := itemProbe["promptFeedback"]; !exists && rawTruthy(outer.PromptFeedback) {
		_ = json.Unmarshal(outer.PromptFeedback, &ch.PromptFeedback)
	}
}

// cleanTypedPart 清洗单个 typed part，严格镜像 cleanPart 的删除规则。
// 返回 false 表示该 part 应被丢弃（等价 cleanPart 返回 nil）。
func cleanTypedPart(p *transform.Part) bool {
	// fileData：仅在 uri 与 mimeType 均为空时移除。
	if p.FileData != nil && p.FileData.FileURI == "" && p.FileData.MimeType == "" {
		p.FileData = nil
	}

	// functionCall：name 和 args 都为空/无意义时移除；否则规范化 args。
	if p.FunctionCall != nil {
		hasName := p.FunctionCall.Name != ""
		hasArgs := false
		if m, ok := p.FunctionCall.Args.(map[string]any); ok && len(m) > 0 {
			hasArgs = true
		}
		if !hasName && !hasArgs {
			p.FunctionCall = nil
		} else if hasName {
			switch a := p.FunctionCall.Args.(type) {
			case nil:
				p.FunctionCall.Args = map[string]any{}
			case string:
				if a != "" {
					var parsed any
					if err := json.Unmarshal([]byte(a), &parsed); err == nil {
						p.FunctionCall.Args = parsed
					} else {
						p.FunctionCall.Args = map[string]any{}
					}
				} else {
					p.FunctionCall.Args = map[string]any{}
				}
			}
		}
	}

	// functionResponse：name 和 response 都为空时移除；否则规范化 response。
	if p.FunctionResponse != nil {
		hasName := p.FunctionResponse.Name != ""
		hasResp := false
		if m, ok := p.FunctionResponse.Response.(map[string]any); ok && len(m) > 0 {
			hasResp = true
		}
		if !hasName && !hasResp {
			p.FunctionResponse = nil
		} else if respStr, ok := p.FunctionResponse.Response.(string); ok && respStr != "" {
			p.FunctionResponse.Response = map[string]any{"result": respStr}
		}
	}

	// inlineData：data 为空时移除。
	if p.InlineData != nil && p.InlineData.Data == "" {
		p.InlineData = nil
	}

	// 全零判定：任一字段非零值即保留（等价 len(cleaned) == 0 判 nil）。
	if p.Text != "" || p.Thought || p.ThoughtSignature != "" || p.InlineData != nil ||
		p.FileData != nil || p.FunctionCall != nil || p.FunctionResponse != nil ||
		p.ExecutableCode != nil || p.CodeExecutionResult != nil || p.VideoMetadata != nil ||
		p.MediaResolution != "" {
		return true
	}
	return false
}

// emitAndCheckFinish emit 一个 typed chunk 并判定是否应结束流。
func emitAndCheckFinish(chunk *transform.GeminiChunk, emit func(*transform.GeminiChunk) bool) (done bool) {
	emit(chunk)
	fr := chunkFinishReasonTyped(chunk)
	if fr != "" && fr != finishReasonUnspecified {
		return true
	}
	return false
}

// chunkFinishReasonTyped 取 chunk 的 candidates[0].finishReason。
func chunkFinishReasonTyped(chunk *transform.GeminiChunk) string {
	if chunk == nil || len(chunk.Candidates) == 0 {
		return ""
	}
	return chunk.Candidates[0].FinishReason
}

// isValidContentChunkTyped 检查强类型 chunk 是否包含有效生成内容或真实 finishReason。
// 接收可选 model 参数，若未传 model 则默认按文本家族处理。
func isValidContentChunkTyped(chunk *transform.GeminiChunk, model ...string) bool {
	m := ""
	if len(model) > 0 {
		m = model[0]
	}
	return transform.NewModelFamilyRouter().For(m).IsValidChunk(chunk)
}

// blockReasonUnspecified 是 protobuf 枚举 BlockReason 的默认值。
// 上游对未发生拦截的响应会携带该值（非空字符串），它不代表真实安全拦截，
// 不得据此判为有效内容，否则空 STOP 帧会被当成正常响应。
const blockReasonUnspecified = "BLOCKED_REASON_UNSPECIFIED"

// extractTextRecursive 从嵌套结构中递归提取纯文本，防止无限递归（depth>20 截断）。
func extractTextRecursive(val any, depth int) string {
	if depth > 20 {
		s := toStr(val)
		if len(s) > 500 {
			return s[:500]
		}
		return s
	}
	switch v := val.(type) {
	case string:
		return v
	case map[string]any:
		if t, ok := v["text"]; ok {
			return extractTextRecursive(t, depth+1)
		}
		return ""
	case []any:
		var sb strings.Builder
		for _, item := range v {
			if t := extractTextRecursive(item, depth+1); t != "" {
				sb.WriteString(t)
			}
		}
		return sb.String()
	default:
		return ""
	}
}
