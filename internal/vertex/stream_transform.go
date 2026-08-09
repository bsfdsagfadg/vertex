package vertex

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"

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

// processStreamingObject 从单个上游 JSON 对象提取增量 chunk。
//
// 先识别 results 内的错误（"Failed to verify action" → AuthenticationError 触发重试），
// 再 unwrap data.ui.streamGenerateContentAnonymous，最后 _extract_chunk 清洗后 emit。
// 返回 (stop, err)：emit 出真实 finishReason 即 stop=true（结束扫描）；客户端断开由 ctx.Err() 路径处理；上游错误即 err 非 nil。
// seenFinish 用于跨帧追踪 finishReason 已发出但 usageMetadata 延迟到达的情况。
func processStreamingObject(obj map[string]any, emit func(*transform.GeminiChunk) bool, seenFinish ...*bool) (bool, error) {
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

		// 如果上一帧已标记 finishReason（STOP）但缺少 usageMetadata，尝试在当前帧收集
		if sf != nil && *sf {
			if data, ok := result["data"].(map[string]any); ok {
				if _, hasUM := data["usageMetadata"]; hasUM {
					if chunk := extractChunk(data); chunk != nil {
						_ = emit(mapToGeminiChunk(chunk))
					} else {
						// 无 candidates 但直接有 usageMetadata 的纯元数据帧
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

// extractChunk 从 Gemini 数据中提取标准化 chunk，清洗畸形嵌套。
//
// 对齐 Python _process_streaming_object：candidates key 存在且非 nil 时保留
// （即使空列表），总是复制 metadata 字段，仅当 chunk完全无字段时返回 nil。
func extractChunk(data map[string]any) map[string]any {
	chunk := map[string]any{}

	if raw, ok := data["candidates"]; ok && raw != nil {
		candidatesRaw, _ := raw.([]any)
		if len(candidatesRaw) > 0 {
			cleaned := make([]any, 0, len(candidatesRaw))
			for _, cRaw := range candidatesRaw {
				candidate, ok := cRaw.(map[string]any)
				if !ok {
					continue
				}
				content, hasContent := candidate["content"].(map[string]any)
				if hasContent {
					parts, ok := content["parts"].([]any)
					if ok {
						cleanedParts := cleanStreamParts(parts)
						cc := shallowCopy(candidate)
						role := toStr(content["role"])
						if role == "" {
							role = "model"
						}
						cc["content"] = map[string]any{"role": role, "parts": cleanedParts}
						cleaned = append(cleaned, cc)
					} else {
						cleaned = append(cleaned, candidate)
					}
				} else {
					cleaned = append(cleaned, candidate)
				}
			}
			if len(cleaned) > 0 {
				chunk["candidates"] = cleaned
			} else {
				chunk["candidates"] = candidatesRaw
			}
		} else {
			chunk["candidates"] = candidatesRaw
		}
	}

	for _, key := range []string{"usageMetadata", "modelVersion", "responseId", "promptFeedback", "createTime"} {
		if v, ok := data[key]; ok && isTruthyAny(v) {
			chunk[key] = v
		}
	}

	if len(chunk) == 0 {
		return nil
	}
	return chunk
}

// cleanStreamParts 清洗 parts 列表，展开畸形嵌套 + 移除 protobuf 空默认字段。
func cleanStreamParts(parts []any) []any {
	cleaned := make([]any, 0, len(parts))
	for _, pRaw := range parts {
		part, ok := pRaw.(map[string]any)
		if !ok {
			continue
		}
		textVal, hasText := part["text"]
		if hasText {
			if _, isStr := textVal.(string); !isStr {
				// 畸形嵌套：text 值是 list/dict 而非字符串，递归提取真正文本。
				extracted := extractTextRecursive(textVal, 0)
				if extracted != "" {
					newPart := cleanPart(part)
					if newPart != nil {
						newPart["text"] = extracted
						cleaned = append(cleaned, newPart)
					}
				}
				continue
			}
		}
		if cp := cleanPart(part); cp != nil {
			cleaned = append(cleaned, cp)
		}
	}
	return cleaned
}

// cleanPart 清洗单个 Gemini part，移除内部 protobuf 空默认字段，仅保留真实内容字段。
func cleanPart(part map[string]any) map[string]any {
	cleaned := shallowCopy(part)

	// 移除内部 protobuf oneof 指示器（always "text" / "inlineData" / "functionCall" / "functionResponse"）
	delete(cleaned, "data")

	// fileData：仅在 uri 为空时移除
	if fd, ok := cleaned["fileData"].(map[string]any); ok {
		if toStr(fd["fileUri"]) == "" && toStr(fd["mimeType"]) == "" {
			delete(cleaned, "fileData")
		}
	}

	// functionCall：name 和 args 都为空/无意义时移除
	if fc, ok := cleaned["functionCall"].(map[string]any); ok {
		hasName := toStr(fc["name"]) != ""
		hasArgs := false
		if args, ok := fc["args"]; ok && args != nil {
			if m, ok := args.(map[string]any); ok && len(m) > 0 {
				hasArgs = true
			}
		}
		if !hasName && !hasArgs {
			delete(cleaned, "functionCall")
		} else if name, ok := fc["name"].(string); ok && name != "" {
			if fc["args"] == nil {
				fc["args"] = map[string]any{}
			} else if argStr, ok := fc["args"].(string); ok {
				if argStr != "" {
					var parsed any
					if err := json.Unmarshal([]byte(argStr), &parsed); err == nil {
						fc["args"] = parsed
					} else {
						fc["args"] = map[string]any{}
					}
				} else {
					fc["args"] = map[string]any{}
				}
			}
		}
	}

	// functionResponse：name 和 response 都为空时移除
	if fr, ok := cleaned["functionResponse"].(map[string]any); ok {
		hasName := toStr(fr["name"]) != ""
		hasResp := false
		if resp, ok := fr["response"]; ok && resp != nil {
			if m, ok := resp.(map[string]any); ok && len(m) > 0 {
				hasResp = true
			}
		}
		if !hasName && !hasResp {
			delete(cleaned, "functionResponse")
		} else if respStr, ok := fr["response"].(string); ok && respStr != "" {
			fr["response"] = map[string]any{"result": respStr}
		}
	}

	// inlineData：data 为空时移除
	if id, ok := cleaned["inlineData"].(map[string]any); ok {
		if toStr(id["data"]) == "" {
			delete(cleaned, "inlineData")
		}
	}

	if len(cleaned) == 0 {
		return nil
	}
	return cleaned
}

// isValidContentChunkTyped 检查强类型 chunk 是否包含有效生成内容或真实 finishReason。
func isValidContentChunkTyped(chunk *transform.GeminiChunk) bool {
	if chunk == nil {
		return false
	}
	if chunk.PromptFeedback != nil && chunk.PromptFeedback.BlockReason != "" && chunk.PromptFeedback.BlockReason != blockReasonUnspecified {
		return true
	}
	if len(chunk.Candidates) > 0 {
		for _, cand := range chunk.Candidates {
			if cand.FinishReason != "" && cand.FinishReason != finishReasonUnspecified {
				return true
			}
			if cand.Content != nil {
				for _, p := range cand.Content.Parts {
					if p.Text != "" || p.Thought || p.FunctionCall != nil || p.InlineData != nil || p.FileData != nil || p.ExecutableCode != nil || p.CodeExecutionResult != nil {
						return true
					}
				}
			}
		}
	}
	return false
}

// blockReasonUnspecified 是 protobuf 枚举 BlockReason 的默认值。
// 上游对未发生拦截的响应会携带该值（非空字符串），它不代表真实安全拦截，
// 不得据此判为有效内容，否则空 STOP 帧会被当成正常响应。
const blockReasonUnspecified = "BLOCKED_REASON_UNSPECIFIED"

// ─────────────────────────────────────────────────────────────────────────
// 以下为 Debug 取证辅助：仅用于输出摘要，不改变任何转发行为。
// ─────────────────────────────────────────────────────────────────────────

// skipThoughtSentinelStr 是 transform 包注入的伪签名哨兵明文，用于日志识别伪造签名。
const skipThoughtSentinelStr = "skip_thought_signature_validator"

// skipThoughtSentinelBase64 是 skipThoughtSentinel 的 base64 编码值，用于日志识别伪造签名。
var skipThoughtSentinelBase64 = base64.StdEncoding.EncodeToString([]byte(skipThoughtSentinelStr))

// sigKind 识别 thoughtSignature 的值形态：sentinel（伪造哨兵）/ 真实签名（长度+前缀）。
func sigKind(raw string) string {
	if raw == skipThoughtSentinelBase64 {
		return "sentinel"
	}
	if b, err := base64.StdEncoding.DecodeString(raw); err == nil && string(b) == skipThoughtSentinelStr {
		return "sentinel"
	}
	if len(raw) > 16 {
		return raw[:12] + "...(len" + strconv.Itoa(len(raw)) + ")"
	}
	return raw + "(len" + strconv.Itoa(len(raw)) + ")"
}

// describePart 生成单个 part 的紧凑描述（类型 + 关键字段长度/名称）。
func describePart(p map[string]any) string {
	if sig, ok := p["thoughtSignature"].(string); ok {
		return "sig:" + sigKind(sig)
	}
	if v, ok := p["text"].(string); ok {
		return "text:" + strconv.Itoa(len(v))
	}
	if v, ok := p["thought"].(string); ok {
		return "thought:" + strconv.Itoa(len(v))
	}
	if v, ok := p["thought"].(bool); ok {
		return "thought:" + strconv.FormatBool(v)
	}
	if fc, ok := p["functionCall"].(map[string]any); ok {
		name, _ := fc["name"].(string)
		return "fc:" + name
	}
	if fr, ok := p["functionResponse"].(map[string]any); ok {
		name, _ := fr["name"].(string)
		return "fr:" + name
	}
	if _, ok := p["inlineData"].(map[string]any); ok {
		return "inlineData"
	}
	return "other"
}

// describeUsage 提取 usageMetadata 的 token 计数摘要。
func describeUsage(u map[string]any) string {
	var sb strings.Builder
	for _, k := range []string{"promptTokenCount", "candidatesTokenCount", "totalTokenCount"} {
		if v, ok := u[k]; ok {
			sb.WriteString(" " + k + "=" + toStr(v))
		}
	}
	return sb.String()
}

// summarizeChunk 摘要格式化一个标准 chunk（与通过过程化提取后的结构一致）。
func summarizeChunk(chunk map[string]any) string {
	var sb strings.Builder
	if cands, ok := chunk["candidates"].([]any); ok && len(cands) > 0 {
		for i, cRaw := range cands {
			c, _ := cRaw.(map[string]any)
			if c == nil {
				continue
			}
			sb.WriteString(" c[" + strconv.Itoa(i) + "]")
			if fr, _ := c["finishReason"].(string); fr != "" {
				sb.WriteString(" fr=" + fr)
			}
			content, _ := c["content"].(map[string]any)
			if content == nil {
				continue
			}
			role, _ := content["role"].(string)
			sb.WriteString(" :" + role + "{")
			if ps, ok := content["parts"].([]any); ok {
				descs := make([]string, 0, len(ps))
				for _, pRaw := range ps {
					if p, ok := pRaw.(map[string]any); ok {
						descs = append(descs, describePart(p))
					} else {
						descs = append(descs, "part(non-map)")
					}
				}
				sb.WriteString(strings.Join(descs, ","))
			}
			sb.WriteString("}")
		}
	} else {
		sb.WriteString(" (no candidates)")
	}
	if usage, ok := chunk["usageMetadata"].(map[string]any); ok {
		sb.WriteString(describeUsage(usage))
	}
	if br, ok := chunk["promptFeedback"].(map[string]any); ok {
		if b, _ := br["blockReason"].(string); b != "" {
			sb.WriteString(" blockReason=" + b)
		}
	}
	return strings.TrimSpace(sb.String())
}

// summarizeUpstreamObject 摘要单条上游原始 JSON 对象（results[].data[].ui.streamGenerateContentAnonymous 结构）。
func summarizeUpstreamObject(obj map[string]any) string {
	results, _ := obj["results"].([]any)
	if len(results) == 0 {
		return summarizeChunk(obj)
	}
	var descs []string
	for _, rRaw := range results {
		r, _ := rRaw.(map[string]any)
		if r == nil {
			continue
		}
		if errs, ok := r["errors"].([]any); ok && len(errs) > 0 {
			msg := ""
			if first, ok := errs[0].(map[string]any); ok {
				msg = toStr(first["message"])
			} else {
				msg = toStr(errs[0])
			}
			if len(msg) > 120 {
				msg = msg[:120]
			}
			descs = append(descs, "errors:"+msg)
			continue
		}
		data, _ := r["data"].(map[string]any)
		if data == nil {
			continue
		}
		inner := data
		if ui, ok := data["ui"].(map[string]any); ok {
			if innerRaw, exists := ui["streamGenerateContentAnonymous"]; exists {
				switch v := innerRaw.(type) {
				case map[string]any:
					inner = v
				case []any:
					var sub []string
					for _, itemRaw := range v {
						if item, ok := itemRaw.(map[string]any); ok {
							sub = append(sub, summarizeChunk(item))
						}
					}
					descs = append(descs, "list["+strings.Join(sub, " | ")+"]")
					continue
				}
			}
		}
		descs = append(descs, summarizeChunk(inner))
	}
	if len(descs) == 0 {
		return "(empty)"
	}
	return strings.Join(descs, " | ")
}

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
