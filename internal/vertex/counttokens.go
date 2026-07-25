package vertex

import (
	"context"
	"encoding/json"
	"log"
	"strconv"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

// CountTokens 统计给定 contents 在指定模型下的 token 数（纯本地离线估算）。
//
// 不再发起 HTTP BatchGraphQL 请求，改为根据字符类型和媒体 Part 估算：
//   - ASCII 字符：0.25 token/字符（len(ascii) / 4）
//   - 非 ASCII 字符（中文/Emoji/日韩文等）：1.5 token/字符（nonAsciiCount * 3 / 2）
//   - 媒体/图片 Part（inlineData / fileData）：固定 1024 token
//
// 返回估算值，0 表示空 contents 或解析失败。
func (c *VertexAIClient) CountTokens(ctx context.Context, model string, contents []any) int {
	result := estimateTokens(contents)
	if c.cfg.DebugMode() {
		log.Printf("[Vertex] [CountTokens] 离线估算: 模型=%s, tokens=%d", model, result)
	}
	return result
}

// estimateTokens 对 contents 进行纯本地 token 估算。
func estimateTokens(contents []any) int {
	total := 0
	for _, c := range contents {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		parts, _ := cm["parts"].([]any)
		total += estimatePartsTokens(parts)
	}
	return total
}

func estimatePartsTokens(parts []any) int {
	total := 0
	for _, p := range parts {
		pm, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if text, ok := pm["text"].(string); ok {
			total += estimateTextTokens(text)
		}
		if _, ok := pm["inlineData"]; ok {
			total += 1024
		} else if _, ok := pm["fileData"]; ok {
			total += 1024
		}
	}
	return total
}

func estimateTextTokens(text string) int {
	ascii := 0
	nonAscii := 0
	for _, r := range text {
		if r <= 127 {
			ascii++
		} else {
			nonAscii++
		}
	}
	// ASCII: 0.25 token/char, 即每 4 字符 1 token
	// 非 ASCII: 1.5 token/char, 即每字符 1.5 token (nonAscii + nonAscii/2)
	return ascii/4 + nonAscii + nonAscii/2
}

// EstimateStringTokens 对外暴露的字符串估算函数（用于调试/测试）。
func EstimateStringTokens(text string) int {
	return estimateTextTokens(text)
}

// ---- 以下为向下兼容保留的旧 CountTokens HTTP 逻辑（不再被调用） ----

// buildCountTokensPayload 构建 CountTokens 的 batchGraphql 请求体。
func buildCountTokensPayload(model string, contents []any, recaptchaToken string, cfg config.ConfigProvider) map[string]any {
	if contents == nil {
		contents = []any{}
	}
	querySig := cfg.CountTokensQuerySignature()
	if querySig == "" {
		querySig = "2/mENOSldfC+HZM+tGhVuJLrl8M6gEyK3HRjUKuA5AM58="
	}
	return map[string]any{
		"requestContext": map[string]any{
			"clientVersion": "boq_cloud-boq-clientweb-vertexaistudio_20260402.09_p0",
			"pagePath":      "/vertex-ai/studio/multimodal",
			"jurisdiction":  "global",
			"localizationData": map[string]any{
				"locale":   "zh_CN",
				"timezone": "Asia/Shanghai",
			},
		},
		"querySignature": querySig,
		"operationName":  "CountTokens",
		"variables": map[string]any{
			"contents":       contents,
			"endpoint":       "",
			"model":          model,
			"region":         "global",
			"recaptchaToken": recaptchaToken,
		},
	}
}

// countTokensHeaders 构造 CountTokens 上游请求头。
func countTokensHeaders() transport.Header {
	h := transport.XHRHeaders(
		"application/json", "*/*",
		"https://console.cloud.google.com",
		"https://console.cloud.google.com/vertex-ai/studio/multimodal",
		"cross-site",
	)
	h["x-goog-authuser"] = []string{"0"}
	return h
}

// parseCountTokensResponse 从 CountTokens 响应里抠 totalTokens（旧 HTTP 响应解析，保留用于测试）。
func parseCountTokensResponse(raw string) int {
	var parsed any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return 0
	}
	var items []any
	switch v := parsed.(type) {
	case []any:
		items = v
	case map[string]any:
		items = []any{v}
	default:
		return 0
	}

	for _, entryRaw := range items {
		entry, ok := entryRaw.(map[string]any)
		if !ok {
			continue
		}
		if _, hasErr := entry["errors"]; hasErr {
			continue
		}
		results, _ := entry["results"].([]any)
		for _, rRaw := range results {
			result, ok := rRaw.(map[string]any)
			if !ok {
				continue
			}
			if _, hasErr := result["errors"]; hasErr {
				continue
			}
			data, ok := result["data"].(map[string]any)
			if !ok {
				continue
			}
			var countData map[string]any
			if ui, ok := data["ui"].(map[string]any); ok {
				if cd, ok := ui["countTokensV2"].(map[string]any); ok {
					countData = cd
				}
			}
			if countData == nil {
				if cd, ok := data["countTokensV2"].(map[string]any); ok {
					countData = cd
				} else if cd, ok := data["countTokens"].(map[string]any); ok {
					countData = cd
				}
			}
			if countData != nil {
				if tt, ok := countData["totalTokens"]; ok {
					return coerceTokenCount(tt)
				}
			}
		}
	}
	return 0
}

// coerceTokenCount 把 totalTokens（数字或数字字符串）转 int。
func coerceTokenCount(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		if x, err := strconv.Atoi(n); err == nil {
			return x
		}
	}
	return 0
}


