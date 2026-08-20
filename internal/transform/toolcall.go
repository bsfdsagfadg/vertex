package transform

import (
	"strings"
)


// extractOAIFunctionTool 从 tools 项提取 function 声明。
func extractOAIFunctionTool(tool any) map[string]any {
	m, ok := tool.(map[string]any)
	if !ok {
		return nil
	}
	if m["type"] == "function" {
		if fn, ok := m["function"].(map[string]any); ok {
			if truthyStr(fn["name"]) {
				return fn
			}
			return nil
		}
	}
	if fnStr, ok := m["function"].(string); ok && fnStr != "" {
		copied := copyMap(m)
		delete(copied, "function")
		copied["name"] = fnStr
		if truthyStr(copied["name"]) {
			return copied
		}
		return nil
	}
	if m["type"] == "function" && truthyStr(m["name"]) {
		return m
	}
	if truthyStr(m["name"]) {
		_, hasParams := m["parameters"]
		_, hasDesc := m["description"]
		if hasParams || hasDesc {
			return m
		}
	}
	return nil
}



func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// parseDataURI 解析 "data:mime;base64,DATA"，返回 (mime, data)。
func parseDataURI(uri string) (string, string) {
	idx := strings.Index(uri, ",")
	if idx < 0 {
		return "", ""
	}
	header := uri[:idx]
	data := uri[idx+1:]
	colon := strings.Index(header, ":")
	if colon < 0 {
		return "", ""
	}
	mime := header[colon+1:]
	if semi := strings.Index(mime, ";"); semi >= 0 {
		mime = mime[:semi]
	}
	return mime, data
}

// guessMIMEFromURL 按扩展名猜图片 mime（默认 image/png）。
func guessMIMEFromURL(url string) string {
	lower := trimLowerSuffix(url)
	switch {
	case strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(lower, ".webp"):
		return "image/webp"
	case strings.HasSuffix(lower, ".gif"):
		return "image/gif"
	default:
		return "image/png"
	}
}

// guessMIMEFromURI 按扩展名猜 mime，覆盖图/视频/音频/pdf/txt（默认 image/png）。
func guessMIMEFromURI(uri string) string {
	lower := trimLowerSuffix(uri)
	switch {
	case strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(lower, ".webp"):
		return "image/webp"
	case strings.HasSuffix(lower, ".gif"):
		return "image/gif"
	case strings.HasSuffix(lower, ".png"):
		return "image/png"
	case strings.HasSuffix(lower, ".mp4"):
		return "video/mp4"
	case strings.HasSuffix(lower, ".mov"):
		return "video/quicktime"
	case strings.HasSuffix(lower, ".webm"):
		return "video/webm"
	case strings.HasSuffix(lower, ".mp3"):
		return "audio/mpeg"
	case strings.HasSuffix(lower, ".wav"):
		return "audio/wav"
	case strings.HasSuffix(lower, ".ogg"):
		return "audio/ogg"
	case strings.HasSuffix(lower, ".pdf"):
		return "application/pdf"
	case strings.HasSuffix(lower, ".txt"):
		return "text/plain"
	default:
		return "image/png"
	}
}
