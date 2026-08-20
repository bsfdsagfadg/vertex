package transform

import (
	"github.com/bsfdsagfadg/vertex/internal/strutil"
)

// truthyStr 判断一个 any 是否为"非空字符串"（委托至 strutil.NonBlankStr）。
func truthyStr(v any) bool {
	return strutil.NonBlankStr(v)
}

// CleanPart 清洗单个 part，去除空字段、修复边界情况。
func CleanPart(part map[string]any, functionCallNames []string, fc *FcNameTracker) (map[string]any, bool) {
	names := functionCallNames
	if fc != nil {
		names = fc.names
	}
	return cleanPartWithID(part, names, 0, nil)
}

// CleanStreamPart 清洗流式增量中的单个 part，移除内部 protobuf 空字段，并对 args/response 规范化。
func CleanStreamPart(part map[string]any) map[string]any {
	cleaned := copyMap(part)
	delete(cleaned, "data")

	if fd, ok := cleaned["fileData"].(map[string]any); ok {
		if strutil.ToStr(fd["fileUri"]) == "" && strutil.ToStr(fd["mimeType"]) == "" {
			delete(cleaned, "fileData")
		}
	}

	if fc, ok := cleaned["functionCall"].(map[string]any); ok {
		hasName := strutil.ToStr(fc["name"]) != ""
		hasArgs := false
		if args, ok := fc["args"]; ok && args != nil {
			if m, ok := args.(map[string]any); ok {
				hasArgs = len(m) > 0
			} else {
				hasArgs = true
			}
		}
		if !hasName && !hasArgs {
			delete(cleaned, "functionCall")
		} else if _, ok := fc["name"].(string); ok {
			cleaned["functionCall"] = fixFunctionCallArgs(fc)
		}
	}

	if fr, ok := cleaned["functionResponse"].(map[string]any); ok {
		hasName := strutil.ToStr(fr["name"]) != ""
		hasResp := false
		if resp, ok := fr["response"]; ok && resp != nil {
			if m, ok := resp.(map[string]any); ok {
				hasResp = len(m) > 0
			} else {
				hasResp = true
			}
		}
		if !hasName && !hasResp {
			delete(cleaned, "functionResponse")
		} else {
			normalizeFunctionResponseBody(fr)
		}
	}

	if id, ok := cleaned["inlineData"].(map[string]any); ok {
		if strutil.ToStr(id["data"]) == "" {
			delete(cleaned, "inlineData")
		}
	}

	for _, key := range []string{"executableCode", "codeExecutionResult"} {
		if v, ok := cleaned[key]; ok && isTruthy(v) {
			return cleaned
		}
	}

	for k := range cleaned {
		switch k {
		case "thought", "thoughtSignature":
			continue
		default:
			return cleaned
		}
	}
	return nil
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
