package transform

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/domain"
)

// safetyCategories 是默认安全设置覆盖的 6 个类别（缺省全 BLOCK_NONE）。
var safetyCategories = []string{ //nolint:gochecknoglobals
	"HARM_CATEGORY_HARASSMENT",
	"HARM_CATEGORY_HATE_SPEECH",
	"HARM_CATEGORY_SEXUALLY_EXPLICIT",
	"HARM_CATEGORY_DANGEROUS_CONTENT",
	"HARM_CATEGORY_CIVIC_INTEGRITY",
	"HARM_CATEGORY_JAILBREAK",
}

// supportedVarFields 是从 geminiPayload 透传进 variables 的字段（统一 camelCase）。
var supportedVarFields = []string{ //nolint:gochecknoglobals
	"contents", "tools", "toolConfig", "systemInstruction",
	"safetySettings", "generationConfig",
	"cachedContent", "serviceTier", "store",
}

// ConvertChatRequest 将 OpenAI ChatCompletionRequest 强类型请求体转为 (model, *domain.GenerateContentRequest)。
func ConvertChatRequest(req *domain.ChatCompletionRequest, cfg config.ConfigProvider) (string, *domain.GenerateContentRequest, error) {
	if req == nil || len(req.Messages) == 0 {
		return "", nil, fmt.Errorf("messages 不能为空 (messages must be a non-empty array)")
	}

	model := parseModelName(req.Model)

	if cfg != nil && cfg.DebugMode() {
		reqBytes, _ := json.Marshal(req)
		log.Printf("[DEBUG] Payload 打印: ConvertChatRequest 转换前 payload: %s", string(reqBytes))
	}

	coord := NewToolCallCoordinator()
	var contents []domain.Content
	var systemParts []domain.Part

	for _, msg := range req.Messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		content := msg.Content

		switch role {
		case "system", "developer":
			parts := extractSystemParts(content)
			systemParts = append(systemParts, parts...)

		case "user":
			parts := convertUserContentTyped(content)
			if len(parts) > 0 {
				contents = append(contents, domain.Content{
					Role:  "user",
					Parts: parts,
				})
			}

		case "assistant":
			var parts []domain.Part
			if isTruthy(content) {
				parts = append(parts, splitAssistantContentTyped(content)...)
			}
			if len(msg.ToolCalls) > 0 {
				toolParts := coord.RegisterAssistantToolCalls(msg.ToolCalls)
				parts = append(parts, toolParts...)
			}
			if len(parts) > 0 {
				contents = append(contents, domain.Content{
					Role:  "model",
					Parts: parts,
				})
			}

		case "tool":
			part := coord.PairToolResponse(msg.ToolCallID, msg.Name, content)
			contents = appendFunctionResponseTyped(contents, part)

		case "function":
			part := coord.PairFunctionResponse(msg.Name, content)
			contents = appendFunctionResponseTyped(contents, part)
		}
	}

	genReq := &domain.GenerateContentRequest{
		Contents: contents,
	}

	if len(systemParts) > 0 {
		genReq.SystemInstruction = &domain.Content{
			Parts: systemParts,
		}
	}

	// 工具声明转换
	var legacyFunctions []any
	if req.ExtraBody != nil {
		if fns, ok := req.ExtraBody["functions"].([]any); ok {
			legacyFunctions = fns
		}
	}
	tools, err := coord.ConvertTools(req.Tools, legacyFunctions)
	if err != nil {
		return "", nil, err
	}
	if len(tools) > 0 {
		genReq.Tools = tools
	}

	// ToolChoice 转换
	toolChoice := req.ToolChoice
	if toolChoice == nil && req.ExtraBody != nil {
		toolChoice = req.ExtraBody["function_call"]
	}
	toolConfig, err := coord.ConvertToolChoice(toolChoice)
	if err != nil {
		return "", nil, err
	}
	if toolConfig != nil {
		genReq.ToolConfig = toolConfig
	}

	// GenerationConfig 转换
	genCfg, err := buildGenerationConfigTyped(req, cfg)
	if err != nil {
		return "", nil, err
	}
	if genCfg != nil {
		genReq.GenerationConfig = genCfg
	}

	// SafetySettings
	if len(req.SafetySettings) > 0 {
		genReq.SafetySettings = req.SafetySettings
	}

	return model, genReq, nil
}

