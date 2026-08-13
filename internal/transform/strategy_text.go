package transform

import (
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

// TextStrategy 文本/思考模型家族策略。
type TextStrategy struct {
	model string
}

// Family 返回文本家族。
func (s *TextStrategy) Family() ModelFamily { return FamilyText }

// FamilyStreamMode 文本家族支持真流式。
func (s *TextStrategy) FamilyStreamMode() StreamMode { return StreamModeTrue }

// Enhance 注入 thinkingConfig 默认档位：
//   - 客户端显式传入的 thinkingConfig 优先（经过 NormalizeThinkingConfig 大写规范化与机制折算，不被覆盖）；
//   - 否则按 (DefaultThinkingLevel, model) 解析注入控制台默认值（自动→-1 预算 / 档位→level 或 budget）。
func (s *TextStrategy) Enhance(req *GeminiRequest, cfg config.ConfigProvider) {
	gc := req.GenerationConfig
	if gc == nil {
		gc = &GenerationConfig{}
		req.GenerationConfig = gc
	}
	if gc.ThinkingConfig != nil {
		gc.ThinkingConfig = NormalizeThinkingConfig(gc.ThinkingConfig, s.model)
		return
	}
	if tc := ResolveThinkingConfig(cfg.DefaultThinkingLevel(), s.model); tc != nil {
		gc.ThinkingConfig = tc
	}
}

// Validate 文本家族无跨家族载荷硬约束。
func (s *TextStrategy) Validate(req *GeminiRequest) error { return nil }

// Prepare 文本家族无特化数据清洗逻辑。
func (s *TextStrategy) Prepare(req *GeminiRequest) {}

// BuildVariables 实现文本家族独占的上行 variables 构建：
// 保留同 role 合并、历史思维链签名注入、TrailingModelFix、系统指令 fallback 等所有语言模型核心增强。
func (s *TextStrategy) BuildVariables(model string, req *GeminiRequest, cfg config.ConfigProvider) *GeminiVariables {
	if req == nil {
		req = &GeminiRequest{}
	}

	contents := sanitizeContentRolesTyped(req.Contents)
	contents = mergeContiguousRolesTyped(contents)
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

	safetySettings := req.SafetySettings
	if len(safetySettings) == 0 && cfg != nil {
		safetySettings = BuildSafetySettingsTyped(cfg)
	}

	out := &GeminiRequest{
		Contents:          contents,
		SystemInstruction: sysInstruction,
		Tools:             prepareNativeTools(req.Tools),
		ToolConfig:        prepareNativeToolConfig(req.ToolConfig),
		SafetySettings:    prepareNativeSafetySettings(safetySettings),
		GenerationConfig:  prepareNativeGenerationConfig(req.GenerationConfig),
		CachedContent:     req.CachedContent,
		ServiceTier:       req.ServiceTier,
		Store:             req.Store,
	}

	return &GeminiVariables{
		Model:         config.ResolveModelName(model),
		GeminiRequest: out,
	}
}

// CalculateIdleTimeouts 经典文本公式：
// preTimeout = max(base * 2, 20s)，postTimeout = max(base * 1, 10s)。
func (s *TextStrategy) CalculateIdleTimeouts(baseSeconds int) (preTimeout, postTimeout time.Duration) {
	postTimeout = max(time.Duration(baseSeconds)*time.Second, 10*time.Second)
	preTimeout = max(time.Duration(baseSeconds*2)*time.Second, 20*time.Second)
	return preTimeout, postTimeout
}

// IsValidChunk 判定文本/思考 Chunk 是否包含有效内容或真实安全拦截。
func (s *TextStrategy) IsValidChunk(chunk *GeminiChunk) bool {
	if chunk == nil {
		return false
	}
	if chunk.PromptFeedback != nil && chunk.PromptFeedback.BlockReason != "" && chunk.PromptFeedback.BlockReason != blockReasonUnspecified {
		return true
	}
	if len(chunk.Candidates) > 0 {
		for _, cand := range chunk.Candidates {
			if cand == nil {
				continue
			}
			if cand.FinishReason == "SAFETY" {
				return true
			}
			if cand.Content != nil {
				for _, p := range cand.Content.Parts {
					if strings.TrimSpace(p.Text) != "" || p.Thought || p.FunctionCall != nil || (p.InlineData != nil && p.InlineData.Data != "") || p.FileData != nil || p.ExecutableCode != nil || p.CodeExecutionResult != nil {
						return true
					}
				}
			}
		}
	}
	return false
}

// IsValidResponse 判定非流式文本响应是否包含有效交付内容或真实安全拦截。
func (s *TextStrategy) IsValidResponse(resp *GeminiResponse) bool {
	if resp == nil {
		return false
	}
	if resp.PromptFeedback != nil && resp.PromptFeedback.BlockReason != "" && resp.PromptFeedback.BlockReason != blockReasonUnspecified {
		return true
	}
	if len(resp.Candidates) > 0 {
		for _, cand := range resp.Candidates {
			if cand == nil {
				continue
			}
			if cand.FinishReason == "SAFETY" {
				return true
			}
			if cand.Content != nil {
				for _, p := range cand.Content.Parts {
					if strings.TrimSpace(p.Text) != "" || p.Thought || p.FunctionCall != nil || (p.InlineData != nil && p.InlineData.Data != "") || p.FileData != nil || p.ExecutableCode != nil || p.CodeExecutionResult != nil {
						return true
					}
				}
			}
		}
	}
	return false
}

const blockReasonUnspecified = "BLOCKED_REASON_UNSPECIFIED"
