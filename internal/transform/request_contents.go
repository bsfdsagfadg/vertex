package transform

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/domain"
	"github.com/bsfdsagfadg/vertex/internal/strutil"
)

// assistantImageMarkdownRe 匹配 assistant 文本里嵌的 markdown data-URI 图片。
var assistantImageMarkdownRe = regexp.MustCompile(`!\[[^\]]*\]\((data:[^()\s;,]+;base64,[A-Za-z0-9+/=_\-]+)\)`)

// convertUserContentTyped 把 OpenAI user message content 转为强类型 Gemini Part 切片。
func convertUserContentTyped(content any) []domain.Part {
	if content == nil {
		return nil
	}

	if s, ok := content.(string); ok {
		if s == "" {
			return nil
		}
		return []domain.Part{{Text: s}}
	}

	var parts []domain.Part

	switch list := content.(type) {
	case []domain.MessageContentPart:
		for _, item := range list {
			switch item.Type {
			case "text", "input_text":
				if item.Text != "" {
					parts = append(parts, domain.Part{Text: item.Text})
				}
			case "image_url":
				if item.ImageURL != nil {
					url := item.ImageURL.URL
					if strings.HasPrefix(url, "data:") {
						if mime, b64 := parseDataURI(url); mime != "" && b64 != "" {
							parts = append(parts, domain.Part{
								InlineData: &domain.Blob{
									MimeType: mime,
									Data:     NormalizeBase64(b64),
								},
							})
						}
					} else if hasRemotePrefix(url) {
						parts = append(parts, domain.Part{
							FileData: &domain.FileData{
								MimeType: guessMIMEFromURL(url),
								FileURI:  url,
							},
						})
					}
				}
			case "input_audio":
				if item.InputAudio != nil && item.InputAudio.Data != "" {
					mime := audioFormatMIME[strings.ToLower(item.InputAudio.Format)]
					if mime == "" {
						mime = "audio/wav"
					}
					parts = append(parts, domain.Part{
						InlineData: &domain.Blob{
							MimeType: mime,
							Data:     NormalizeBase64(item.InputAudio.Data),
						},
					})
				}
			}
		}

	case []any:
		for _, itemRaw := range list {
			if s, ok := itemRaw.(string); ok {
				if s != "" {
					parts = append(parts, domain.Part{Text: s})
				}
				continue
			}
			item, ok := itemRaw.(map[string]any)
			if !ok {
				continue
			}
			t, _ := item["type"].(string)
			switch t {
			case "text", "input_text":
				txt := toString(item["text"])
				if txt != "" {
					parts = append(parts, domain.Part{Text: txt})
				}

			case "image_url", "input_image":
				url := imageURLString(firstNonEmpty(item["image_url"], item["input_image"]))
				if strings.HasPrefix(url, "data:") {
					if mime, b64 := parseDataURI(url); mime != "" && b64 != "" {
						parts = append(parts, domain.Part{
							InlineData: &domain.Blob{
								MimeType: mime,
								Data:     NormalizeBase64(b64),
							},
						})
					}
				} else if hasRemotePrefix(url) {
					parts = append(parts, domain.Part{
						FileData: &domain.FileData{
							MimeType: guessMIMEFromURL(url),
							FileURI:  url,
						},
					})
				}

			case "video_url", "input_video":
				url := holderURLString(item[t])
				if strings.HasPrefix(url, "data:") {
					if mime, b64 := parseDataURI(url); b64 != "" {
						if mime == "" || !strings.HasPrefix(mime, "video/") {
							mime = "video/mp4"
						}
						parts = append(parts, domain.Part{
							InlineData: &domain.Blob{
								MimeType: mime,
								Data:     NormalizeBase64(b64),
							},
						})
					}
				}

			case "input_audio":
				mime, b64 := parseInputAudio(item["input_audio"])
				if b64 != "" {
					if mime == "" || !strings.HasPrefix(mime, "audio/") {
						mime = "audio/wav"
					}
					parts = append(parts, domain.Part{
						InlineData: &domain.Blob{
							MimeType: mime,
							Data:     NormalizeBase64(b64),
						},
					})
				}
			}
		}
	}

	return parts
}

