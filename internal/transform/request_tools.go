package transform

import (
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/strutil"
)

// toolKeys 是 Vertex AI tools 列表里可与 functionDeclarations 共存的内置工具键集合。
var toolKeys = map[string]bool{ //nolint:gochecknoglobals
	"functionDeclarations": true, "googleSearch": true, "googleSearchRetrieval": true,
	"codeExecution": true, "retrieval": true, "urlContext": true,
	"computerUse": true, "mcpServer": true, "fileSearch": true, "googleMaps": true,
}

// normalizeToolsFormat 把 tools 归一为 Vertex AI 期望的 List[Tool]：
// 先 camelCase 化，再把裸 FunctionDeclaration 聚合进一个 functionDeclarations Tool，
// 其余携带 tool_keys 的条目（内置工具/已包好的 Tool）原样保留，二者可同时存在。
func normalizeToolsFormat(tools any) []any {
	converted := convertToolsFormat(tools)

	if cm, ok := converted.(map[string]any); ok {
		for k := range cm {
			if toolKeys[k] {
				return []any{cm}
			}
		}
		if _, ok := cm["name"]; ok {
			return []any{map[string]any{"functionDeclarations": []any{cm}}}
		}
		return nil
	}

	list, ok := converted.([]any)
	if !ok || len(list) == 0 {
		return nil
	}

	var normalized []any
	var funcDecls []any
	for _, item := range list {
		im, ok := item.(map[string]any)
		if !ok {
			continue
		}
		hasToolKey := false
		for k := range im {
			if toolKeys[k] {
				hasToolKey = true
				break
			}
		}
		if hasToolKey {
			normalized = append(normalized, im)
		} else if _, ok := im["name"]; ok {
			funcDecls = append(funcDecls, im)
		}
	}
	if len(funcDecls) > 0 {
		normalized = append([]any{map[string]any{"functionDeclarations": funcDecls}}, normalized...)
	}
	return normalized
}

// convertToolsFormat 递归把工具结构转为 camelCase。
func convertToolsFormat(data any) any {
	switch d := data.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, v := range d {
			switch k {
			case "function_declarations", "functionDeclarations":
				out["functionDeclarations"] = convertToolsFormat(v)
			case "google_search", "googleSearch":
				out["googleSearch"] = convertToolsFormatLeaf(v)
			case "google_search_retrieval", "googleSearchRetrieval":
				out["googleSearchRetrieval"] = convertToolsFormatLeaf(v)
			case "code_execution", "codeExecution":
				out["codeExecution"] = convertToolsFormatLeaf(v)
			case "url_context", "urlContext":
				out["urlContext"] = convertToolsFormatLeaf(v)
			case "name":
				if isTruthy(v) {
					out["name"] = v
				}
			case "parameters", "parametersJsonSchema", "parameters_json_schema":
				out["parameters"] = ToNativeSchema(v)
			default:
				camelKey := k
				if strings.Contains(k, "_") {
					camelKey = strutil.SnakeToCamel(k)
				}
				out[camelKey] = convertToolsFormatLeaf(v)
			}
		}
		return out
	case []any:
		out := make([]any, len(d))
		for i, item := range d {
			out[i] = convertToolsFormat(item)
		}
		return out
	default:
		return data
	}
}

// convertToolsFormatLeaf 仅对 dict/list 递归，标量原样返回。
func convertToolsFormatLeaf(v any) any {
	switch v.(type) {
	case map[string]any, []any:
		return convertToolsFormat(v)
	default:
		return v
	}
}

// asAnySlice 把 any 规整为 []any（非数组返回 nil）。
func asAnySlice(v any) []any {
	if arr, ok := v.([]any); ok {
		return arr
	}
	return nil
}
