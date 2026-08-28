package transform

import (
	"fmt"
	"strings"
)

// toolKeys 是 Vertex AI tools 列表里可与 functionDeclarations 共存的内置工具键集合。
var toolKeys = map[string]bool{ //nolint:gochecknoglobals
	"functionDeclarations": true, "googleSearch": true, "googleSearchRetrieval": true,
	"codeExecution": true, "retrieval": true, "urlContext": true,
	"computerUse": true, "mcpServer": true, "fileSearch": true, "googleMaps": true,
}

// convertTools 把 OpenAI tools（或 legacy functions）转为 functionDeclarations，写入
// geminiPayload["tools"]。返回已声明的工具名集合（供 tool_choice 校验）。
func convertTools(body, geminiPayload map[string]any) (map[string]bool, error) {
	oaiTools, _ := body["tools"].([]any)
	// 兼容已废弃的顶层 functions 字段（无 tools 时回退）。
	if len(oaiTools) == 0 {
		if fns, ok := body["functions"].([]any); ok {
			for _, f := range fns {
				oaiTools = append(oaiTools, map[string]any{"type": "function", "function": f})
			}
		}
	}
	declared := map[string]bool{}
	if len(oaiTools) == 0 {
		return declared, nil
	}

	var funcDecls []any
	var builtins []any
	for _, t := range oaiTools {
		if builtin := normalizeBuiltinTool(t); builtin != nil {
			builtins = append(builtins, builtin)
			continue
		}
		f := extractOAIFunctionTool(t)
		if f == nil {
			continue
		}
		decl := map[string]any{"name": f["name"]}
		declared[toString(f["name"])] = true
		if isTruthy(f["description"]) {
			decl["description"] = f["description"]
		}
		if params, ok := f["parameters"].(map[string]any); ok && len(params) > 0 {
			// 对 parameters 递归白名单清洗，剔除 Gemini 不支持的 schema 字段。
			decl["parameters"] = cleanFunctionParameters(deepCopyAny(params))
		} else {
			// 缺省 parameters 时补默认空对象 schema，满足 Gemini functionDeclarations 要求。
			decl["parameters"] = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		funcDecls = append(funcDecls, decl)
	}
	if len(funcDecls) > 0 || len(builtins) > 0 {
		tools := make([]any, 0, 1+len(builtins))
		if len(funcDecls) > 0 {
			tools = append(tools, map[string]any{"functionDeclarations": funcDecls})
		}
		tools = append(tools, builtins...)
		geminiPayload["tools"] = tools
	}
	return declared, nil
}

// normalizeBuiltinTool maps OpenAI web-search aliases to Gemini's canonical
// googleSearch tool while preserving already-native tool objects.
func normalizeBuiltinTool(tool any) map[string]any {
	m, ok := tool.(map[string]any)
	if !ok {
		return nil
	}
	typ, _ := m["type"].(string)
	switch strings.ToLower(typ) {
	case "web_search", "web_search_2", "web_search_preview", "google_search", "google_search_2":
		out := map[string]any{"googleSearch": map[string]any{}}
		if cfg, ok := m["googleSearch"].(map[string]any); ok {
			out["googleSearch"] = cfg
		}
		return out
	}
	if _, ok := m["googleSearch"]; ok {
		return convertToolsFormat(m).(map[string]any)
	}
	if _, ok := m["google_search"]; ok {
		return convertToolsFormat(m).(map[string]any)
	}
	return nil
}

// convertToolChoice 把 tool_choice（或 legacy function_call）转为 toolConfig。
func convertToolChoice(body, geminiPayload map[string]any, declared map[string]bool) error {
	tc := firstPresentRaw(body, "tool_choice", "function_call")
	if tc == nil || !isTruthy(tc) {
		return nil
	}
	switch v := tc.(type) {
	case string:
		switch v {
		case "none":
			geminiPayload["toolConfig"] = map[string]any{"functionCallingConfig": map[string]any{"mode": "NONE"}}
		case "auto":
			geminiPayload["toolConfig"] = map[string]any{"functionCallingConfig": map[string]any{"mode": "AUTO"}}
		case "required":
			if len(declared) == 0 {
				return fmt.Errorf("tool_choice='required' requires at least one tool")
			}
			geminiPayload["toolConfig"] = map[string]any{"functionCallingConfig": map[string]any{"mode": "ANY"}}
		}
	case map[string]any:
		var fnName string
		if v["type"] == "function" {
			if fn, ok := v["function"].(map[string]any); ok {
				fnName, _ = fn["name"].(string)
			}
		} else if n, ok := v["name"].(string); ok {
			fnName = n
		}
		if fnName != "" {
			if len(declared) > 0 && !declared[fnName] {
				return fmt.Errorf("tool_choice references unknown function: %s", fnName)
			}
			geminiPayload["toolConfig"] = map[string]any{"functionCallingConfig": map[string]any{
				"mode": "ANY", "allowedFunctionNames": []any{fnName},
			}}
		}
	}
	return nil
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
			case "google_search", "google_search_2", "web_search", "web_search_2", "googleSearch":
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
				out["parameters"] = toNativeSchema(v)
			default:
				camelKey := k
				if strings.Contains(k, "_") {
					camelKey = SnakeToCamel(k)
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
