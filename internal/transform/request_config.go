package transform

import (
	"fmt"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/domain"
	"github.com/bsfdsagfadg/vertex/internal/strutil"
)

// buildGenerationConfigTyped 根据强类型的 ChatCompletionRequest 构建 GenerationConfig。
func buildGenerationConfigTyped(req *domain.ChatCompletionRequest, cfg config.ConfigProvider) (*domain.GenerationConfig, error) {
	if req == nil {
		return nil, nil
	}

	genCfg := &domain.GenerationConfig{}
	hasAny := false

	if req.Temperature != nil {
		genCfg.Temperature = req.Temperature
		hasAny = true
	}
	if req.TopP != nil {
		genCfg.TopP = req.TopP
		hasAny = true
	}
	if req.TopK != nil {
		genCfg.TopK = clampTopKInt(req.TopK)
		hasAny = true
	}
	if req.PresencePenalty != nil {
		genCfg.PresencePenalty = req.PresencePenalty
		hasAny = true
	}
	if req.FrequencyPenalty != nil {
		genCfg.FrequencyPenalty = req.FrequencyPenalty
		hasAny = true
	}
	if req.Seed != nil {
		genCfg.Seed = req.Seed
		hasAny = true
	}
	if req.Logprobs != nil {
		genCfg.ResponseLogprobs = req.Logprobs
		hasAny = true
	}
	if req.TopLogprobs != nil {
		genCfg.Logprobs = req.TopLogprobs
		hasAny = true
	}

	// MaxTokens / MaxCompletionTokens
	var maxTokens *int
	if req.MaxTokens != nil {
		maxTokens = req.MaxTokens
	} else if req.MaxCompletionTokens != nil {
		maxTokens = req.MaxCompletionTokens
	}
	if maxTokens != nil {
		if *maxTokens < 1 {
			return nil, fmt.Errorf("max_tokens must be an integer >= 1")
		}
		if cfg == nil || !cfg.DropMaxTokens() {
			genCfg.MaxOutputTokens = maxTokens
			hasAny = true
		}
	}

	// Stop sequences
	if req.Stop != nil {
		switch s := req.Stop.(type) {
		case string:
			if s != "" {
				genCfg.StopSequences = []string{s}
				hasAny = true
			}
		case []string:
			if len(s) > 0 {
				genCfg.StopSequences = s
				hasAny = true
			}
		case []any:
			var stops []string
			for _, item := range s {
				if str := toString(item); str != "" {
					stops = append(stops, str)
				}
			}
			if len(stops) > 0 {
				genCfg.StopSequences = stops
				hasAny = true
			}
		}
	}

	// Response format
	if req.ResponseFormat != nil {
		t := strings.ToLower(strings.TrimSpace(req.ResponseFormat.Type))
		if t == "json_object" || t == "json_schema" {
			genCfg.ResponseMimeType = "application/json"
			hasAny = true
			if t == "json_schema" && req.ResponseFormat.JSONSchema != nil && req.ResponseFormat.JSONSchema.Schema != nil {
				genCfg.ResponseSchema = ToNativeSchema(req.ResponseFormat.JSONSchema.Schema)
			}
		}
	}

	// Media resolution
	mr := req.MediaResolution
	if mr == "" && req.ExtraBody != nil {
		if v, ok := req.ExtraBody["media_resolution"]; ok {
			mr = toString(v)
		} else if v, ok := req.ExtraBody["mediaResolution"]; ok {
			mr = toString(v)
		}
	}
	if mr != "" {
		if normalizedMR := normalizeMediaResolution(mr); normalizedMR != "" {
			genCfg.MediaResolution = normalizedMR
			hasAny = true
		}
	}

	// Reasoning Effort / Thinking
	if req.ReasoningEffort != "" {
		if level, ok := reasoningEffortToThinkingLevel[strings.ToLower(strings.TrimSpace(req.ReasoningEffort))]; ok {
			if genCfg.ThinkingConfig == nil {
				genCfg.ThinkingConfig = &domain.ThinkingConfig{}
			}
			genCfg.ThinkingConfig.ThinkingLevel = level
			hasAny = true
		}
	}

	if req.Thinking != nil {
		if genCfg.ThinkingConfig == nil {
			genCfg.ThinkingConfig = &domain.ThinkingConfig{}
		}
		tt := strings.ToLower(strings.TrimSpace(req.Thinking.Type))
		if tt == "enabled" || tt == "disabled" {
			genCfg.ThinkingConfig.ThinkingLevel = "MEDIUM"
			if tt == "disabled" {
				genCfg.ThinkingConfig.ThinkingLevel = "NONE"
			}
			if req.Thinking.ThinkingBudget != nil {
				genCfg.ThinkingConfig.ThinkingBudget = req.Thinking.ThinkingBudget
			}
			hasAny = true
		}
	}

	if !hasAny {
		return nil, nil
	}
	return genCfg, nil
}

func clampTopKInt(topK *int) *int {
	if topK == nil {
		return nil
	}
	val := *topK
	if val > 63 {
		val = 63
	}
	return &val
}


// buildGenerationConfig 构建 generationConfig。
func buildGenerationConfig(geminiPayload map[string]any) map[string]any {
	final := map[string]any{}
	if ugc, ok := geminiPayload["generationConfig"].(map[string]any); ok {
		for k, v := range ugc {
			final[k] = v
		}
	} else if ugc, ok := geminiPayload["generation_config"].(map[string]any); ok {
		for k, v := range ugc {
			final[k] = v
		}
	}
	return convertToGeminiFormat(final)
}

// convertToGeminiFormat 把 generationConfig 转为 Gemini 期望格式。
func convertToGeminiFormat(cfg map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range cfg {
		camelKey := strutil.SnakeToCamel(k)
		switch camelKey {
		case "thinkingConfig":
			if vm, ok := v.(map[string]any); ok {
				tc, _ := camelizeNested(vm).(map[string]any)
				if lvl, ok := tc["thinkingLevel"].(string); ok {
					tc["thinkingLevel"] = strings.ToUpper(lvl)
				}
				out[camelKey] = tc
				continue
			}
			out[camelKey] = v
		case "imageConfig", "speechConfig", "audioTimestamp", "routingConfig":
			if vm, ok := v.(map[string]any); ok {
				out[camelKey] = camelizeNested(vm)
				continue
			}
			out[camelKey] = v
		case "responseSchema":
			out[camelKey] = ToNativeSchema(v)
		case "topK":
			out[camelKey] = clampTopK(v)
		default:
			out[camelKey] = v
		}
	}
	return out
}

// clampTopK 把 topK 限制到 ≤63。
func clampTopK(v any) any {
	switch n := v.(type) {
	case float64:
		if n > 63 {
			return 63
		}
		return int(n)
	case int:
		if n > 63 {
			return 63
		}
		return n
	default:
		return v
	}
}

// camelizeNested 深度遍历键 camelCase 化。
func camelizeNested(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range x {
			out[strutil.SnakeToCamel(k)] = camelizeNested(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = camelizeNested(item)
		}
		return out
	default:
		return v
	}
}

// buildSafetySettings 构建默认安全设置。
func buildSafetySettings(cfg config.ConfigProvider) []any {
	out := make([]any, 0, len(safetyCategories))
	for _, cat := range safetyCategories {
		threshold := "BLOCK_NONE"
		if cfg != nil {
			if t, ok := cfg.SafetySettings()[cat]; ok && t != "" {
				threshold = t
			}
		}
		out = append(out, map[string]any{"category": cat, "threshold": threshold})
	}
	return out
}
