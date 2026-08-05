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
