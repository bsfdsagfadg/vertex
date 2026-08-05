package vertex

import (
	"encoding/json"
	"strings"
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
func processStreamingObject(obj map[string]any, emit func(map[string]any) bool, seenFinish ...*bool) (bool, error) {
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
						_ = emit(chunk)
					} else {
						// 无 candidates 但直接有 usageMetadata 的纯元数据帧
						metaChunk := map[string]any{}
						for _, key := range []string{"usageMetadata", "modelVersion", "responseId", "promptFeedback", "createTime"} {
							if v, ok := data[key]; ok && isTruthyAny(v) {
								metaChunk[key] = v
							}
						}
						if len(metaChunk) > 0 {
							_ = emit(metaChunk)
						}
					}
					return true, nil
				}
			}
			// 未找到 usageMetadata 则继续等待（修复：不再无条件停止）
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
					// 极少数情况 inner 是 list（如多个 candidate）：逐项 extract+emit。
					// 注意：inner 各项是平级 candidates 而非连续帧，因此同一 list 内即使
					// 某一项触发 *sf=true，剩余项也会继续处理（不会进入上方 seenFinish 块）。
					for _, itemRaw := range inner {
						if item, ok := itemRaw.(map[string]any); ok {
							// 浅拷贝避免修改原始 json 解析数据（C2 修复）
							itemCopy := shallowCopy(item)
							for k, v := range outerMeta {
								if _, exists := itemCopy[k]; !exists {
									itemCopy[k] = v
								}
							}
							if chunk := extractChunk(itemCopy); chunk != nil {
								if done := emitAndCheckFinish(chunk, emit); done {
									if _, hasUsage := chunk["usageMetadata"]; hasUsage || sf == nil {
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
			if done := emitAndCheckFinish(chunk, emit); done {
				if _, hasUsage := chunk["usageMetadata"]; hasUsage || sf == nil {
					return true, nil
				}
				*sf = true
				return false, nil
			}
		}
	}
	return false, nil
}

// emitAndCheckFinish emit 一个 chunk 并判定是否应结束流。
//
// finishReason 过滤（红线⑤）：emit 后取 chunk 的 candidates[0].finishReason，
// **仅当非空且 != FINISH_REASON_UNSPECIFIED 才主动结束流**。
// 返回 done=true 表示应停止扫描。
func emitAndCheckFinish(chunk map[string]any, emit func(map[string]any) bool) (done bool) {
	emit(chunk)
	fr := chunkFinishReason(chunk)
	if fr != "" && fr != finishReasonUnspecified {
		return true
	}
	return false
}

// chunkFinishReason 取 chunk 的 candidates[0].finishReason。
func chunkFinishReason(chunk map[string]any) string {
	cands, ok := chunk["candidates"].([]any)
	if !ok || len(cands) == 0 {
		return ""
	}
	c, ok := cands[0].(map[string]any)
	if !ok {
		return ""
	}
	return toStr(c["finishReason"])
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
			if argStr, ok := fc["args"].(string); ok {
				if argStr != "" {
					var parsed any
					if err := json.Unmarshal([]byte(argStr), &parsed); err == nil {
						fc["args"] = parsed
					}
				} else {
					fc["args"] = map[string]any{}
				}
			}
			// args 已是 map[string]any 无需处理
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

// isValidContentChunk 检查 chunk 是否包含有效生成内容（text/thought/functionCall）
// 或真实 finishReason，或安全拦截 blockReason。
func isValidContentChunk(chunk map[string]any) bool {
	cands, ok := chunk["candidates"].([]any)
	if ok && len(cands) > 0 {
		for _, cRaw := range cands {
			cand, ok := cRaw.(map[string]any)
			if !ok {
				continue
			}
			content, ok := cand["content"].(map[string]any)
			if !ok {
				continue
			}
			parts, ok := content["parts"].([]any)
			if !ok || len(parts) == 0 {
				continue
			}
			for _, pRaw := range parts {
				part, ok := pRaw.(map[string]any)
				if !ok {
					continue
				}
				if text, ok := part["text"].(string); ok && text != "" {
					return true
				}
				if thought, ok := part["thought"].(string); ok && thought != "" {
					return true
				}
				if thought, ok := part["thought"].(bool); ok && thought {
					return true
				}
				if fc, ok := part["functionCall"].(map[string]any); ok && fc != nil {
					return true
				}
				if ec, ok := part["executableCode"].(map[string]any); ok && ec != nil {
					return true
				}
				if cr, ok := part["codeExecutionResult"].(map[string]any); ok && cr != nil {
					return true
				}
				if id, ok := part["inlineData"].(map[string]any); ok && id != nil {
					return true
				}
				if fd, ok := part["fileData"].(map[string]any); ok && fd != nil {
					return true
				}
			}
		}
	}
	if fr := chunkFinishReason(chunk); fr == "SAFETY" {
		return true
	}
	if pf, ok := chunk["promptFeedback"].(map[string]any); ok {
		if br, _ := pf["blockReason"].(string); br != "" {
			return true
		}
	}
	return false
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
