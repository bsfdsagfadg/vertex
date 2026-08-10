package transform

import (
	"encoding/json"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

// 本文件是"强类型请求 → 上游 variables"的构建层（取代旧 request_text.go 的
// BuildVertexVariables map 版）。输入是已增强的 GeminiRequest，输出是发往
// batchGraphql 的 variables 结构。所有对内容的后处理（同 role 合并、空内容
// 过滤、systemInstruction 降级、尾部"继续"补足）都在 typed 上完成，零 map 往返。

// BuildGeminiVariables 由强类型请求构建发往上游的 variables。
//
// 与旧 map 版的差异：输入已结构化，故不再需要 normalizeContents /
// handleInlineData 等 map 清洗；签名注入（applyHistorySignatures）与
// base64 规范化（NormalizeBase64）已由请求转换阶段完成，这里仅负责
// 内容后处理与包壳。
func BuildGeminiVariables(model string, req *GeminiRequest, cfg config.ConfigProvider) map[string]any {
	if req == nil {
		req = &GeminiRequest{}
	}

	contents := mergeContiguousRolesTyped(req.Contents)
	contents = filterEmptyContentsTyped(contents)
	applyHistorySignatures(contents)
	sysInstruction := req.SystemInstruction

	// systemInstruction 降级：contents 不含任何 user 回合时，把 system 文本
	// 提前为首条 user message（对齐旧 handleSystemInstruction 语义）。
	if sysInstruction != nil && !contentsHasUser(contents) {
		if text := extractTextTyped(sysInstruction); text != "" {
			contents = append([]Content{{Role: "user", Parts: []Part{{Text: text}}}}, contents...)
			sysInstruction = nil
		}
	}

	// 尾部兼容（TrailingModelFix）：命中清单且历史以 model 回合结尾时补 user 回合。
	if cfg != nil && cfg.TrailingModelFixEnabled() && matchTrailingFixModel(model, cfg.TrailingFixModels()) {
		if endsWithModelTurnTyped(contents) {
			contents = append(contents, Content{Role: "user", Parts: []Part{{Text: "继续"}}})
		}
	}

	out := &GeminiRequest{
		Contents:          contents,
		SystemInstruction: sysInstruction,
		Tools:             prepareNativeTools(req.Tools),
		ToolConfig:        prepareNativeToolConfig(req.ToolConfig),
		SafetySettings:    prepareNativeSafetySettings(req.SafetySettings),
		GenerationConfig:  prepareNativeGenerationConfig(req.GenerationConfig),
		CachedContent:     req.CachedContent,
		ServiceTier:       req.ServiceTier,
		Store:             req.Store,
	}

	vars := marshalRequest(out)
	vars["model"] = config.ResolveModelName(model)
	return vars
}

// mergeContiguousRolesTyped 合并相邻同 role 的 content。
//
// 规则：只要 prev 或 c 包含 FunctionResponse，则绝对禁止与其他 Content 合并
// （即使对方同样含有 FunctionResponse 或同为 user/function role），
// 强制保持 FunctionResponse 处于独占的 Content 节点中，防止 Google Vertex AI 上游
// 抛出 400 INVALID_ARGUMENT (Function response content must be unique)。
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
		if partsContainFunctionResponseTyped(prev.Parts) || partsContainFunctionResponseTyped(c.Parts) {
			merged = append(merged, c)
			continue
		}
		prev.Parts = append(prev.Parts, c.Parts...)
	}
	return merged
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

// matchTrailingFixModel 检查模型名称是否匹配尾部修复清单。
func matchTrailingFixModel(model string, list []string) bool {
	for _, item := range list {
		if strings.EqualFold(item, model) || strings.Contains(strings.ToLower(model), strings.ToLower(item)) {
			return true
		}
	}
	return false
}

// marshalRequest 把 typed 请求序列化为 map（不产生任何额外字段）。
func marshalRequest(req *GeminiRequest) map[string]any {
	b, err := json.Marshal(req)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return map[string]any{}
	}
	return m
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
	out := make([]SafetySetting, len(settings))
	for i, s := range settings {
		out[i] = SafetySetting{
			Category:  strings.ToUpper(strings.TrimSpace(s.Category)),
			Threshold: strings.ToUpper(strings.TrimSpace(s.Threshold)),
		}
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