package transform

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

// 本文件是强类型 OAI -> Gemini 请求转换核心。
// 输入 *ChatCompletionRequest（typed），输出 *GeminiRequest（typed）。
// 复用旧版 params.go / toolcall.go 的映射表与解析辅助（保持符号语义不变）。
//
// 注意：thinking/image/responseModalities 三个默认档位的注入不属于本函数职责，
// 由 api 层 ModelStrategy.Enhance 统一负责。

// ConfigFace 是请求转换需要的最小配置接口（调用方传入 config.ConfigProvider 即可）。
type ConfigFace interface {
	DropMaxTokens() bool
	SafetySettings() map[string]string
}

// ConvertChatRequestTyped 将 OpenAI ChatCompletion 请求体强类型转为 Gemini 请求。
// 返回模型名与 *GeminiRequest。
func ConvertChatRequestToGemini(req *ChatCompletionRequest, cfg ConfigFace) (*GeminiRequest, string, error) {
	model := req.Model

	if len(req.Messages) == 0 {
		return nil, "", fmt.Errorf("messages 不能为空 (messages must be a non-empty array)")
	}

	gemini := &GeminiRequest{}
	var systemParts []Part
	toolIDToName := map[string]string{}

	// 预扫描：针对历史消息建立 tool_call_id 到函数名的映射表（处理部分客户端传入空 function.name 但 tool 消息带 name 的情况）
	for i := range req.Messages {
		m := &req.Messages[i]
		if m.Role == "tool" {
			tcID := strings.TrimSpace(m.ToolCallID)
			name := strings.TrimSpace(m.Name)
			if tcID != "" && name != "" {
				toolIDToName[tcID] = name
			}
		} else if m.Role == "assistant" {
			for _, tc := range m.ToolCalls {
				tcID := strings.TrimSpace(tc.ID)
				name := strings.TrimSpace(tc.Function.Name)
				if tcID != "" && name != "" {
					toolIDToName[tcID] = name
				}
			}
		}
	}

	type pendingToolCall struct {
		id       string
		name     string
		consumed bool
	}
	var pendingCalls []*pendingToolCall

	for i := range req.Messages {
		msg := &req.Messages[i]
		switch msg.Role {
		case "system", "developer":
			if msg.Content.IsString() {
				systemParts = append(systemParts, Part{Text: msg.Content.StringValue()})
			} else {
				for _, p := range msg.Content.Parts {
					if (p.Type == "text" || p.Type == "input_text") && p.Text != "" {
						systemParts = append(systemParts, Part{Text: p.Text})
					}
				}
			}
		case "user":
			parts := convertUserContentTyped(msg.Content)
			if len(parts) > 0 {
				gemini.Contents = append(gemini.Contents, Content{Role: "user", Parts: parts})
			}
		case "assistant":
			var parts []Part
			if msg.Content.IsString() {
				if s := msg.Content.StringValue(); s != "" {
					parts = append(parts, splitAssistantContentTyped(msg.Content)...)
				}
			} else if !msg.Content.IsEmpty() {
				parts = append(parts, splitAssistantContentTyped(msg.Content)...)
			}
			for _, tc := range msg.ToolCalls {
				name := strings.TrimSpace(tc.Function.Name)
				if name == "" && tc.ID != "" {
					name = toolIDToName[tc.ID]
				}
				if name == "" {
					name = inferFunctionNameFromArgs(tc.Function.Arguments, req.Tools)
				}
				if name == "" {
					return nil, "", fmt.Errorf("assistant 工具调用缺少 function.name (missing assistant tool_calls.function.name)")
				}
				if tc.ID != "" && name != "" {
					toolIDToName[tc.ID] = name
				}
				if name != "" {
					pendingCalls = append(pendingCalls, &pendingToolCall{id: tc.ID, name: name, consumed: false})
				}
				fc := FunctionCall{Name: name, Args: parseArgsString(tc.Function.Arguments)}
				if tc.ID != "" {
					fc.ID = tc.ID
				}
				parts = append(parts, Part{FunctionCall: &fc})
			}
			if len(parts) > 0 {
				gemini.Contents = append(gemini.Contents, Content{Role: "model", Parts: parts})
			}
		case "tool":
			tcID := strings.TrimSpace(msg.ToolCallID)
			name := strings.TrimSpace(msg.Name)

			if name == "" && tcID != "" {
				name = toolIDToName[tcID]
			}

			// 尝试标记对应的 pendingCall
			var matched *pendingToolCall
			if name != "" || tcID != "" {
				for _, p := range pendingCalls {
					if !p.consumed {
						if (tcID != "" && p.id == tcID) || (name != "" && p.name == name) {
							matched = p
							if name == "" {
								name = p.name
							}
							break
						}
					}
				}
			}

			// 若仍然无法定位函数名，检查是否存在唯一未消费的前置工具调用以顺序恢复
			if name == "" {
				var unconsumed []*pendingToolCall
				for _, p := range pendingCalls {
					if !p.consumed {
						unconsumed = append(unconsumed, p)
					}
				}
				if len(unconsumed) == 1 {
					matched = unconsumed[0]
					name = matched.name
				} else if len(unconsumed) > 1 {
					return nil, "", fmt.Errorf("tool 消息缺少 name/tool_call_id，且历史存在多个未消费的工具调用，无法精确推断函数名 (ambiguous unconsumed tool calls)")
				} else {
					return nil, "", fmt.Errorf("tool 消息缺少 name 且 tool_call_id (%q) 无法关联到前置 assistant 工具调用 (unmatched tool_call_id)", tcID)
				}
			}

			if matched != nil {
				matched.consumed = true
			}

			fr := FunctionResponse{Name: name, Response: coerceFunctionResponseTyped(msg)}
			if tcID != "" {
				fr.ID = tcID
			}
			gemini.Contents = append(gemini.Contents, Content{
				Role:  "user",
				Parts: []Part{{FunctionResponse: &fr}},
			})
		case "function":
			name := strings.TrimSpace(msg.Name)
			if name == "" {
				var unconsumed []*pendingToolCall
				for _, p := range pendingCalls {
					if !p.consumed {
						unconsumed = append(unconsumed, p)
					}
				}
				if len(unconsumed) == 1 {
					unconsumed[0].consumed = true
					name = unconsumed[0].name
				} else if len(unconsumed) > 1 {
					return nil, "", fmt.Errorf("function 消息缺少 name，且历史存在多个未消费的函数调用 (ambiguous unconsumed function calls)")
				} else {
					return nil, "", fmt.Errorf("function 消息缺少 name 且无法关联到前置函数调用 (missing function name)")
				}
			} else {
				for _, p := range pendingCalls {
					if !p.consumed && p.name == name {
						p.consumed = true
						break
					}
				}
			}

			gemini.Contents = append(gemini.Contents, Content{
				Role:  "user",
				Parts: []Part{{FunctionResponse: &FunctionResponse{
					Name: name, Response: coerceFunctionResponseTyped(msg),
				}}},
			})
		}
	}

	if len(systemParts) > 0 {
		gemini.SystemInstruction = &Content{Parts: systemParts}
	}

	declared := convertToolsTyped(req, gemini)
	if err := convertToolChoiceTyped(req, gemini, declared); err != nil {
		return nil, "", err
	}

	if gc := convertGenConfigTyped(req, cfg); true {
		if gc != nil && hasGenConfig(gc) {
			gemini.GenerationConfig = gc
		}
	}

if ss := req.SafetySettingsList(); len(ss) > 0 {
		var settings []SafetySetting
		for _, s := range ss {
			if m, ok := s.(map[string]any); ok {
settings = append(settings, SafetySetting{
					Category: strings.TrimSpace(toString(m["category"])),
					Threshold: strings.TrimSpace(toString(m["threshold"])),
				})
			}
		}
		if len(settings) > 0 {
			gemini.SafetySettings = settings
		}
	}
	if gemini.SafetySettings == nil && cfg != nil {
		if built := BuildSafetySettingsTyped(cfg); len(built) > 0 {
			gemini.SafetySettings = built
		}
	}

	return gemini, model, nil
}

