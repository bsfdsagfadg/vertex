package transform

import (
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/strutil"
)

// ensureGenCfg 返回 geminiPayload["generationConfig"]（不存在则创建）。
func ensureGenCfg(geminiPayload map[string]any) map[string]any {
	gc, ok := geminiPayload["generationConfig"].(map[string]any)
	if !ok {
		gc = map[string]any{}
		geminiPayload["generationConfig"] = gc
	}
	return gc
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
			out[camelKey] = toNativeSchema(v)
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
		if t, ok := cfg.SafetySettings()[cat]; ok && t != "" {
			threshold = t
		}
		out = append(out, map[string]any{"category": cat, "threshold": threshold})
	}
	return out
}
