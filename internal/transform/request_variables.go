package transform

import (
	"encoding/json"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

// 本文件是"强类型请求 → 上游 variables"的构建层。
// 输入是已增强的 GeminiRequest，通过各家族 Strategy 分发构建对应的强类型 variables 结构。

// BuildGeminiVariablesTyped 由强类型请求按模型家族 Strategy 构建发往上游的强类型 variables 结构体。
func BuildGeminiVariablesTyped(model string, req *GeminiRequest, cfg config.ConfigProvider) *GeminiVariables {
	router := NewModelFamilyRouter()
	strategy := router.For(model)
	return strategy.BuildVariables(model, req, cfg)
}

// BuildGeminiVariables 由强类型请求构建发往上游的 variables map 兼容层。
func BuildGeminiVariables(model string, req *GeminiRequest, cfg config.ConfigProvider) map[string]any {
	vars := BuildGeminiVariablesTyped(model, req, cfg)
	b, err := json.Marshal(vars)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return map[string]any{}
	}
	return m
}

// mergeContiguousRolesTyped 合并相邻同 role 的 content。
//
// 规则：
// 1. 若相邻两个 Content 均为 FunctionResponse（即 partsIsOnlyFunctionResponsesTyped 为 true），
//    则允许并推荐合并到一个 Content 中，满足 Vertex AI 上游对于多工具调用 (Parallel Tool Calls) 需打包在同一个 turn 的强要求；
// 2. 若只有一个 Content 包含 FunctionResponse 而另一个是普通 user 内容（如随后的文本/图片提示词），
//    则严格禁止合并，保持 FunctionResponse 在独立 Content turn 中隔离。
func mergeContiguousRolesTyped(contents []Content) []Content {
	if len(contents) == 0 {
		return contents
	}
	merged := []Content{contents[0]}
	for _, c := range contents[1:] {
		prev := &merged[len(merged)-1]
		if c.Role != prev.Role {
			merged = append(merged, c)
			continue
		}
		prevIsFR := partsIsOnlyFunctionResponsesTyped(prev.Parts)
		currIsFR := partsIsOnlyFunctionResponsesTyped(c.Parts)

		if prevIsFR && currIsFR {
			// 两者都是纯 FunctionResponse，合并为同一个 Content turn
			prev.Parts = append(prev.Parts, c.Parts...)
			continue
		}

		if partsContainFunctionResponseTyped(prev.Parts) || partsContainFunctionResponseTyped(c.Parts) {
			// 其中一个为 FunctionResponse，另一个包含非 FunctionResponse 内容（如 user text），隔离禁止合并
			merged = append(merged, c)
			continue
		}

		// 普通同 role 消息合并
		prev.Parts = append(prev.Parts, c.Parts...)
	}
	return merged
}

// sanitizeContentRolesTyped 将 contents 中 Role 为空或纯空白的 Content
// 兜底为 "user"，满足 Vertex AI 上游对 role 字段的非空强要求。
// 仅补全空值，不改动已有的合法 role（user/model）。
func sanitizeContentRolesTyped(contents []Content) []Content {
	for i := range contents {
		if strings.TrimSpace(contents[i].Role) == "" {
			contents[i].Role = "user"
		}
	}
	return contents
}

// filterEmptyContentsTyped 丢弃无任何有效字段的 part，并丢弃清洗后 parts 为空的 content。
func filterEmptyContentsTyped(contents []Content) []Content {
	var filtered []Content
	for i := range contents {
		parts := make([]Part, 0, len(contents[i].Parts))
		for _, p := range contents[i].Parts {
			if partEmptyTyped(p) {
				continue
			}
			parts = append(parts, p)
		}
		if len(parts) == 0 {
			continue
		}
		c := contents[i]
		c.Parts = parts
		filtered = append(filtered, c)
	}
	return filtered
}

// partEmptyTyped 判断 part 是否不含任何有效内容字段。
func partEmptyTyped(p Part) bool {
	if p.Text != "" || p.Thought {
		return false
	}
	if p.InlineData != nil || p.FileData != nil {
		return false
	}
	if p.FunctionCall != nil || p.FunctionResponse != nil {
		return false
	}
	if p.ExecutableCode != nil || p.CodeExecutionResult != nil {
		return false
	}
	return p.VideoMetadata == nil && p.MediaResolution == ""
}

