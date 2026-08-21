package vertex

import (
	"context"
	"log"
	"strconv"

	"github.com/bsfdsagfadg/vertex/internal/engine/transform"
)

// CountTokens 统计给定 contents 在指定模型下的 token 数（纯本地离线估算）。
//
// 不再发起 HTTP BatchGraphQL 请求，改为根据字符类型和媒体 Part 估算：
//   - ASCII 字符：0.25 token/字符（len(ascii) / 4）
//   - 非 ASCII 字符（中文/Emoji/日韩文等）：1.5 token/字符（nonAsciiCount * 3 / 2）
//   - 媒体/图片 Part（inlineData / fileData）：固定 1024 token
//
// 返回估算值，0 表示空 contents。
func (c *VertexAIClient) CountTokens(_ context.Context, model string, contents []transform.Content) int {
	result := estimateTokensTyped(contents)
	if c.cfg.DebugMode() {
		log.Printf("[Vertex] [CountTokens] 离线估算: 模型=%s, tokens=%d", model, result)
	}
	return result
}

// estimateTokensTyped 对强类型 contents 进行纯本地 token 估算（零 map 中转）。
func estimateTokensTyped(contents []transform.Content) int {
	total := 0
	for _, cm := range contents {
		total += estimatePartsTokensTyped(cm.Parts)
	}
	return total
}

// estimatePartsTokensTyped 统计单个 content 内 parts 的 token 估算。
func estimatePartsTokensTyped(parts []transform.Part) int {
	total := 0
	for _, p := range parts {
		if p.Text != "" {
			total += estimateTextTokens(p.Text)
		}
		if p.InlineData != nil {
			total += 1024
		} else if p.FileData != nil {
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