// applySignatures 对 model 回合历史的 thought/functionCall part 签发签名并
// 统一编码为合法 base64。注意：只处理 model 回合；user/tool 回绝不进签名路径。
func applyHistorySignatures(contents []Content) {
	resolver := NewSignatureResolver()
	for i := range contents {
		if contents[i].Role != "model" {
			continue
		}
		for j := range contents[i].Parts {
			resolver.ApplyPart(&contents[i].Parts[j])
			if contents[i].Parts[j].ThoughtSignature != "" {
				contents[i].Parts[j].ThoughtSignature = resolver.EnsureBase64Sig(contents[i].Parts[j].ThoughtSignature)
			}
		}
	}
}

// convertUserContentTyped 把 user message content 转为 Gemini parts。
func convertUserContentTyped(content MessageContent) []Part {
	if content.IsEmpty() {
		return nil
	}
	if content.IsString() {
		return []Part{{Text: content.StringValue()}}
	}
	var parts []Part
	for _, p := range content.Parts {
		switch p.Type {
		case "text", "input_text":
			if p.Text != "" {
				parts = append(parts, Part{Text: p.Text})
			}
		case "image_url", "input_image":
			ref := p.ImageURL
			if ref == nil {
				ref = p.InputImage
			}
			if ref == nil {
				continue
			}
			url := ref.URL
			if strings.HasPrefix(url, "data:") {
				if mime, b64 := parseDataURI(url); mime != "" && b64 != "" {
					parts = append(parts, Part{InlineData: &InlineData{MimeType: mime, Data: NormalizeBase64(b64)}})
				}
			} else if hasRemotePrefix(url) {
				parts = append(parts, Part{FileData: &FileData{MimeType: guessMIMEFromURL(url), FileURI: url}})
			}
		case "video_url", "input_video":
			ref := p.VideoURL
			if ref == nil {
				ref = p.InputVideo
			}
			if ref == nil {
				continue
			}
			url := ref.URL
			if strings.HasPrefix(url, "data:") {
				mime, b64 := parseDataURI(url)
				if b64 != "" {
					if mime == "" || !strings.HasPrefix(mime, "video/") {
						mime = "video/mp4"
					}
					parts = append(parts, Part{InlineData: &InlineData{MimeType: mime, Data: NormalizeBase64(b64)}})
				}
			}
		case "input_audio":
			if part := convertInputAudioInput(p); part != nil {
				parts = append(parts, *part)
			}
		case "media", "file", "file_data":
			fileURI := firstTruthyString(p.FileURI, p.FileURIAlt, p.URI, p.URL)
			if fileURI != "" {
				mime := firstTruthyString(p.MimeType, p.MimeTypeAlt)
				if mime == "" {
					mime = guessMIMEFromURI(fileURI)
				}
				parts = append(parts, Part{FileData: &FileData{MimeType: mime, FileURI: fileURI}})
			}
		case "inline_data", "inlineData":
			id := p.InlineData
			if id != nil && id.Data != "" && id.MimeType != "" {
				parts = append(parts, Part{InlineData: &InlineData{MimeType: id.MimeType, Data: NormalizeBase64(id.Data)}})
			}
		}
	}
	return parts
}

