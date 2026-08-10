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
				if tc.ID != "" {
					toolIDToName[tc.ID] = tc.Function.Name
				}
				fc := FunctionCall{Name: tc.Function.Name, Args: parseArgsString(tc.Function.Arguments)}
				if tc.ID != "" {
					fc.ID = tc.ID
				}
				parts = append(parts, Part{FunctionCall: &fc})
			}
			if len(parts) > 0 {
				gemini.Contents = append(gemini.Contents, Content{Role: "model", Parts: parts})
			}
		case "tool":
			tcID := msg.ToolCallID
			name := firstTruthyString(msg.Name, toolIDToName[tcID])
			fr := FunctionResponse{Response: coerceFunctionResponseTyped(msg)}
			if name != "" {
				fr.Name = name
			}
			if tcID != "" {
				fr.ID = tcID
			}
			gemini.Contents = append(gemini.Contents, Content{
				Role:  "user",
				Parts: []Part{{FunctionResponse: &fr}},
			})
		case "function":
			name := msg.Name
			if name == "" {
				name = "unknown"
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
		if built := buildSafetySettingsTyped(cfg); len(built) > 0 {
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

// 确保 ConfigFace 实现兼容 config.ConfigProvider 的最小面。
var _ ConfigFace = (config.ConfigProvider)(nil)