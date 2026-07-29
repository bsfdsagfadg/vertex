package transform

import (
	"strings"
)

// truthyStr 判断一个 any 是否为"非空字符串"。
func truthyStr(v any) bool {
	s, ok := v.(string)
	return ok && strings.TrimSpace(s) != ""
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