// convertInputAudioInput 处理 input_audio part（data/url + format）。
func convertInputAudioInput(p ContentPart) *Part {
	switch {
	case p.InputAudio == nil:
		return nil
	case p.InputAudio.Data != "":
		if strings.HasPrefix(p.InputAudio.Data, "data:") {
			mime, b64 := parseDataURI(p.InputAudio.Data)
			if b64 == "" {
				return nil
			}
			if mime == "" || !strings.HasPrefix(mime, "audio/") {
				mime = AudioInputMIME(p.InputAudio.Format)
			}
			return &Part{InlineData: &InlineData{MimeType: mime, Data: NormalizeBase64(b64)}}
		}
		mime := AudioInputMIME(p.InputAudio.Format)
		return &Part{InlineData: &InlineData{MimeType: mime, Data: NormalizeBase64(p.InputAudio.Data)}}
	case p.InputAudio.URL != "" && strings.HasPrefix(p.InputAudio.URL, "data:"):
		mime, b64 := parseDataURI(p.InputAudio.URL)
		if b64 == "" {
			return nil
		}
		if mime == "" || !strings.HasPrefix(mime, "audio/") {
			mime = AudioInputMIME(p.InputAudio.Format)
		}
		return &Part{InlineData: &InlineData{MimeType: mime, Data: NormalizeBase64(b64)}}
	}
	return nil
}

// assistantImageMarkdownRe 匹配 assistant 文本中的 markdown data URL 图片。
var assistantImageMarkdownRe = regexp.MustCompile(`!\[.*?\]\((data:image\/([a-zA-Z0-9\+\-\.]+);base64,([A-Za-z0-9+/=]+))\)`)

