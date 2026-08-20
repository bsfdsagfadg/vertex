package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/config"
	coremodel "github.com/bsfdsagfadg/vertex/internal/core/model"
	"github.com/bsfdsagfadg/vertex/internal/transform"
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

// resolveConfiguredModel resolves aliases and the model's explicit simulated-
// stream policy. Protocol behavior is never encoded in a synthetic model name.
func resolveConfiguredModel(rawModel string, cfg config.ConfigProvider) (actualModel string, useFake bool, ok bool) {
	requestedModel, prefixed := stripFakePrefix(strings.TrimSpace(rawModel), cfg.FakePrefixes())
	actualModel = cfg.ResolveModelName(requestedModel)
	entry, exists := cfg.LookupModel(actualModel)
	if !exists || !entry.Enabled {
		return actualModel, false, false
	}
	if prefixed && (!cfg.FakeStreamEnabled() || !entry.FakeStreamEnabled) {
		return actualModel, true, false
	}
	return actualModel, prefixed, true
}

func applyRequestModelPolicy(w http.ResponseWriter, payload map[string]any, modelID, configuredPolicy, dialect string) bool {
	fallback := coremodel.PolicyAdaptive
	if dialect == "gemini" {
		fallback = coremodel.PolicyPassthrough
	}
	diagnostics, err := transform.ApplyModelPolicy(payload, modelID, coremodel.ParsePolicy(configuredPolicy, fallback))
	if err != nil {
		var policyErr *transform.PolicyError
		if errors.As(err, &policyErr) {
			if dialect == "gemini" {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{
					"code": http.StatusBadRequest, "message": policyErr.Message, "status": "INVALID_ARGUMENT", "details": []any{},
				}})
			} else {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{
					"message": policyErr.Message, "type": "invalid_request_error", "param": policyErr.Param, "code": policyErr.Code,
				}})
			}
			return false
		}
		return false
	}
	if len(diagnostics) > 0 {
		codes := make([]string, 0, len(diagnostics))
		for _, diagnostic := range diagnostics {
			codes = append(codes, diagnostic.Code+":"+diagnostic.Param)
		}
		w.Header().Set("X-VProxy-Transform-Warnings", strings.Join(codes, ","))
	}
	return true
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
