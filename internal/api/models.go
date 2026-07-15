package api

import "strings"

// 本文件实现模型清单端点所依赖的工具函数。

// stripFakePrefix 检测并剥离假非流前缀，返回 (实际模型名, 是否假非流)。
func stripFakePrefix(model string, fakePrefixes []string) (string, bool) {
	for _, p := range fakePrefixes {
		if strings.HasPrefix(model, p) {
			return model[len(p):], true
		}
	}
	return model, false
}

// supportedGenerationMethods 返回模型详情里声明的支持方法（本代理统一支持这三种）。
func supportedGenerationMethods() []any {
	return []any{"generateContent", "streamGenerateContent", "countTokens"}
}

// geminiModelInfo 构造单个 Gemini 模型详情对象（供 get_model_info / list_models_gemini 用）。
func geminiModelInfo(name string) map[string]any {
	return map[string]any{
		"name":                       "models/" + name,
		"version":                    name,
		"displayName":                name,
		"description":                "Vertex AI Studio anonymous model",
		"inputTokenLimit":            1048576,
		"outputTokenLimit":           65536,
		"supportedGenerationMethods": supportedGenerationMethods(),
	}
}