// convertUserContent 兼容旧签名（返回 []any）。
func convertUserContent(content any) []any {
	parts := convertUserContentTyped(content)
	if len(parts) == 0 {
		return nil
	}
	out := make([]any, len(parts))
	for i, p := range parts {
		m := make(map[string]any)
		if p.Text != "" {
			m["text"] = p.Text
		}
		if p.InlineData != nil {
			m["inlineData"] = map[string]any{
				"mimeType": p.InlineData.MimeType,
				"data":     p.InlineData.Data,
			}
		}
		if p.FileData != nil {
			m["fileData"] = map[string]any{
				"mimeType": p.FileData.MimeType,
				"fileUri":  p.FileData.FileURI,
			}
		}
		out[i] = m
	}
	return out
}

// splitAssistantContentTyped 把 assistant 文本切成 text / inlineData 混合 parts。
func splitAssistantContentTyped(content any) []domain.Part {
	s, ok := content.(string)
	if !ok {
		return []domain.Part{{Text: toString(content)}}
	}
	locs := assistantImageMarkdownRe.FindAllStringSubmatchIndex(s, -1)
	if len(locs) == 0 {
		return []domain.Part{{Text: s}}
	}
	var parts []domain.Part
	last := 0
	for _, m := range locs {
		if pre := strings.TrimSpace(s[last:m[0]]); pre != "" {
			parts = append(parts, domain.Part{Text: pre})
		}
		if mime, b64 := parseDataURI(s[m[2]:m[3]]); mime != "" && b64 != "" {
			parts = append(parts, domain.Part{
				InlineData: &domain.Blob{
					MimeType: mime,
					Data:     NormalizeBase64(b64),
				},
			})
		}
		last = m[1]
	}
	if post := strings.TrimSpace(s[last:]); post != "" {
		parts = append(parts, domain.Part{Text: post})
	}
	if len(parts) == 0 {
		parts = append(parts, domain.Part{Text: ""})
	}
	return parts
}

