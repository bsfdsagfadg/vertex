package transform

import (
	"fmt"
	"strings"
)

// 本文件是强类型请求转换的工具声明/工具选择/generationConfig 部分。

// convertToolsTyped 把 OpenAI tools（或 legacy functions）转为 Gemini Tool 列表，
// 写入 gemini.Tools，返回声明的工具名集合（供 tool_choice 校验）。
func convertToolsTyped(req *ChatCompletionRequest, gemini *GeminiRequest) map[string]bool {
	declared := map[string]bool{}
	if len(req.Tools) == 0 && len(req.Functions) > 0 {
		for _, fn := range req.Functions {
			if fn.Name != "" {
				declared[fn.Name] = true
			}
		}
		if len(declared) > 0 {
			gemini.Tools = append(gemini.Tools, Tool{FunctionDeclarations: toolDeclsFromFunctions(req.Functions)})
		}
		return declared
	}
	if len(req.Tools) == 0 {
		return declared
	}
	var decls []FunctionDeclaration
	var hasGoogleSearch bool
	for _, t := range req.Tools {
		switch t.Type {
		case "function":
			if t.Function.Name != "" {
				d := FunctionDeclaration{Name: t.Function.Name, Description: t.Function.Description}
				if len(t.Function.Parameters) > 0 {
					d.Parameters = cleanFunctionParameters(DeepCopyAny(t.Function.Parameters))
				} else {
					d.Parameters = map[string]any{"type": "object", "properties": map[string]any{}}
				}
				decls = append(decls, d)
				declared[t.Function.Name] = true
			}
		case "web_search", "web_search_2", "google_search", "googleSearch":
			hasGoogleSearch = true
		default:
			// 兼容某些客户端直接传入自定义类型或包含 googleSearch / web_search 字段的 tool 对象
			if t.RawMap != nil {
				_, ok1 := t.RawMap["googleSearch"]
				_, ok2 := t.RawMap["google_search"]
				_, ok3 := t.RawMap["web_search"]
				if ok1 || ok2 || ok3 {
					hasGoogleSearch = true
				}
			}
		}
	}

	if len(decls) > 0 || hasGoogleSearch {
		tool := Tool{}
		if len(decls) > 0 {
			tool.FunctionDeclarations = decls
		}
		if hasGoogleSearch {
			tool.GoogleSearch = map[string]any{}
		}
		gemini.Tools = append(gemini.Tools, tool)
	}
	return declared
}

