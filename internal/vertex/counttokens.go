package vertex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/jsonx"
	"github.com/bsfdsagfadg/vertex/internal/strutil"
	"github.com/bsfdsagfadg/vertex/internal/upstream/anonymousgraph"
)

// CountTokens 统计给定 contents 在指定模型下的 token 数。
func (c *VertexAIClient) CountTokens(ctx context.Context, model string, contents []any) (int, error) {
	ctx = c.prepareRequest(ctx)
	ctx = executionContext(ctx, "countTokens", model, map[string]any{"contents": contents})
	cfg := config.FromContext(ctx, c.cfg)
	if strings.TrimSpace(cfg.CountTokensQuerySignature()) == "" {
		return 0, NewUnavailableError("countTokens anonymous Graph operation is unavailable").WithPublicDetail("", "unsupported_endpoint", "countTokens is not supported by the configured anonymous upstream")
	}
	run := func(candidateCtx context.Context, requestNodeURI string) (int, error) {
		graph := c.anonymousGraph()
		session, err := graph.OpenSession(candidateCtx, cfg.RequestTimeout(), requestNodeURI, RequestIDFromContext(candidateCtx))
		if err != nil {
			return 0, NewUnavailableError("create countTokens route: "+err.Error(), err).WithScope(ScopeRoute)
		}
		defer session.Close()
		token, err := graph.RouteToken(candidateCtx, session.Route())
		if err != nil || token == "" {
			return 0, NewAuthenticationError("Could not fetch recaptcha token for countTokens", err)
		}
		payload := anonymousgraph.BuildCountTokensEnvelope(model, contents, token, cfg.CountTokensQuerySignature())
		body, err := jsonx.Marshal(payload)
		if err != nil {
			return 0, NewInternalError("marshal countTokens payload: "+err.Error(), err)
		}
		status, raw, err := graph.DoCountTokens(candidateCtx, session, cfg.VertexAPIKey(), bytes.NewReader(body))
		if err != nil {
			return 0, NewNetworkError(fmt.Errorf("countTokens upstream request: %w", err))
		}
		if status != http.StatusOK {
			return 0, classifyUpstreamHTTPError(status, string(raw))
		}
		count, found := parseCountTokensResponseOK(raw)
		if !found {
			return 0, NewUnavailableError("countTokens upstream response did not contain totalTokens")
		}
		return count, nil
	}
	return runRaceWithDependencies(ctx, cfg, run, c.race)
}

// parseCountTokensResponse 从 CountTokens 响应里抠 totalTokens。
//
// 上游可能是单对象或数组；逐层 results → data.ui.countTokensV2 / data.countTokensV2 / data.countTokens，
// 命中 totalTokens 即返回。任何错误/缺字段返回 0。
func parseCountTokensResponse(raw []byte) int {
	count, _ := parseCountTokensResponseOK(raw)
	return count
}

func parseCountTokensResponseOK(raw []byte) (int, bool) {
	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return 0, false
	}
	var items []any
	switch v := parsed.(type) {
	case []any:
		items = v
	case map[string]any:
		items = []any{v}
	default:
		return 0, false
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
					return strutil.ToInt(tt, 0), true
				}
			}
		}
	}
	return 0, false
}

// coerceTokenCount converts token count values to int via strutil.ToInt.
func coerceTokenCount(v any) int {
	return strutil.ToInt(v, 0)
}

// estimateTokens 递归或嵌套遍历 contents 计算估算的 token 总数。
func estimateTokens(contents []any) int {
	totalTokens := 0
	for _, contentAny := range contents {
		if contentAny == nil {
			continue
		}
		content, ok := contentAny.(map[string]any)
		if !ok {
			if s, ok := contentAny.(string); ok {
				totalTokens += estimateTextTokens(s)
			}
			continue
		}

		parts, ok := content["parts"].([]any)
		if !ok {
			continue
		}

		for _, partAny := range parts {
			if partAny == nil {
				continue
			}
			switch part := partAny.(type) {
			case string:
				totalTokens += estimateTextTokens(part)
			case map[string]any:
				totalTokens += estimatePartTokens(part)
			}
		}
	}
	return totalTokens
}

// estimatePartTokens 估算单个 part 的 token 数。
func estimatePartTokens(part map[string]any) int {
	if isImagePart(part) {
		return 1024
	}
	if textVal, ok := part["text"].(string); ok {
		return estimateTextTokens(textVal)
	}
	return 0
}

// isImagePart 判断一个 part 是否为图片。
func isImagePart(part map[string]any) bool {
	// 检查 image_url, input_image (OpenAI style)
	if _, ok := part["image_url"]; ok {
		return true
	}
	if _, ok := part["input_image"]; ok {
		return true
	}
	// 检查 inlineData / inline_data (Gemini style)
	for _, k := range []string{"inlineData", "inline_data"} {
		if m, ok := part[k].(map[string]any); ok {
			for _, mk := range []string{"mimeType", "mime_type"} {
				if mime, ok := m[mk].(string); ok && strings.Contains(strings.ToLower(mime), "image") {
					return true
				}
			}
		}
	}
	// 检查 fileData / file_data (Gemini style)
	for _, k := range []string{"fileData", "file_data"} {
		if m, ok := part[k].(map[string]any); ok {
			for _, mk := range []string{"mimeType", "mime_type"} {
				if mime, ok := m[mk].(string); ok && strings.Contains(strings.ToLower(mime), "image") {
					return true
				}
			}
		}
	}
	// 检查直接的 mimeType / mime_type
	for _, mk := range []string{"mimeType", "mime_type"} {
		if mime, ok := part[mk].(string); ok && strings.Contains(strings.ToLower(mime), "image") {
			return true
		}
	}
	return false
}

// estimateTextTokens 估算文本部分的 token 数。
// 这里的简单估算规则：
// - ASCII 字符（如英文、数字、符号、空格）算 0.25 个 token
// - 非 ASCII 字符（如中文汉字、日文、韩文、Emoji等）每个算 1.5 个 token
func estimateTextTokens(text string) int {
	var tokens float64
	for _, r := range text {
		if r < 128 {
			tokens += 0.25
		} else {
			tokens += 1.5
		}
	}
	return int(tokens + 0.99)
}