// splitAssistantContent 兼容旧签名（返回 []any）。
func splitAssistantContent(content any) []any {
	parts := splitAssistantContentTyped(content)
	out := make([]any, len(parts))
	for i, p := range parts {
		if p.InlineData != nil {
			out[i] = map[string]any{
				"inlineData": map[string]any{
					"mimeType": p.InlineData.MimeType,
					"data":     p.InlineData.Data,
				},
			}
		} else {
			out[i] = map[string]any{"text": p.Text}
		}
	}
	return out
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

// BuildVertexVariables 由 geminiPayload 构建发往上游 Vertex AI 的 GraphQL variables 字典。
func BuildVertexVariables(model string, geminiPayload any, cfg config.ConfigProvider) map[string]any {
	var payloadMap map[string]any

	switch p := geminiPayload.(type) {
	case map[string]any:
		payloadMap = p
	case *domain.GenerateContentRequest:
		m, err := GenerateContentRequestToMap(p)
		if err == nil {
			payloadMap = m
		} else {
			payloadMap = map[string]any{}
		}
	case domain.GenerateContentRequest:
		m, err := GenerateContentRequestToMap(&p)
		if err == nil {
			payloadMap = m
		} else {
			payloadMap = map[string]any{}
		}
	default:
		// 尝试通过 JSON 转换
		data, err := json.Marshal(geminiPayload)
		if err == nil {
			_ = json.Unmarshal(data, &payloadMap)
		}
		if payloadMap == nil {
			payloadMap = map[string]any{}
		}
	}

	vars := map[string]any{}
	vars["model"] = parseModelName(model)

	for _, field := range supportedVarFields {
		if v, ok := payloadMap[field]; ok {
			vars[field] = v
		} else {
			if v, ok := payloadMap[strutil.CamelToSnake(field)]; ok {
				vars[field] = v
			}
		}
	}

	handleSystemInstruction(vars)

	if c, ok := vars["contents"]; ok {
		trailingFix := trailingModelFixActive(model, cfg)
		c = normalizeContents(c)
		c = stripGeminiIDs(c)
		c = handleInlineDataCase(c)
		c = normalizeContents(c)
		c = HandleBase64InContents(c)
		c = filterEmptyContents(c)
		if trailingFix {
			c = appendTrailingUserTurn(c)
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

	if genCfg := buildGenerationConfig(payloadMap); len(genCfg) > 0 {
		dropUnsupportedPenaltyConfig(model, cfg, genCfg)
		if len(genCfg) > 0 {
			vars["generationConfig"] = genCfg
		}
	}

	if _, ok := vars["safetySettings"]; !ok {
		if _, ok2 := payloadMap["safety_settings"]; !ok2 {
			vars["safetySettings"] = buildSafetySettings(cfg)
		}
	}

	return vars
}

func dropUnsupportedPenaltyConfig(model string, cfg config.ConfigProvider, generationConfig map[string]any) {
	realModel := model
	if cfg != nil {
		realModel = cfg.ResolveModelName(model)
	}
	switch strings.ToLower(strings.TrimSpace(realModel)) {
	case "gemini-3.6-flash", "gemini-3.5-flash-lite":
		delete(generationConfig, "presencePenalty")
		delete(generationConfig, "frequencyPenalty")
	}
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

		// 合并相邻的具有相同 normalized 角色的 content 回合，确保角色严格交替
		merged := []any{}
		for _, item := range normalized {
			m, ok := item.(map[string]any)
			if !ok {
				merged = append(merged, item)
				continue
			}
			role, _ := m["role"].(string)
			if role == "function" || role == "tool" {
				if len(merged) > 0 {
					if last, ok := merged[len(merged)-1].(map[string]any); ok {
						lastRole, _ := last["role"].(string)
						if lastRole == "function" || lastRole == "tool" {
							lastParts, _ := last["parts"].([]any)
							currentParts, _ := m["parts"].([]any)
							mergedParts := make([]any, len(lastParts)+len(currentParts))
							copy(mergedParts, lastParts)
							copy(mergedParts[len(lastParts):], currentParts)
							last["parts"] = mergedParts
							continue
						}
					}
				}
			}
			// 浅拷贝一份 map，避免在重试（第二次走 pipeline）时污染原始的 geminiPayload
			newM := make(map[string]any, len(m))
			for k, v := range m {
				newM[k] = v
			}
			merged = append(merged, newM)
		}
		return merged
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
		out[strutil.SnakeToCamel(k)] = v
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
			camelK := strutil.SnakeToCamel(k)
			switch camelK {
			case "inlineData":
				if vm, ok := v.(map[string]any); ok {
					nid := map[string]any{}
					for ik, iv := range vm {
						nid[strutil.SnakeToCamel(ik)] = iv
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
		cik := strutil.SnakeToCamel(ik)
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

func trailingModelFixActive(model string, cfg config.ConfigProvider) bool {
	if cfg == nil || !cfg.ModelTurnGuardEnabled() {
		return false
	}
	realModel := cfg.ResolveModelName(model)
	entry, ok := cfg.LookupModel(realModel)
	return ok && entry.TrailingFixEnabled
}

// endsWithModelTurn 判断对话是否以未闭合的 model 回合结尾。
func endsWithModelTurn(content map[string]any) bool {
	role, _ := content["role"].(string)
	return strings.EqualFold(role, "model") || strings.EqualFold(role, "assistant")
}

func appendTrailingUserTurn(contents any) any {
	list, ok := contents.([]any)
	if !ok || len(list) == 0 {
		return contents
	}
	last, ok := list[len(list)-1].(map[string]any)
	if !ok || !endsWithModelTurn(last) {
		return contents
	}
	if modelTurnHasFunctionCall(last) {
		return contents
	}
	return append(list, map[string]any{
		"role":  "user",
		"parts": []any{map[string]any{"text": "\n"}},
	})
}

func modelTurnHasFunctionCall(content map[string]any) bool {
	for _, rawPart := range asAnySlice(content["parts"]) {
		part, ok := rawPart.(map[string]any)
		if !ok {
			continue
		}
		fc, ok := part["functionCall"].(map[string]any)
		if ok && strings.TrimSpace(toString(fc["name"])) != "" {
			return true
		}
	}
	return false
}
