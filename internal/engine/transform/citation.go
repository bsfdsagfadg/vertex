package transform

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// citeRegex 匹配文本中的 [cite: xxx] 角标标记。
var citeRegex = regexp.MustCompile(`\[cite:\s*([^\]]+)\]`)

// GroundingChunk 表示 groundingMetadata 中的单个 web 网页源引用。
type GroundingChunk struct {
	Web struct {
		URI   string `json:"uri"`
		Title string `json:"title"`
	} `json:"web"`
}

// ConvertCitationsToMarkdown 解析 text 中的 [cite: id] 或 [cite: N]，
// 配合 candidate / response 中的 GroundingMetadata，改写为标准的 Markdown 超链接 [1](URL "Title") 或 [Title](URL)。
func ConvertCitationsToMarkdown(text string, groundingMetadata any) string {
	if text == "" || groundingMetadata == nil {
		return text
	}

	gmMap, ok := groundingMetadata.(map[string]any)
	if !ok {
		return text
	}

	// 提取 groundingChunks
	chunksRaw, ok := gmMap["groundingChunks"].([]any)
	if !ok || len(chunksRaw) == 0 {
		return text
	}

	type webRef struct {
		URI   string
		Title string
	}

	var webRefs []webRef
	for _, item := range chunksRaw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		web, ok := m["web"].(map[string]any)
		if !ok {
			continue
		}
		uri, _ := web["uri"].(string)
		title, _ := web["title"].(string)
		if uri != "" {
			webRefs = append(webRefs, webRef{URI: uri, Title: title})
		}
	}

	if len(webRefs) == 0 {
		return text
	}

	// 尝试解析并替换 [cite: xxx]
	replaced := citeRegex.ReplaceAllStringFunc(text, func(match string) string {
		sub := citeRegex.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		idStr := strings.TrimSpace(sub[1])

		// 情形 1：[cite: 1] 或 [cite: 0f151430-1] - 尝试取后缀数字或直接解析为 1-based 索引
		var index int = -1
		if idx, err := strconv.Atoi(idStr); err == nil {
			index = idx - 1
		} else if pos := strings.LastIndex(idStr, "-"); pos != -1 {
			if idx, err := strconv.Atoi(idStr[pos+1:]); err == nil {
				index = idx - 1
			}
		}

		if index >= 0 && index < len(webRefs) {
			ref := webRefs[index]
			label := fmt.Sprintf("[%d]", index+1)
			if ref.Title != "" {
				return fmt.Sprintf("[%s](%s %q)", label, ref.URI, ref.Title)
			}
			return fmt.Sprintf("[%s](%s)", label, ref.URI)
		}

		// 兜底：如果索引超出或无法数字解析，默认用第 1 个 reference
		ref := webRefs[0]
		if ref.Title != "" {
			return fmt.Sprintf("[%s](%s %q)", idStr, ref.URI, ref.Title)
		}
		return fmt.Sprintf("[%s](%s)", idStr, ref.URI)
	})

	return replaced
}
