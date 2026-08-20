package transform

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/samber/lo"

	"github.com/bsfdsagfadg/vertex/internal/strutil"
)

const skipThoughtSentinel = "skip_thought_signature_validator"

// NormalizeBase64 规范化 base64（委托至 strutil.NormalizeBase64 统一维护）。
func NormalizeBase64(data string) string {
	return strutil.NormalizeBase64(data)
}

// FcNameTracker 按出现顺序追踪 functionCall 名称。
type FcNameTracker struct {
	names []string
	idx   int
}

// NewFcNameTracker 过滤掉空名后构造追踪器。
func NewFcNameTracker(names []string) *FcNameTracker {
	filtered := lo.Filter(names, func(n string, _ int) bool {
		return strings.TrimSpace(n) != ""
	})
	return &FcNameTracker{names: filtered}
}

// NextName 返回下一个未用的名称，用尽返回 ("", false)。
func (t *FcNameTracker) NextName() (string, bool) {
	if t.idx < len(t.names) {
		name := strings.TrimSpace(t.names[t.idx])
		t.idx++
		if name != "" {
			return name, true
		}
	}
	return "", false
}

// cleanPartWithID 是 CleanPart 的 id 锚点版本。
func cleanPartWithID(part map[string]any, functionCallNames []string, responseIndex int, callIDMap map[string]string) (map[string]any, bool) {
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
				fixed := fixFunctionCallArgs(fcMap)
				delete(fixed, "id")
				cleaned["functionCall"] = fixed
				hasValid = true
			}
		}
	}

	if frRaw, ok := part["functionResponse"]; ok {
		if frMap, ok := frRaw.(map[string]any); ok {
			name := strings.TrimSpace(toString(frMap["name"]))
			if name == "" {
				if fid, _ := frMap["id"].(string); fid != "" && callIDMap != nil {
					name = callIDMap[fid]
				}
				if name == "" && responseIndex >= 0 && responseIndex < len(functionCallNames) {
					name = functionCallNames[responseIndex]
				}
				if name == "" {
					name = "unknown"
				}
			}
			fixed := copyMap(frMap)
			fixed["name"] = name
			delete(fixed, "id")
			normalizeFunctionResponseBody(fixed)
			cleaned["functionResponse"] = fixed
			hasValid = true
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

// fixFunctionCallArgs 拷贝 functionCall 并把字符串 args 解析为对象。
func fixFunctionCallArgs(fc map[string]any) map[string]any {
	fixed := copyMap(fc)
	if argStr, ok := fixed["args"].(string); ok {
		var parsed any
		if err := json.Unmarshal([]byte(argStr), &parsed); err == nil {
			fixed["args"] = parsed
		} else {
			fixed["args"] = map[string]any{"raw": argStr}
		}
	}
	return fixed
}

// finalizeCleanedPart 对清洗后的 part 做收尾归一。
func finalizeCleanedPart(cleaned map[string]any) {
	if tv, ok := cleaned["thought"]; ok {
		if _, isStr := tv.(string); !isStr {
			if _, isBool := tv.(bool); !isBool {
				cleaned["thought"] = ""
			}
		}
	}

	if _, ok := cleaned["functionResponse"]; ok {
		delete(cleaned, "thought")
		delete(cleaned, "thoughtSignature")
	} else {
		_, hasFC := cleaned["functionCall"]
		_, hasThought := cleaned["thought"]
		_, hasSig := cleaned["thoughtSignature"]
		if (hasFC || hasThought) && !hasSig {
			cleaned["thoughtSignature"] = skipThoughtSentinel
		}
	}

	if truthyStr(cleaned["text"]) && !isTruthy(cleaned["thought"]) {
		delete(cleaned, "thought")
		delete(cleaned, "thoughtSignature")
	}
}

func ensureBase64Signature(signature string) string {
	if signature == skipThoughtSentinel {
		return base64.StdEncoding.EncodeToString([]byte(signature))
	}
	normalized := NormalizeBase64(signature)
	decoded, err := base64.StdEncoding.DecodeString(normalized)
	if err == nil && base64.StdEncoding.EncodeToString(decoded) == normalized {
		return normalized
	}
	return base64.StdEncoding.EncodeToString([]byte(signature))
}

// EncodeThoughtSignature recursively normalizes thought signatures to standard Base64.
func EncodeThoughtSignature(contents any, depth int) any {
	const maxDepth = 64
	if depth > maxDepth {
		return contents
	}
	switch v := contents.(type) {
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = EncodeThoughtSignature(item, depth+1)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, val := range v {
			if k == "parts" {
				if parts, ok := val.([]any); ok {
					newParts := make([]any, len(parts))
					for i, p := range parts {
						if pm, ok := p.(map[string]any); ok {
							np := copyMap(pm)
							if sig, ok := np["thoughtSignature"].(string); ok && sig != "" {
								np["thoughtSignature"] = ensureBase64Signature(sig)
							}
							newParts[i] = np
						} else {
							newParts[i] = p
						}
					}
					out[k] = newParts
					continue
				}
			}
			switch val.(type) {
			case map[string]any, []any:
				out[k] = EncodeThoughtSignature(val, depth+1)
			default:
				out[k] = val
			}
		}
		return out
	default:
		return contents
	}
}

// HandleBase64InContents 递归规范化 contents 中 inlineData 的 base64 数据。
func HandleBase64InContents(contents any) any {
	switch v := contents.(type) {
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = HandleBase64InContents(item)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, val := range v {
			if k == "inlineData" {
				if id, ok := val.(map[string]any); ok {
					if data, ok := id["data"].(string); ok {
						ni := copyMap(id)
						ni["data"] = NormalizeBase64(data)
						out[k] = ni
						continue
					}
				}
			}
			out[k] = HandleBase64InContents(val)
		}
		return out
	default:
		return contents
	}
}

// MergeContentBlocks 合并相邻同类型文本块（thought+thought、text+text）。
func MergeContentBlocks(parts []map[string]any) []map[string]any {
	cleaned := make([]map[string]any, 0, len(parts))
	for _, p := range parts {
		if c := cleanSimple(p); c != nil {
			cleaned = append(cleaned, c)
		}
	}
	if len(cleaned) == 0 {
		return []map[string]any{}
	}

	merged := make([]map[string]any, 0, len(cleaned))
	var current map[string]any

	for _, part := range cleaned {
		isText := truthyStr(part["text"])
		if !isText {
			merged = append(merged, part)
			current = nil
			continue
		}
		isThought := isTruthy(part["thought"])
		if current != nil && isTruthy(current["thought"]) == isThought {
			current["text"] = toString(current["text"]) + toString(part["text"])
			if sig, ok := part["thoughtSignature"]; ok {
				if _, exists := current["thoughtSignature"]; !exists {
					current["thoughtSignature"] = sig
				}
			}
		} else {
			np := map[string]any{"text": toString(part["text"])}
			if isThought {
				np["thought"] = true
				if sig, ok := part["thoughtSignature"]; ok {
					np["thoughtSignature"] = sig
				}
			}
			merged = append(merged, np)
			current = np
		}
	}
	return merged
}