// extractTextTyped 汇总 Content 中所有纯文本 part 的文本。
func extractTextTyped(c *Content) string {
	var sb strings.Builder
	for _, p := range c.Parts {
		if p.Text != "" {
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

// contentsHasUser 判断 contents 中是否存在 user 回合。
func contentsHasUser(contents []Content) bool {
	for _, c := range contents {
		if c.Role == "user" {
			return true
		}
	}
	return false
}

// endsWithModelTurnTyped 判断对话历史是否以模型回合（model）结尾。
func endsWithModelTurnTyped(contents []Content) bool {
	if len(contents) == 0 {
		return false
	}
	role := contents[len(contents)-1].Role
	return role == "model" || role == "assistant"
}

// partsContainFunctionResponseTyped 判断 parts 中是否存在含 functionResponse 的 part。
func partsContainFunctionResponseTyped(parts []Part) bool {
	for _, p := range parts {
		if p.FunctionResponse != nil {
			return true
		}
	}
	return false
}

// partsIsOnlyFunctionResponsesTyped 判断 parts 是否非空且所有 part 均为 functionResponse。
func partsIsOnlyFunctionResponsesTyped(parts []Part) bool {
	if len(parts) == 0 {
		return false
	}
	for _, p := range parts {
		if p.FunctionResponse == nil {
			return false
		}
	}
	return true
}

// matchTrailingFixModel 检查模型名称是否匹配尾部修复清单。
func matchTrailingFixModel(model string, list []string) bool {
	for _, item := range list {
		if strings.EqualFold(item, model) || strings.Contains(strings.ToLower(model), strings.ToLower(item)) {
			return true
		}
	}
	return false
}

// prepareNativeTools 把 Tools 中各 FunctionDeclaration 的 Parameters
// 转换为私有 GraphQL 端点 (google_cloud_aiplatform_ui_Schema) 认可的原生 Map-style Schema，
// 包含将 type 转换为全大写 (OBJECT/STRING/NUMBER 等) 及 properties 转为 key/value 数组。
func prepareNativeTools(tools []Tool) []Tool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]Tool, len(tools))
	for i, t := range tools {
		toolCopy := t
		if len(t.FunctionDeclarations) > 0 {
			decls := make([]FunctionDeclaration, len(t.FunctionDeclarations))
			for j, decl := range t.FunctionDeclarations {
				declCopy := decl
				if declCopy.Parameters != nil {
					cleaned := cleanFunctionParameters(declCopy.Parameters)
					declCopy.Parameters = toNativeSchema(cleaned)
				}
				decls[j] = declCopy
			}
			toolCopy.FunctionDeclarations = decls
		}
		out[i] = toolCopy
	}
	return out
}

// prepareNativeToolConfig 规范化 ToolConfig，确保 functionCallingConfig.mode 为 Vertex AI 上游 GraphQL 支持的值。
// 如果客户端传入非标准 mode（如 "VALIDATED" / "validated"），将其归一化为 "AUTO"。
func prepareNativeToolConfig(tc *ToolConfig) *ToolConfig {
	if tc == nil || tc.FunctionCallingConfig == nil {
		return tc
	}
	fcc := tc.FunctionCallingConfig
	mode := strings.ToUpper(strings.TrimSpace(fcc.Mode))
	switch mode {
	case "AUTO", "NONE", "ANY":
		fcc.Mode = mode
	default:
		// 上游仅支持 AUTO, NONE, ANY；非法/未知 mode 兜底为 AUTO
		fcc.Mode = "AUTO"
	}
	return tc
}

// prepareNativeSafetySettings 规范化 SafetySettings 中的 Category 与 Threshold 枚举为全大写并去除首尾空格。
func prepareNativeSafetySettings(settings []SafetySetting) []SafetySetting {
	if len(settings) == 0 {
		return nil
	}
	out := make([]SafetySetting, 0, len(settings))
	for _, s := range settings {
		cat := strings.ToUpper(strings.TrimSpace(s.Category))
		out = append(out, SafetySetting{
			Category:  cat,
			Threshold: strings.ToUpper(strings.TrimSpace(s.Threshold)),
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// prepareNativeGenerationConfig 规范化 GenerationConfig 中的枚举字段（MediaResolution, ThinkingLevel, ResponseModalities）。
func prepareNativeGenerationConfig(gc *GenerationConfig) *GenerationConfig {
	if gc == nil {
		return nil
	}

	// 1. 媒体分辨率 (MediaResolution) 处理
	if gc.MediaResolution != "" {
		trimmed := strings.ToUpper(strings.TrimSpace(gc.MediaResolution))
		switch trimmed {
		case "LOW":
			gc.MediaResolution = "MEDIA_RESOLUTION_LOW"
		case "MEDIUM":
			gc.MediaResolution = "MEDIA_RESOLUTION_MEDIUM"
		case "HIGH":
			gc.MediaResolution = "MEDIA_RESOLUTION_HIGH"
		case "MEDIA_RESOLUTION_LOW", "MEDIA_RESOLUTION_MEDIUM", "MEDIA_RESOLUTION_HIGH", "MEDIA_RESOLUTION_UNSPECIFIED":
			gc.MediaResolution = trimmed
		default:
			gc.MediaResolution = trimmed
		}
	}

	// 2. 思考配置 (ThinkingConfig) 处理
	if gc.ThinkingConfig != nil && gc.ThinkingConfig.ThinkingLevel != "" {
		level := strings.ToUpper(strings.TrimSpace(gc.ThinkingConfig.ThinkingLevel))
		switch level {
		case "LOW", "MEDIUM", "HIGH", "NONE", "MINIMAL", "THINKING_LEVEL_UNSPECIFIED":
			gc.ThinkingConfig.ThinkingLevel = level
		default:
			gc.ThinkingConfig.ThinkingLevel = "MEDIUM"
		}
	}

	// 3. 响应模态 (ResponseModalities) 处理
	if len(gc.ResponseModalities) > 0 {
		modalities := make([]string, len(gc.ResponseModalities))
		for i, m := range gc.ResponseModalities {
			modalities[i] = strings.ToUpper(strings.TrimSpace(m))
		}
		gc.ResponseModalities = modalities
	}

	return gc
}
