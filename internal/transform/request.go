package transform

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/config"
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

// ConvertChatRequest 将 OpenAI ChatCompletion 请求体转为 (model, geminiPayload)。
func ConvertChatRequest(body map[string]any, cfg config.ConfigProvider) (string, map[string]any, error) {
	model, _ := body["model"].(string)

	if cfg.DebugMode() {
		geminiPayloadStr, _ := json.Marshal(body)
		log.Printf("[DEBUG] Payload 打印: ConvertChatRequest 转换前 payload: %s", string(geminiPayloadStr))
	}

	messagesRaw, ok := body["messages"].([]any)
	if !ok || len(messagesRaw) == 0 {
		return "", nil, fmt.Errorf("messages 不能为空 (messages must be a non-empty array)")
	}

	contents := []any{}
	var systemParts []any
	toolIDToName := map[string]string{}

	for _, msgRaw := range messagesRaw {
		msg, ok := msgRaw.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		content := msg["content"]

		switch role {
		case "system", "developer":
			switch c := content.(type) {
			case string:
				systemParts = append(systemParts, map[string]any{"text": c})
			case []any:
				for _, item := range c {
					if im, ok := item.(map[string]any); ok {
						if t, _ := im["type"].(string); t == "text" || t == "input_text" {
							systemParts = append(systemParts, map[string]any{"text": im["text"]})
						}
					} else if s, ok := item.(string); ok {
						systemParts = append(systemParts, map[string]any{"text": s})
					}
				}
			}
		case "user":
			parts := convertUserContent(content)
			if len(parts) > 0 {
				contents = append(contents, map[string]any{"role": "user", "parts": parts})
			}
		case "assistant":
			var parts []any
			if isTruthy(content) {
				parts = append(parts, splitAssistantContent(content)...)
			}
			if toolCalls, ok := msg["tool_calls"].([]any); ok {
				for _, tc := range toolCalls {
					parsed := extractOAIToolCall(tc)
					if parsed == nil {
						continue
					}
					if parsed.id != "" {
						toolIDToName[parsed.id] = parsed.name
					}
					fc := map[string]any{"name": parsed.name, "args": parsed.args}
					if parsed.id != "" {
						fc["id"] = parsed.id
					}
					parts = append(parts, map[string]any{"functionCall": fc})
				}
			}
			if len(parts) > 0 {
				contents = append(contents, map[string]any{"role": "model", "parts": parts})
			}
		case "tool":
			tcID, _ := msg["tool_call_id"].(string)
			name := firstTruthyString(msg["name"], toolIDToName[tcID])
			fr := map[string]any{"response": coerceFunctionResponse(msg["content"])}
			if name != "" {
				fr["name"] = name
			}
			if tcID != "" {
				fr["id"] = tcID
			}
			contents = appendFunctionResponse(contents, map[string]any{"functionResponse": fr})
		case "function":
			name := firstTruthyString(msg["name"])
			if name == "" {
				name = "unknown"
			}
			contents = appendFunctionResponse(contents, map[string]any{"functionResponse": map[string]any{
				"name": name, "response": coerceFunctionResponse(msg["content"]),
			}})
		}
	}
	realModel := cfg.ResolveModelName(model)
	if cfg.ModelTurnGuardEnabled() && (strings.Contains(realModel, "gemini-3.6-flash") || strings.Contains(realModel, "gemini-3.5-flash-lite")) && len(contents) > 0 {
		if lastContent, ok := contents[len(contents)-1].(map[string]any); ok {
			if lastContent["role"] == "model" {
				contents = append(contents, map[string]any{
					"role": "user",
					"parts": []any{
						map[string]any{"text": "\n"},
					},
				})
			}
		}
	}

	geminiPayload := map[string]any{"contents": contents}
	if len(systemParts) > 0 {
		geminiPayload["systemInstruction"] = map[string]any{"parts": systemParts}
	}

	declaredToolNames, err := convertTools(body, geminiPayload)
	if err != nil {
		return "", nil, err
	}

	if err := convertToolChoice(body, geminiPayload, declaredToolNames); err != nil {
		return "", nil, err
	}

	genCfg := map[string]any{}
	for _, m := range []struct{ oai, gem string }{
		{"temperature", "temperature"},
		{"top_p", "topP"},
		{"top_k", "topK"},
		{"presence_penalty", "presencePenalty"},
		{"frequency_penalty", "frequencyPenalty"},
		{"seed", "seed"},
	} {
		if v, ok := body[m.oai]; ok && v != nil {
			genCfg[m.gem] = v
		}
	}

	if v, ok := body["logprobs"]; ok && v != nil {
		genCfg["responseLogprobs"] = isTruthy(v)
	}
	if v, ok := body["top_logprobs"]; ok && v != nil {
		genCfg["logprobs"] = v
	}

	var maxTokens any
	if v, ok := body["max_tokens"]; ok && v != nil {
		maxTokens = v
	} else if v, ok := body["max_completion_tokens"]; ok && v != nil {
		maxTokens = v
	}
	if maxTokens != nil {
		f, ok := maxTokens.(float64)
		if !ok || f < 1 {
			return "", nil, fmt.Errorf("max_tokens must be an integer >= 1")
		}
		if !cfg.DropMaxTokens() {
			genCfg["maxOutputTokens"] = maxTokens
		}
	}

	if stop, ok := body["stop"]; ok && stop != nil {
		switch s := stop.(type) {
		case string:
			genCfg["stopSequences"] = []any{s}
		case []any:
			genCfg["stopSequences"] = s
		}
	}

	if rf, ok := body["response_format"].(map[string]any); ok {
		if t, _ := rf["type"].(string); t == "json_object" || t == "json_schema" {
			genCfg["responseMimeType"] = "application/json"
			if t == "json_schema" {
				if js, ok := rf["json_schema"].(map[string]any); ok {
					if sch, ok := js["schema"].(map[string]any); ok {
						genCfg["responseSchema"] = sch
					}
				}
			}
		}
	}

	if sl, ok := body["safety_settings"].([]any); ok {
		geminiPayload["safetySettings"] = sl
	} else if sl, ok := body["safetySettings"].([]any); ok {
		geminiPayload["safetySettings"] = sl
	}

	if len(genCfg) > 0 {
		geminiPayload["generationConfig"] = genCfg
	}

	mrRaw := firstPresentRaw(body, "media_resolution", "mediaResolution")
	if mrRaw == nil {
		if extra, ok := body["extra_body"].(map[string]any); ok {
			mrRaw = firstPresentRaw(extra, "media_resolution", "mediaResolution")
		}
	}
	if mrRaw != nil {
		if mr := normalizeMediaResolution(mrRaw); mr != "" {
			ensureGenCfg(geminiPayload)["mediaResolution"] = mr
		}
	}

	if re, ok := body["reasoning_effort"].(string); ok {
		if level, ok := reasoningEffortToThinkingLevel[strings.ToLower(strings.TrimSpace(re))]; ok {
			gc := ensureGenCfg(geminiPayload)
			tc, ok := gc["thinkingConfig"].(map[string]any)
			if !ok {
				tc = map[string]any{}
				gc["thinkingConfig"] = tc
			}
			tc["thinkingLevel"] = level
		}
	}

	if thinking, ok := body["thinking"].(map[string]any); ok {
		if tt, _ := thinking["type"].(string); tt == "enabled" || tt == "disabled" {
			tc := map[string]any{"thinkingLevel": "MEDIUM"}
			if tt == "disabled" {
				tc["thinkingLevel"] = "NONE"
			}
			if budget, ok := thinking["budget_tokens"]; ok && budget != nil {
				tc["thinkingBudget"] = budget
			}
			ensureGenCfg(geminiPayload)["thinkingConfig"] = tc
		}
	}

	return model, geminiPayload, nil
}

// appendFunctionResponse 把一个 functionResponse part 追加进 contents。
func appendFunctionResponse(contents []any, part map[string]any) []any {
	if n := len(contents); n > 0 {
		if last, ok := contents[n-1].(map[string]any); ok && last["role"] == "function" {
			parts, _ := last["parts"].([]any)
			last["parts"] = append(parts, part)
			return contents
		}
	}
	return append(contents, map[string]any{"role": "function", "parts": []any{part}})
}

// coerceFunctionResponse 把 tool/function 角色的 content 规范成 Gemini functionResponse.response。
func coerceFunctionResponse(raw any) map[string]any {
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

// firstPresentRaw 在 map 里依次查 keys，返回第一个存在的原始值（不存在返回 nil）。
func firstPresentRaw(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	return nil
}

// deepCopyAny 深拷贝 map/slice 结构。
func deepCopyAny(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = deepCopyAny(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = deepCopyAny(item)
		}
		return out
	default:
		return v
	}
}

// parseModelName 解析模型名：经 models.json 的 alias_map 重映射。
func parseModelName(model string) string {
	return config.ResolveModelName(model)
}
