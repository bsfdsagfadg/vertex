package transform

import (
	"strings"
)

// truthyStr 判断一个 any 是否为"非空字符串"。
func truthyStr(v any) bool {
	s, ok := v.(string)
	return ok && strings.TrimSpace(s) != ""
}

// CleanPart 清洗单个 part，去除空字段、修复边界情况。
func CleanPart(part map[string]any, functionCallNames []string, fc *FcNameTracker) (map[string]any, bool) {
	hasValid := false
	cleaned := map[string]any{}

	if v, ok := part["text"]; ok {
		if v != nil && toString(v) != "" {
			cleaned["text"] = v
			hasValid = true
		}
	}

	if v, ok := part["thought"]; ok {
		cleaned["thought"] = v
	}

	if fcRaw, ok := part["functionCall"]; ok {
		if fcMap, ok := fcRaw.(map[string]any); ok {
			if truthyStr(fcMap["name"]) {
				cleaned["functionCall"] = fixFunctionCallArgs(fcMap)
				hasValid = true
			}
		}
	}

	if frRaw, ok := part["functionResponse"]; ok {
		if frMap, ok := frRaw.(map[string]any); ok {
			currentName, _ := frMap["name"].(string)
			if strings.TrimSpace(currentName) == "" {
				inferred := ""
				if fc != nil {
					if n, ok := fc.NextName(); ok {
						inferred = n
					}
				} else if len(functionCallNames) > 0 {
					inferred = functionCallNames[len(functionCallNames)-1]
				}
				if inferred != "" {
					fixed := copyMap(frMap)
					fixed["name"] = inferred
					normalizeFunctionResponseBody(fixed)
					cleaned["functionResponse"] = fixed
					hasValid = true
				}
			} else {
				fixed := copyMap(frMap)
				normalizeFunctionResponseBody(fixed)
				cleaned["functionResponse"] = fixed
				hasValid = true
			}
		}
	}

	if idRaw, ok := part["inlineData"]; ok {
		if id, ok := idRaw.(map[string]any); ok {
			if truthyStr(id["data"]) && truthyStr(id["mimeType"]) {
				cleaned["inlineData"] = idRaw
				hasValid = true
			}
		}
	}

	if fdRaw, ok := part["fileData"]; ok {
		if fd, ok := fdRaw.(map[string]any); ok {
			if truthyStr(fd["fileUri"]) && truthyStr(fd["mimeType"]) {
				cleaned["fileData"] = fdRaw
				hasValid = true
			}
		}
	}

	for _, key := range []string{"executableCode", "codeExecutionResult"} {
		if v, ok := part[key]; ok && isTruthy(v) {
			cleaned[key] = v
			hasValid = true
		}
	}

	if v, ok := part["thoughtSignature"]; ok {
		cleaned["thoughtSignature"] = v
	}

	for _, key := range []string{"videoMetadata", "mediaResolution"} {
		if v, ok := part[key]; ok && isTruthy(v) {
			cleaned[key] = v
		}
	}

	finalizeCleanedPart(cleaned)

	if hasValid {
		return cleaned, true
	}
	return nil, false
}

// normalizeFunctionResponseBody 把 functionResponse.response 的非对象值包成 {"result": ...}。
func normalizeFunctionResponseBody(fr map[string]any) {
	if resp, ok := fr["response"]; ok {
		if _, isMap := resp.(map[string]any); !isMap {
			fr["response"] = map[string]any{"result": resp}
		}
	}
}

// cleanSimple 是用于内容块合并的轻量清洗。
func cleanSimple(part map[string]any) map[string]any {
	cleaned := copyMap(part)
	if t, ok := cleaned["text"]; ok {
		if toString(t) == "" {
			delete(cleaned, "text")
		}
	}
	if fcRaw, ok := cleaned["functionCall"]; ok {
		if fc, ok := fcRaw.(map[string]any); ok {
			if !truthyStr(fc["name"]) {
				delete(cleaned, "functionCall")
			}
		}
	}
	if len(cleaned) == 0 {
		return nil
	}
	// 如果仅剩 thought/thoughtSignature，保留（非流式合并时思考块不能被清空）
	return cleaned
}
