package transform

import (
	"encoding/json"
	"strings"
)

// oaiToolCall 是从 OpenAI tool_call 提取出的归一结果。
type oaiToolCall struct { //nolint:govet
	id   string
	name string
	args any
}

// extractOAIToolCall 健壮解析 OpenAI tool_call。
func extractOAIToolCall(tc any) *oaiToolCall {
	m, ok := tc.(map[string]any)
	if !ok {
		return nil
	}
	id := firstTruthyString(m["id"], m["tool_call_id"], m["call_id"])

	var name string
	var args any
	if fn, ok := m["function"].(map[string]any); ok {
		name = firstTruthyString(fn["name"], m["name"])
		args = firstPresent(fn, "arguments", m, "arguments", "args")
	} else {
		name = firstTruthyString(m["name"], m["function_name"])
		args = firstPresentIn(m, "arguments", "args")
	}
	if name == "" {
		return nil
	}
	return &oaiToolCall{id: id, name: name, args: coerceFunctionArgs(args)}
}

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

// coerceFunctionArgs 把 tool_call.arguments 规范成 dict/对象。
func coerceFunctionArgs(args any) any {
	if s, ok := args.(string); ok {
		var parsed any
		if err := json.Unmarshal([]byte(s), &parsed); err == nil {
			return parsed
		}
		return map[string]any{"raw": s}
	}
	if args == nil {
		return map[string]any{}
	}
	return args
}

// firstPresent 在两个 map 里依次查 keys，返回第一个存在的值。
func firstPresent(m1 map[string]any, k1 string, m2 map[string]any, k2, k3 string) any {
	if v, ok := m1[k1]; ok {
		return v
	}
	if v, ok := m2[k2]; ok {
		return v
	}
	if v, ok := m2[k3]; ok {
		return v
	}
	return map[string]any{}
}

// firstPresentIn 在一个 map 里依次查 keys，返回第一个存在的值。
func firstPresentIn(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	return map[string]any{}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
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
