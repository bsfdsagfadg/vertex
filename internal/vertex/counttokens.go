package vertex

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"strconv"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/jsonx"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

// CountTokens 统计给定 contents 在指定模型下的 token 数（Vertex CountTokens）。
//
// 走匿名 batchGraphql 的 CountTokens operation（独立 querySignature/operationName），
// 单 session + 实时 recaptcha。失败/解析不到时返回 0（吞错），语义为"尽力计数"——
// CountTokens 在上游不返数时不报错，给客户端一个 0。
//
// querySignature 从 config（count_tokens_query_signature）读，缺省值=内置硬编码值。
func (c *VertexAIClient) CountTokens(ctx context.Context, model string, contents []any) int {
	cfg := c.cfg

	reqID := RequestIDFromContext(ctx)
	sess, err := c.net.CreateSession(60, "", reqID)
	if err != nil {
		return 0
	}
	defer sess.Close()

	token, _ := c.pool.GetToken()
	if token == "" {
		return 0
	}

	// 去掉 models/ 前缀以匹配上游示例（去 models/ 前缀）。
	target := cfg.ResolveModelName(model)
	target = strings.TrimPrefix(target, "models/")

	payload := buildCountTokensPayload(target, contents, token, cfg)
	bodyBytes, err := jsonx.Marshal(payload)
	if err != nil {
		log.Printf("[Vertex] [CountTokens] 序列化请求体失败: %v", err)
		return 0
	}

	// CountTokens 的请求头与 chat 略有差异（referer 指向 multimodal、带 x-goog-authuser）。
	// 逐字节保持既定 headers。
	header := countTokensHeaders()

	status, raw, err := sess.DoAndRead(ctx, "POST", c.getBatchGraphqlURL(), header, bytes.NewReader(bodyBytes))
	if err != nil || status != 200 {
		log.Printf("[Vertex] [CountTokens] 上游请求失败, status=%d, err=%v, resp=%s", status, err, string(raw))
		return 0
	}
	return parseCountTokensResponse(string(raw))
}

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

// countTokensHeaders 构造 CountTokens 上游请求头（逐字节保持既定 headers）。
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

// parseCountTokensResponse 从 CountTokens 响应里抠 totalTokens。
//
// 上游可能是单对象或数组；逐层 results → data.ui.countTokensV2 / data.countTokensV2 / data.countTokens，
// 命中 totalTokens 即返回。任何错误/缺字段返回 0。
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
		// entry 级别 errors → 跳过。
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
