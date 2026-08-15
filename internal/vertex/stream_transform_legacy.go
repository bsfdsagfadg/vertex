
// Package vertex 实现与 Google 匿名 batchGraphql 端点交互的核心请求循环。
package vertex

import "encoding/json"

// ── legacy map 路径（仅畸形嵌套帧回退用）──
// 本文件承载 decodeChunkTyped 快路径失败时的 map 兜底三函数（extractChunk /
// cleanStreamParts / cleanPart），语义与 typed 快路径严格等价（见双轨对照测试）。

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