// MapToChatCompletionRequest 将 map[string]any 转为 *domain.ChatCompletionRequest。
func MapToChatCompletionRequest(body map[string]any) (*domain.ChatCompletionRequest, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request body failed: %w", err)
	}

	var req domain.ChatCompletionRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, fmt.Errorf("unmarshal request to ChatCompletionRequest failed: %w", err)
	}

	// 兼容顶层 functions 和 function_call 注入 ExtraBody
	if fns, ok := body["functions"]; ok && fns != nil {
		if req.ExtraBody == nil {
			req.ExtraBody = make(map[string]any)
		}
		req.ExtraBody["functions"] = fns
	}
	if fc, ok := body["function_call"]; ok && fc != nil {
		if req.ExtraBody == nil {
			req.ExtraBody = make(map[string]any)
		}
		req.ExtraBody["function_call"] = fc
	}

	return &req, nil
}

// GenerateContentRequestToMap 将 *domain.GenerateContentRequest 序列化为 map[string]any。
func GenerateContentRequestToMap(genReq *domain.GenerateContentRequest) (map[string]any, error) {
	if genReq == nil {
		return nil, nil
	}
	data, err := json.Marshal(genReq)
	if err != nil {
		return nil, fmt.Errorf("marshal GenerateContentRequest failed: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("unmarshal to map failed: %w", err)
	}
	return out, nil
}

// ConvertChatRequestMap 为接受 map[string]any 的入口提供适配封装。
func ConvertChatRequestMap(body map[string]any, cfg config.ConfigProvider) (string, map[string]any, error) {
	req, err := MapToChatCompletionRequest(body)
	if err != nil {
		return "", nil, err
	}
	model, genReq, err := ConvertChatRequest(req, cfg)
	if err != nil {
		return "", nil, err
	}
	payloadMap, err := GenerateContentRequestToMap(genReq)
	if err != nil {
		return "", nil, err
	}
	return model, payloadMap, nil
}

// ConvertChatRequestBody 是 ConvertChatRequestMap 的别名。
func ConvertChatRequestBody(body map[string]any, cfg config.ConfigProvider) (string, map[string]any, error) {
	return ConvertChatRequestMap(body, cfg)
}

func extractSystemParts(content any) []domain.Part {
	var parts []domain.Part
	switch c := content.(type) {
	case string:
		if c != "" {
			parts = append(parts, domain.Part{Text: c})
		}
	case []any:
		for _, item := range c {
			if im, ok := item.(map[string]any); ok {
				if t, _ := im["type"].(string); t == "text" || t == "input_text" {
					parts = append(parts, domain.Part{Text: toString(im["text"])})
				}
			} else if s, ok := item.(string); ok {
				parts = append(parts, domain.Part{Text: s})
			}
		}
	case []domain.MessageContentPart:
		for _, p := range c {
			if p.Type == "text" || p.Type == "input_text" {
				parts = append(parts, domain.Part{Text: p.Text})
			}
		}
	}
	return parts
}

func appendFunctionResponseTyped(contents []domain.Content, part domain.Part) []domain.Content {
	if n := len(contents); n > 0 {
		if contents[n-1].Role == "function" {
			contents[n-1].Parts = append(contents[n-1].Parts, part)
			return contents
		}
	}
	return append(contents, domain.Content{
		Role:  "function",
		Parts: []domain.Part{part},
	})
}

// parseModelName 解析模型名：经 models.json 的 alias_map 重映射。
func parseModelName(model string) string {
	return config.ResolveModelName(model)
}
