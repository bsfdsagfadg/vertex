package transform

import (
	"regexp"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

// assistantImageMarkdownRe 匹配 assistant 文本里嵌的 markdown data-URI 图片。
var assistantImageMarkdownRe = regexp.MustCompile(`!\[[^\]]*\]\((data:[^()\s;,]+;base64,[A-Za-z0-9+/=_\-]+)\)`)

// convertUserContent 把 OpenAI user message content 转为 Gemini parts。
func convertUserContent(content any) []any {
	if content == nil {
		return nil
	}
	if s, ok := content.(string); ok {
		return []any{map[string]any{"text": s}}
	}
	list, ok := content.([]any)
	if !ok {
		return nil
	}

	var parts []any
	for _, itemRaw := range list {
		if s, ok := itemRaw.(string); ok {
			parts = append(parts, map[string]any{"text": s})
			continue
		}
		item, ok := itemRaw.(map[string]any)
		if !ok {
			continue
		}
		t, _ := item["type"].(string)
		switch t {
		case "text":
			parts = append(parts, map[string]any{"text": item["text"]})

		case "image_url":
			url := imageURLString(item["image_url"])
			if strings.HasPrefix(url, "data:") {
				if mime, b64 := parseDataURI(url); mime != "" && b64 != "" {
					parts = append(parts, inlineDataPart(mime, b64))
				}
			} else if hasRemotePrefix(url) {
				parts = append(parts, map[string]any{"fileData": map[string]any{
					"mimeType": guessMIMEFromURL(url), "fileUri": url,
				}})
			}

		case "video_url", "input_video":
			url := holderURLString(item[t])
			if strings.HasPrefix(url, "data:") {
				if mime, b64 := parseDataURI(url); b64 != "" {
					if mime == "" || !strings.HasPrefix(mime, "video/") {
						mime = "video/mp4"
					}
					parts = append(parts, inlineDataPart(mime, b64))
				}
			}

		case "input_audio":
			mime, b64 := parseInputAudio(item["input_audio"])
			if b64 != "" {
				if mime == "" || !strings.HasPrefix(mime, "audio/") {
					mime = "audio/wav"
				}
				parts = append(parts, inlineDataPart(mime, b64))
			}
		}
	}
	return parts
}

// splitAssistantContent 把 assistant 文本切成 text / inlineData 混合 parts。
func splitAssistantContent(content any) []any {
	s, ok := content.(string)
	if !ok {
		return []any{map[string]any{"text": content}}
	}
	locs := assistantImageMarkdownRe.FindAllStringSubmatchIndex(s, -1)
	if len(locs) == 0 {
		return []any{map[string]any{"text": s}}
	}
	var parts []any
	last := 0
	for _, m := range locs {
		if pre := strings.TrimSpace(s[last:m[0]]); pre != "" {
			parts = append(parts, map[string]any{"text": pre})
		}
		if mime, b64 := parseDataURI(s[m[2]:m[3]]); mime != "" && b64 != "" {
			parts = append(parts, inlineDataPart(mime, b64))
		}
		last = m[1]
	}
	if post := strings.TrimSpace(s[last:]); post != "" {
		parts = append(parts, map[string]any{"text": post})
	}
	if len(parts) == 0 {
		parts = append(parts, map[string]any{"text": ""})
	}
	return parts
}

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

// matchTrailingFixModel 判断 model 是否命中尾部兼容清单（纯精确匹配，不做后缀扩展）。
func matchTrailingFixModel(model string, entries []string) bool {
	model = strings.TrimSpace(model)
	if model == "" || len(entries) == 0 {
		return false
	}
	for _, e := range entries {
		if model == strings.TrimSpace(e) {
			return true
		}
	}
	return false
}

// BuildVertexVariables 由 geminiPayload 构建发往上游的 variables。
func BuildVertexVariables(model string, geminiPayload map[string]any, cfg config.ConfigProvider) map[string]any {
	vars := map[string]any{}
	vars["model"] = parseModelName(model)

	for _, field := range supportedVarFields {
		if v, ok := geminiPayload[field]; ok {
			vars[field] = v
		} else {
			if v, ok := geminiPayload[CamelToSnake(field)]; ok {
				vars[field] = v
			}
		}
	}

	handleSystemInstruction(vars)

	if c, ok := vars["contents"]; ok {
		c = normalizeContents(c)
		c = handleInlineDataCase(c)
		c = normalizeContents(c)
		c = HandleBase64InContents(c)
		modelName, _ := vars["model"].(string)
		trailingFixActive := cfg.TrailingModelFixEnabled() &&
			matchTrailingFixModel(modelName, cfg.TrailingFixModels())

		if trailingFixActive {
			c = normalizeFunctionResponseRoles(c)
		}

		c = mergeContiguousRoles(c)
		c = filterEmptyContents(c)

		if trailingFixActive {
			if list, ok := c.([]any); ok && len(list) > 0 {
				if last, ok := list[len(list)-1].(map[string]any); ok {
					if endsWithModelTurn(last) {
						list = append(list, map[string]any{
							"role":  "user",
							"parts": []any{map[string]any{"text": "继续"}},
						})
						c = list
					}
				}
			}
		}

		c = EncodeThoughtSignature(c, 0)
		vars["contents"] = c
	}

	if rawTools, ok := vars["tools"]; ok {
		normalized := normalizeToolsFormat(rawTools)
		if len(normalized) > 0 {
			vars["tools"] = normalized
		} else {
			delete(vars, "tools")
			delete(vars, "toolConfig")
		}
	}
	if tc, ok := vars["toolConfig"]; ok {
		vars["toolConfig"] = convertToolsFormat(tc)
	}

	if genCfg := buildGenerationConfig(geminiPayload); len(genCfg) > 0 {
		vars["generationConfig"] = genCfg
	}

	if _, ok := vars["safetySettings"]; !ok {
		if _, ok2 := geminiPayload["safety_settings"]; !ok2 {
			vars["safetySettings"] = buildSafetySettings(cfg)
		}
	}

	return vars
}

// handleSystemInstruction 把 systemInstruction 在无 user content 时降级为首条 user message。
func handleSystemInstruction(vars map[string]any) {
	siRaw, ok := vars["systemInstruction"]
	if !ok || !isTruthy(siRaw) {
		return
	}
	contents, _ := vars["contents"].([]any)
	for _, c := range contents {
		if cm, ok := c.(map[string]any); ok {
			if r, _ := cm["role"].(string); r == "user" {
				return
			}
		}
	}
	text := extractTextFromInstruction(siRaw)
	if text == "" {
		return
	}
	userMsg := map[string]any{"role": "user", "parts": []any{map[string]any{"text": text}}}
	vars["contents"] = append([]any{userMsg}, contents...)
	delete(vars, "systemInstruction")
}

func extractTextFromInstruction(instruction any) string {
	if s, ok := instruction.(string); ok {
		return s
	}
	if m, ok := instruction.(map[string]any); ok {
		if parts, ok := m["parts"].([]any); ok {
			var sb strings.Builder
			for _, p := range parts {
				if pm, ok := p.(map[string]any); ok {
					if t, ok := pm["text"]; ok {
						sb.WriteString(toString(t))
					}
				}
			}
			return sb.String()
		}
	}
	return ""
}

// normalizeContents 把 contents 归一为 Gemini content 列表。
func normalizeContents(contents any) any {
	switch c := contents.(type) {
	case nil:
		return []any{}
	case string:
		return []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": c}}}}
	case map[string]any:
		return []any{normalizeContent(c)}
	case []any:
		normalized := []any{}
		var pendingText []any
		for _, item := range c {
			if s, ok := item.(string); ok {
				pendingText = append(pendingText, map[string]any{"text": s})
			} else if m, ok := item.(map[string]any); ok {
				if len(pendingText) > 0 {
					normalized = append(normalized, map[string]any{"role": "user", "parts": pendingText})
					pendingText = nil
				}
				normalized = append(normalized, normalizeContent(m))
			}
		}
		if len(pendingText) > 0 {
			normalized = append(normalized, map[string]any{"role": "user", "parts": pendingText})
		}
		return normalized
	default:
		return contents
	}
}

// normalizeContent 归一单个 content（role 映射 + content→parts + str→text）。
func normalizeContent(content map[string]any) map[string]any {
	n := copyMap(content)
	_, hasContent := n["content"]
	_, hasParts := n["parts"]
	switch {
	case hasContent && !hasParts:
		n["parts"] = normalizeParts(n["content"])
		delete(n, "content")
	case hasParts:
		n["parts"] = normalizeParts(n["parts"])
	default:
		if t, hasText := n["text"]; hasText {
			n["parts"] = []any{map[string]any{"text": toString(t)}}
			delete(n, "text")
		} else {
			n["parts"] = []any{}
		}
	}
	switch role, _ := n["role"].(string); role {
	case "assistant":
		n["role"] = "model"
	case "tool":
		n["role"] = "function"
	case "":
		n["role"] = "user"
	}
	return n
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

// filterEmptyContents 对每个 content 的 parts 逐个清洗。
func filterEmptyContents(contents any) any {
	list, ok := contents.([]any)
	if !ok {
		return contents
	}

	callIDMap := map[string]string{}
	var lastModelFunctionCalls []string
	responseIndex := 0

	filtered := []any{}
	for _, c := range list {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		role, _ := cm["role"].(string)
		parts := asAnySlice(cm["parts"])

		if role == "model" {
			lastModelFunctionCalls = nil
			responseIndex = 0
			for _, p := range parts {
				if pm, ok := p.(map[string]any); ok {
					if fc, ok := pm["functionCall"].(map[string]any); ok {
						if name, _ := fc["name"].(string); strings.TrimSpace(name) != "" {
							lastModelFunctionCalls = append(lastModelFunctionCalls, name)
							if fid, _ := fc["id"].(string); fid != "" {
								callIDMap[fid] = name
							}
						}
					}
				}
			}
		}

		var cleanedParts []any
		for _, p := range parts {
			pm, ok := p.(map[string]any)
			if !ok {
				continue
			}
			_, isFuncResponse := pm["functionResponse"]
			idx := -1
			if isFuncResponse {
				idx = responseIndex
				responseIndex++
			}
			if cleaned, ok := cleanPartWithID(pm, lastModelFunctionCalls, idx, callIDMap); ok {
				cleanedParts = append(cleanedParts, cleaned)
			}
		}
		if len(cleanedParts) > 0 {
			nc := copyMap(cm)
			nc["parts"] = cleanedParts
			filtered = append(filtered, nc)
		}
	}
	return filtered
}

// endsWithModelTurn 判断 content 是否会被上游视为 "以 model turn 结尾"。
// Google 服务端按 part 类型判定：非 user 角色（system 除外，system 结尾不属模型回合），
// 或 user 消息中含 functionResponse/functionCall part，均被视为模型回合未闭合。
func endsWithModelTurn(content map[string]any) bool {
	role, _ := content["role"].(string)
	if !strings.EqualFold(role, "user") && !strings.EqualFold(role, "system") {
		return true
	}
	parts, _ := content["parts"].([]any)
	for _, p := range parts {
		pm, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if pm["functionResponse"] != nil || pm["functionCall"] != nil {
			return true
		}
	}
	return false
}

// normalizeFunctionResponseRoles 将 contents 中纯 functionResponse 的 model role 修正为 function。
// 必须在 mergeContiguousRoles 之前调用，否则相邻同 role 合并后无法单独特定 functionResponse。
func normalizeFunctionResponseRoles(contents any) any {
	list, ok := contents.([]any)
	if !ok || len(list) == 0 {
		return contents
	}
	result := make([]any, len(list))
	for i, c := range list {
		cm, ok := c.(map[string]any)
		if !ok {
			result[i] = c
			continue
		}
		role, _ := cm["role"].(string)
		if role != "model" {
			result[i] = c
			continue
		}
		parts, _ := cm["parts"].([]any)
		if len(parts) == 0 {
			result[i] = c
			continue
		}
		allFuncResp := true
		for _, p := range parts {
			pm, ok := p.(map[string]any)
			if !ok || pm["functionResponse"] == nil {
				allFuncResp = false
				break
			}
		}
		if !allFuncResp {
			result[i] = c
			continue
		}
		nc := copyMap(cm)
		nc["role"] = "function"
		result[i] = nc
	}
	return result
}

// mergeContiguousRoles 合并相邻同 role 的 content。
func mergeContiguousRoles(contents any) any {
	list, ok := contents.([]any)
	if !ok || len(list) == 0 {
		return contents
	}

	merged := []any{list[0]}
	for _, c := range list[1:] {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		prev, ok := merged[len(merged)-1].(map[string]any)
		if !ok {
			merged = append(merged, cm)
			continue
		}
		role, _ := cm["role"].(string)
		prevRole, _ := prev["role"].(string)
		if role == prevRole {
			prevCopy := copyMap(prev)
			prevParts := asAnySlice(prevCopy["parts"])
			curParts := asAnySlice(cm["parts"])
			prevCopy["parts"] = append(prevParts, curParts...)
			merged[len(merged)-1] = prevCopy
		} else {
			merged = append(merged, cm)
		}
	}
	return merged
}
