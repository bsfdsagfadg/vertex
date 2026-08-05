package transform

import "strings"

// inlineDataPart 构造 inlineData part，data 经 NormalizeBase64 规范化。
func inlineDataPart(mime, b64 string) map[string]any {
	return map[string]any{"inlineData": map[string]any{
		"mimeType": mime, "data": NormalizeBase64(b64),
	}}
}

// imageURLString 从 image_url 字段取出 url 字符串（兼容 {url} 与字符串两种形态）。
func imageURLString(v any) string {
	if m, ok := v.(map[string]any); ok {
		s, _ := m["url"].(string)
		return s
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// holderURLString 从 video_url/input_video 的 holder 取出 url（兼容 {url} 与字符串）。
func holderURLString(holder any) string {
	switch h := holder.(type) {
	case string:
		return h
	case map[string]any:
		s, _ := h["url"].(string)
		return s
	default:
		return ""
	}
}

// parseInputAudio 从 input_audio holder 解析 (mime, base64)。
func parseInputAudio(holder any) (string, string) {
	switch h := holder.(type) {
	case string:
		if strings.HasPrefix(h, "data:") {
			return parseDataURI(h)
		}
	case map[string]any:
		if rawData, ok := h["data"].(string); ok && rawData != "" {
			if strings.HasPrefix(rawData, "data:") {
				return parseDataURI(rawData)
			}
			fmtStr, _ := h["format"].(string)
			return audioFormatMIME[strings.ToLower(fmtStr)], rawData
		}
		if url, ok := h["url"].(string); ok && strings.HasPrefix(url, "data:") {
			return parseDataURI(url)
		}
	}
	return "", ""
}

// hasRemotePrefix 判断 URL 是否为远程引用（http/https/gs）。
func hasRemotePrefix(url string) bool {
	return strings.HasPrefix(url, "http://") ||
		strings.HasPrefix(url, "https://") ||
		strings.HasPrefix(url, "gs://")
}

// normalizeParts 把 parts 归一为 part 列表。
func normalizeParts(parts any) []any {
	switch p := parts.(type) {
	case nil:
		return []any{}
	case string:
		return []any{map[string]any{"text": p}}
	case map[string]any:
		return []any{normalizePart(p)}
	case []any:
		out := []any{}
		for _, item := range p {
			if s, ok := item.(string); ok {
				out = append(out, map[string]any{"text": s})
			} else if m, ok := item.(map[string]any); ok {
				if np := normalizePart(m); len(np) > 0 {
					out = append(out, np)
				}
			}
		}
		return out
	default:
		return []any{map[string]any{"text": toString(parts)}}
	}
}

// normalizePart 把 OpenAI 风格 part 归一为 Gemini part。
func normalizePart(part map[string]any) map[string]any {
	pt, _ := part["type"].(string)
	switch pt {
	case "text", "input_text":
		return map[string]any{"text": toString(part["text"])}

	case "image_url", "input_image":
		var url string
		switch u := firstNonEmpty(part["image_url"], part["input_image"]).(type) {
		case map[string]any:
			url = toString(u["url"])
		case string:
			url = u
		}
		if strings.HasPrefix(url, "data:") {
			if mime, data := parseDataURI(url); mime != "" && data != "" {
				return map[string]any{"inlineData": map[string]any{"mimeType": mime, "data": data}}
			}
		}
		if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "gs://") {
			return map[string]any{"fileData": map[string]any{"mimeType": guessMIMEFromURI(url), "fileUri": url}}
		}

	case "media", "file", "file_data":
		fileURI := toString(firstNonEmpty(part["fileUri"], part["file_uri"], part["uri"], part["url"]))
		if fileURI != "" {
			mime := firstTruthyString(part["mimeType"], part["mime_type"])
			if mime == "" {
				mime = guessMIMEFromURI(fileURI)
			}
			return map[string]any{"fileData": map[string]any{"mimeType": mime, "fileUri": fileURI}}
		}

	case "inline_data", "inlineData":
		inline := part
		if m, ok := part["inlineData"].(map[string]any); ok {
			inline = m
		} else if m, ok := part["inline_data"].(map[string]any); ok {
			inline = m
		}
		data := toString(inline["data"])
		mime := firstTruthyString(inline["mimeType"], inline["mime_type"], part["mimeType"], part["mime_type"])
		if data != "" && mime != "" {
			return map[string]any{"inlineData": map[string]any{"mimeType": mime, "data": data}}
		}
	}

	out := map[string]any{}
	for k, v := range part {
		if k == "type" {
			continue
		}
		out[SnakeToCamel(k)] = v
	}
	return out
}

// handleInlineDataCase 递归把键 camelCase 化。
func handleInlineDataCase(contents any) any {
	switch c := contents.(type) {
	case []any:
		out := make([]any, len(c))
		for i, item := range c {
			out[i] = handleInlineDataCase(item)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, v := range c {
			camelK := SnakeToCamel(k)
			switch camelK {
			case "inlineData":
				if vm, ok := v.(map[string]any); ok {
					nid := map[string]any{}
					for ik, iv := range vm {
						nid[SnakeToCamel(ik)] = iv
					}
					out["inlineData"] = nid
					continue
				}
				out[camelK] = handleInlineDataCase(v)
			case "functionCall":
				if vm, ok := v.(map[string]any); ok {
					out["functionCall"] = camelizeFunctionRef(vm, "args")
					continue
				}
				out[camelK] = handleInlineDataCase(v)
			case "functionResponse":
				if vm, ok := v.(map[string]any); ok {
					out["functionResponse"] = camelizeFunctionRef(vm, "response")
					continue
				}
				out[camelK] = handleInlineDataCase(v)
			default:
				out[camelK] = handleInlineDataCase(v)
			}
		}
		return out
	default:
		return contents
	}
}

// camelizeFunctionRef 处理 functionCall/functionResponse 分支。
func camelizeFunctionRef(v map[string]any, payloadKey string) map[string]any {
	out := map[string]any{}
	if fid := firstTruthyString(v["id"], v["tool_call_id"], v["toolCallId"]); fid != "" {
		out["id"] = fid
	}
	for ik, iv := range v {
		cik := SnakeToCamel(ik)
		switch cik {
		case payloadKey:
			out[cik] = iv
		case "id", "toolCallId":
		default:
			out[cik] = handleInlineDataCase(iv)
		}
	}
	return out
}