// toolDeclsFromFunctions 由 legacy functions 数组构建函数声明。
func toolDeclsFromFunctions(fns []OAIFunction) []FunctionDeclaration {
	decls := make([]FunctionDeclaration, 0, len(fns))
	for _, fn := range fns {
		decl := FunctionDeclaration{Name: fn.Name, Description: fn.Description}
		if len(fn.Parameters) > 0 {
			decl.Parameters = cleanFunctionParameters(deepCopyAny(fn.Parameters))
		} else {
			decl.Parameters = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		decls = append(decls, decl)
	}
	return decls
}

// convertToolChoiceTyped 把 tool_choice（或 legacy function_call）转为 toolConfig。
func convertToolChoiceTyped(req *ChatCompletionRequest, gemini *GeminiRequest, declared map[string]bool) error {
	tc := req.ToolChoice
	if tc == nil {
		return nil
	}
	if s, ok := tc.(string); ok {
		switch s {
		case "none":
			gemini.ToolConfig = &ToolConfig{FunctionCallingConfig: &FunctionCallingConfig{Mode: "NONE"}}
		case "auto":
			gemini.ToolConfig = &ToolConfig{FunctionCallingConfig: &FunctionCallingConfig{Mode: "AUTO"}}
		case "required":
			if len(declared) == 0 {
				return fmt.Errorf("tool_choice='required' requires at least one tool")
			}
			gemini.ToolConfig = &ToolConfig{FunctionCallingConfig: &FunctionCallingConfig{Mode: "ANY"}}
		}
		return nil
	}
	if m, ok := tc.(map[string]any); ok {
		var fnName string
		if m["type"] == "function" {
			if fn, ok := m["function"].(map[string]any); ok {
				fnName, _ = fn["name"].(string)
			}
		} else if n, ok := m["name"].(string); ok {
			fnName = n
		}
		if fnName != "" {
			if len(declared) > 0 && !declared[fnName] {
				return fmt.Errorf("tool_choice references unknown function: %s", fnName)
			}
			gemini.ToolConfig = &ToolConfig{FunctionCallingConfig: &FunctionCallingConfig{
				Mode: "ANY", AllowedFunctionNames: []string{fnName},
			}}
		}
	}
	return nil
}

// convertGenConfigTyped 组装 generationConfig（不含 thinking/image 默认注入）。
func convertGenConfigTyped(req *ChatCompletionRequest, cfg ConfigFace) *GenerationConfig {
	gc := &GenerationConfig{}

	if req.Temperature != nil {
		gc.Temperature = req.Temperature
	}
	if req.TopP != nil {
		gc.TopP = req.TopP
	}
	if req.TopK != nil {
		v := *req.TopK
		if v > 63 {
			v = 63
		}
		gc.TopK = &v
	}
	if req.PresencePenalty != nil {
		gc.PresencePenalty = req.PresencePenalty
	}
	if req.FrequencyPenalty != nil {
		gc.FrequencyPenalty = req.FrequencyPenalty
	}
	if req.Seed != nil {
		gc.Seed = req.Seed
	}
	if req.Logprobs != nil {
		gc.ResponseLogprobs = req.Logprobs
	}
	if req.TopLogprobs != nil {
		v := *req.TopLogprobs
		gc.Logprobs = &v
	}

	maxTokens := req.MaxTokens
	if maxTokens == nil {
		maxTokens = req.MaxCompletionTokens
	}
	if maxTokens != nil {
		if *maxTokens < 1 {
			return gc
		}
		if cfg == nil || !cfg.DropMaxTokens() {
			gc.MaxOutputTokens = maxTokens
		}
	}

	if len(req.Stop) > 0 {
		gc.StopSequences = req.Stop
	}

	if rf := req.ResponseFormat; rf != nil {
		switch rf.Type {
		case "json_object":
			gc.ResponseMimeType = "application/json"
		case "json_schema":
			gc.ResponseMimeType = "application/json"
			if rf.JSONSchema != nil && rf.JSONSchema.Schema != nil {
				gc.ResponseSchema = rf.JSONSchema.Schema
			}
		}
	}

	if mr := normalizeMediaContentResolution(req.MediaResolution, req.ExtraBody); mr != "" {
		gc.MediaResolution = mr
	}

	if re := strings.ToLower(strings.TrimSpace(req.ReasoningEffort)); re != "" {
		if level, ok := reasoningEffortToThinkingLevel[re]; ok {
			gc.ThinkingConfig = &ThinkingConfig{ThinkingLevel: level}
		}
	}

	if req.Thinking != nil {
		tc := &ThinkingConfig{}
		switch req.Thinking.Type {
		case "enabled":
			tc.ThinkingLevel = "MEDIUM"
		case "disabled":
			tc.ThinkingLevel = "NONE"
		default:
			tc = nil
		}
		if tc != nil {
			if req.Thinking.BudgetTokens != nil {
				tc.ThinkingBudget = budgetToInt(req.Thinking.BudgetTokens)
			}
			gc.ThinkingConfig = tc
		}
	}

	return gc
}

// budgetToInt 把任意 JSON 数字转为 *int。
func budgetToInt(v any) *int {
	switch n := v.(type) {
	case float64:
		return intPtr(int(n))
	case float32:
		return intPtr(int(n))
	case int:
		return intPtr(n)
	case int64:
		return intPtr(int(n))
	case int32:
		return intPtr(int(n))
	default:
		return nil
	}
}

// normalizeMediaContentResolution 解析 media_resolution（顶层或 extra_body）。
func normalizeMediaContentResolution(top string, extra map[string]any) string {
	if top != "" {
		if mr := normalizeMediaResolution(top); mr != "" {
			return mr
		}
	}
	if extra != nil {
		for _, k := range []string{"media_resolution", "mediaResolution"} {
			if v, ok := extra[k]; ok {
				if mr := normalizeMediaResolution(v); mr != "" {
					return mr
				}
			}
		}
	}
	return ""
}

// hasGenConfig 判断 generationConfig 是否有任何非零字段。
func hasGenConfig(gc *GenerationConfig) bool {
	if gc == nil {
		return false
	}
	return len(gc.StopSequences) > 0 ||
		gc.MaxOutputTokens != nil ||
		gc.Temperature != nil ||
		gc.TopP != nil ||
		gc.TopK != nil ||
		gc.PresencePenalty != nil ||
		gc.FrequencyPenalty != nil ||
		gc.Seed != nil ||
		gc.Logprobs != nil ||
		gc.ResponseLogprobs != nil ||
		gc.ResponseMimeType != "" ||
		gc.ResponseSchema != nil ||
		gc.ResponseModalities != nil ||
		gc.ThinkingConfig != nil ||
		gc.ImageConfig != nil ||
		gc.MediaResolution != "" ||
		gc.SpeechConfig != nil ||
		gc.RoutingConfig != nil
}

// safetyCategories 是默认安全设置覆盖的 6 个类别（缺省全 BLOCK_NONE）。
var safetyCategories = []string{ //nolint:gochecknoglobals
	"HARM_CATEGORY_HARASSMENT",
	"HARM_CATEGORY_HATE_SPEECH",
	"HARM_CATEGORY_SEXUALLY_EXPLICIT",
	"HARM_CATEGORY_DANGEROUS_CONTENT",
	"HARM_CATEGORY_CIVIC_INTEGRITY",
	"HARM_CATEGORY_JAILBREAK",
}

// BuildSafetySettingsTyped 构建默认安全设置。
func BuildSafetySettingsTyped(cfg ConfigFace) []SafetySetting {
	if cfg == nil {
		return nil
	}
	settings := make([]SafetySetting, 0, len(safetyCategories))
	for _, cat := range safetyCategories {
		threshold := "BLOCK_NONE"
		if t, ok := cfg.SafetySettings()[cat]; ok && t != "" {
			threshold = t
		}
		settings = append(settings, SafetySetting{Category: cat, Threshold: threshold})
	}
	return settings
}

// deepCopyAny 深拷贝 map/slice 结构。
func deepCopyAny(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = deepCopyAny(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = deepCopyAny(item)
		}
		return out
	default:
		return v
	}
}