// splitAssistantContentTyped 把 assistant 文本切成 text / inlineData 混合 parts。
func splitAssistantContentTyped(content MessageContent) []Part {
	if !content.IsString() {
		return []Part{{Text: content.StringValue()}}
	}
	s := content.StringValue()
	locs := assistantImageMarkdownRe.FindAllStringSubmatchIndex(s, -1)
	if len(locs) == 0 {
		return []Part{{Text: s}}
	}
	var parts []Part
	last := 0
	for _, m := range locs {
		if pre := strings.TrimSpace(s[last:m[0]]); pre != "" {
			parts = append(parts, Part{Text: pre})
		}
		if mime, b64 := parseDataURI(s[m[2]:m[3]]); mime != "" && b64 != "" {
			parts = append(parts, Part{InlineData: &InlineData{MimeType: mime, Data: NormalizeBase64(b64)}})
		}
		last = m[1]
	}
	if post := strings.TrimSpace(s[last:]); post != "" {
		parts = append(parts, Part{Text: post})
	}
	if len(parts) == 0 {
		parts = append(parts, Part{Text: ""})
	}
	return parts
}

// coerceFunctionResponseTyped 规范化 tool/function 角色 content。
func coerceFunctionResponseTyped(msg *Message) any {
	var raw any
	if msg.Content.IsString() {
		raw = msg.Content.StringValue()
	} else {
		hasText := false
		var texts []string
		for _, p := range msg.Content.Parts {
			if p.Type == "text" && p.Text != "" {
				texts = append(texts, p.Text)
				hasText = true
			}
		}
		if !hasText {
			if len(msg.Content.Parts) == 1 {
				if msg.Content.Parts[0].Text != "" {
					raw = msg.Content.Parts[0].Text
				} else {
					return map[string]any{"result": ""}
				}
			} else {
				return map[string]any{"result": map[string]any{}}
			}
		} else {
			raw = strings.Join(texts, "\n")
		}
	}
	obj := raw
	if s, ok := raw.(string); ok {
		var parsed any
		if err := json.Unmarshal([]byte(s), &parsed); err == nil {
			obj = parsed
		} else {
			obj = map[string]any{"result": s}
		}
	}
	if m, ok := obj.(map[string]any); ok {
		return m
	}
	return map[string]any{"result": obj}
}

// parseArgsString 把 arguments 字符串解析为 any。
func parseArgsString(arguments string) any {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return map[string]any{}
	}
	var parsed any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
		return parsed
	}
	return map[string]any{"raw": arguments}
}

// SafetySettingsList 把安全设置原样返回列表。
func (r *ChatCompletionRequest) SafetySettingsList() []any {
	if list, ok := r.SafetySettings.([]any); ok {
		return list
	}
	return nil
}

// intPtr 返回 int 指针。
func intPtr(v int) *int { return &v }

// floatPtr 返回 float64 指针。
func floatPtr(v float64) *float64 { return &v }

// int64Ptr 返回 int64 指针。
func int64Ptr(v int64) *int64 { return &v }

// boolPtr 返回 bool 指针。
func boolPtr(v bool) *bool { return &v }

// inferFunctionNameFromArgs 根据参数键与工具声明尝试推断/恢复丢失的工具函数名。
func inferFunctionNameFromArgs(argsStr string, tools []OAITool) string {
	rawArgs := parseArgsString(argsStr)
	argsMap, _ := rawArgs.(map[string]any)

	// 1. 若当前请求仅声明了一个工具，且非空，优先直接归属于该工具
	if len(tools) == 1 && tools[0].Function.Name != "" {
		return tools[0].Function.Name
	}

	if len(argsMap) == 0 {
		return ""
	}

	// 2. 优先比对当前请求中 req.Tools 声明的 Schema 属性（确保恢复出的名称强锁定在请求允许的工具范围内）
	for _, t := range tools {
		if t.Function.Name == "" || t.Function.Parameters == nil {
			continue
		}
		props, ok := t.Function.Parameters["properties"].(map[string]any)
		if !ok {
			continue
		}
		matchAll := true
		for k := range argsMap {
			if _, exists := props[k]; !exists {
				matchAll = false
				break
			}
		}
		if matchAll {
			return t.Function.Name
		}
	}

	// 3. 仅在 req.Tools 无法匹配时，使用静态特征规则兜底
	if _, ok := argsMap["filePath"]; ok {
		return "read"
	}
	if _, ok := argsMap["command"]; ok {
		return "bash"
	}
	if _, ok := argsMap["cmd"]; ok {
		return "bash"
	}
	if _, ok := argsMap["pattern"]; ok {
		return "grep"
	}
	if _, ok := argsMap["todos"]; ok {
		return "todowrite"
	}

	return ""
}

// 确保 ConfigFace 实现兼容 config.ConfigProvider 的最小面。
var _ ConfigFace = (config.ConfigProvider)(nil)