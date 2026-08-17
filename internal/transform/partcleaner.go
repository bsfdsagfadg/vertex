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
	names := functionCallNames
	if fc != nil {
		names = fc.names
	}
	return cleanPartWithID(part, names, 0, nil)
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
	return cleaned
}
