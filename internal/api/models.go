package api

import (
	"net/http"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

// 本文件实现模型清单端点所依赖的工具函数。

// stripFakePrefix 检测并剥离假流式前缀，返回 (实际模型名, 是否假流式)。
func stripFakePrefix(model string, fakePrefixes []string) (string, bool) {
	for _, p := range fakePrefixes {
		if strings.HasPrefix(model, p) {
			return model[len(p):], true
		}
	}
	return model, false
}

// resolveConfiguredModel 统一处理假流式前缀、别名、模型启用和局部能力。
func resolveConfiguredModel(rawModel string, cfg config.ConfigProvider) (actualModel string, useFake bool, ok bool) {
	baseModel, requestedFake := stripFakePrefix(strings.TrimSpace(rawModel), cfg.FakePrefixes())
	actualModel = cfg.ResolveModelName(baseModel)
	entry, exists := cfg.LookupModel(actualModel)
	if !exists || !entry.Enabled {
		return actualModel, requestedFake, false
	}
	if requestedFake && (!cfg.FakeStreamEnabled() || !entry.FakeStreamEnabled) {
		return actualModel, true, false
	}
	return actualModel, requestedFake, true
}

func oaiModelNotFound(w http.ResponseWriter, model string) {
	writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{
		"message": "Model '" + model + "' not found.", "type": "invalid_request_error", "code": "model_not_found", "param": "model",
	}})
}

func geminiModelNotFound(w http.ResponseWriter, model string) {
	writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{
		"code": 404, "message": "Model '" + model + "' not found.", "status": "NOT_FOUND",
	}})
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
