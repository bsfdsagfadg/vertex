package transform

import (
	"fmt"
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

// Validate 结合 TextModelSpec 进行强约束拦截：
//   - 若模型属于 ThinkingUnsupported 且客户端显式传入 thinkingConfig 时抛出 400 Invalid Argument；
//   - 若模型属于 ThinkingLevel 机制，校验档位是否在 Spec 准许列表（如 HIGH/MEDIUM/LOW/MINIMAL）；
//   - 校验工具透传约束。
func (s *TextStrategy) Validate(req *GeminiRequest) error {
	if req == nil {
		return nil
	}
	spec := TextSpecFor(s.model)

	if req.GenerationConfig != nil && req.GenerationConfig.ThinkingConfig != nil {
		tc := req.GenerationConfig.ThinkingConfig
		if spec.Mechanism == ThinkingUnsupported {
			return fmt.Errorf("thinkingConfig is not supported for model %s", s.model)
		}
		if spec.Mechanism == ThinkingLevel && tc.ThinkingLevel != "" {
			level := strings.ToUpper(strings.TrimSpace(tc.ThinkingLevel))
			if len(spec.AllowedLevels) > 0 && !spec.AllowedLevels[level] {
				return fmt.Errorf("invalid thinking level %s for model %s", level, s.model)
			}
		}
	}

	if (!spec.SupportsTools) && (len(req.Tools) > 0 || req.ToolConfig != nil) {
		return fmt.Errorf("tools and toolConfig are not supported for model %s", s.model)
	}

	return nil
}

// Prepare 特化清洗：SafetySettings 已由 BuildVariables 统一覆盖为固定 4×OFF，
// 本方法保留为空实现（签名兼容既有调用点，零破坏）。
func (s *TextStrategy) Prepare(req *GeminiRequest) {}

// applyTextSamplingParams 文本家族单点采样参数处理（模型感知，纯函数）：
//   - maxOutputTokens：drop_max_tokens → nil；未传 → 注入 spec.MaxOutputTokens；超上限 → clamp；
//   - temperature：不支持模型 → nil；未传 → 注入 spec.DefaultTemperature；越界 → clamp [0, MaxTemperature]；
//   - topP：不支持模型 → nil；未传 → 注入 spec.DefaultTopP；越界 → clamp [0, 1.0]。
//
// 只允许在 TextStrategy.BuildVariables 中调用（Enhance 存在 thinkingConfig early-return 陷阱，
// 放在 Enhance 会跳过注入）。不触碰 ThinkingConfig / MediaResolution / ResponseModalities（归一仍归 prepareNativeGenerationConfig）。
func applyTextSamplingParams(gc *GenerationConfig, spec TextModelSpec, cfg config.ConfigProvider) *GenerationConfig {
	if gc == nil {
		gc = &GenerationConfig{}
	}

	if cfg != nil && cfg.DropMaxTokens() {
		gc.MaxOutputTokens = nil
	} else if gc.MaxOutputTokens == nil {
		gc.MaxOutputTokens = &spec.MaxOutputTokens
	} else if *gc.MaxOutputTokens > spec.MaxOutputTokens {
		clamped := spec.MaxOutputTokens
		gc.MaxOutputTokens = &clamped
	}

	if !spec.AllowTemperature {
		gc.Temperature = nil
	} else if gc.Temperature == nil && spec.DefaultTemperature != nil {
		gc.Temperature = spec.DefaultTemperature
	} else if gc.Temperature != nil {
		t := *gc.Temperature
		if t < 0 {
			t = 0
		} else if t > spec.MaxTemperature {
			t = spec.MaxTemperature
		}
		gc.Temperature = &t
	}

	if !spec.AllowTopP {
		gc.TopP = nil
	} else if gc.TopP == nil && spec.DefaultTopP != nil {
		gc.TopP = spec.DefaultTopP
	} else if gc.TopP != nil {
		p := *gc.TopP
		if p < 0 {
			p = 0
		} else if p > 1.0 {
			p = 1.0
		}
		gc.TopP = &p
	}

	return gc
}

// bindSearchAndMapsTools 文本家族搜索/地图工具追加绑定（纯函数）：
// 客户端传任意 googleSearch / googleMaps 工具 → 在原工具数组（含 functionDeclarations 等）基础上
// 追加去重后的 [googleSearch, googleMaps]（追加共存，非替换）；未触发则原样返回。
// 客户端重复的 googleSearch / googleMaps 标记会被折叠（仅保留首个，其它字段如 functionDeclarations 不受影响）。
// googleSearchRetrieval 不计入触发条件，也不参与追加/删除（透传保留）。
// 约束：不修改原数组元素（range 拷贝上折叠标记），追加项仅空结构标记，不复制客户端字段。
func bindSearchAndMapsTools(tools []Tool) []Tool {
	if len(tools) == 0 {
		return tools
	}
	triggered := false
	for _, t := range tools {
		if t.GoogleSearch != nil || t.GoogleMaps != nil {
			triggered = true
			break
		}
	}
	if !triggered {
		return tools
	}

	out := make([]Tool, 0, len(tools)+2)
	hasGS, hasGM := false, false
	for _, t := range tools {
		if t.GoogleSearch != nil {
			if hasGS {
				t.GoogleSearch = nil
			} else {
				hasGS = true
			}
		}
		if t.GoogleMaps != nil {
			if hasGM {
				t.GoogleMaps = nil
			} else {
				hasGM = true
			}
		}
		// 折叠后为空壳（纯重复标记）直接跳过，避免输出空工具
		if t.GoogleSearch == nil && t.GoogleMaps == nil &&
			t.FunctionDeclarations == nil && t.GoogleSearchRetrieval == nil &&
			t.CodeExecution == nil && t.Retrieval == nil &&
			t.URLContext == nil && t.ComputerUse == nil &&
			t.MCPTool == nil && t.FileSearch == nil {
			continue
		}
		out = append(out, t)
	}
	if !hasGS {
		out = append(out, Tool{GoogleSearch: GoogleSearch{}})
	}
	if !hasGM {
		out = append(out, Tool{GoogleMaps: GoogleMaps{}})
	}
	return out
}

// BuildVariables 实现文本家族独占的上行 variables 构建：
// 保留同 role 合并、历史思维链签名注入、TrailingModelFix、系统指令 fallback 等所有语言模型核心增强。
func (s *TextStrategy) BuildVariables(model string, req *GeminiRequest, cfg config.ConfigProvider) *GeminiVariables {
	if req == nil {
		req = &GeminiRequest{}
	}

	spec := TextSpecFor(model)

	contents := sanitizeContentRolesTyped(req.Contents)
	contents = mergeContiguousRolesTyped(contents)
	contents = filterEmptyContentsTyped(contents)
	NewSignatureResolver().ApplyContents(contents)
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
		Tools:             prepareNativeTools(bindSearchAndMapsTools(req.Tools)),
		ToolConfig:        prepareNativeToolConfig(req.ToolConfig),
		SafetySettings:    BuildSafetySettingsTyped(cfg),
		GenerationConfig:  prepareNativeGenerationConfig(applyTextSamplingParams(req.GenerationConfig, spec, cfg)),
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
