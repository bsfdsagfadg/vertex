package transform

import (
	"fmt"
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/infra/config"
)

// ImageStrategy 图模型家族策略。
//
// 关键硬约束：图载荷的 imageSize / aspectRatio 必须按模型能力矩阵过滤；
// thinkingConfig 仅允许模型能力白名单内的档位。
type ImageStrategy struct {
	model string
}

// Family 返回图像家族。
func (s *ImageStrategy) Family() ModelFamily { return FamilyImage }

// FamilyStreamMode 图像家族默认聚合单包流（降级保护）。
func (s *ImageStrategy) FamilyStreamMode() StreamMode { return StreamModeAggregate }

// Enhance 唯一注入 imageConfig / responseModalities / thinkingConfig：
//   - imageSize：客户端未设置时用默认档位（按能力回退 1K）；
//   - responseModalities：客户端未设置时按默认配置（["TEXT","IMAGE"] 或 ["IMAGE"]）；
//   - thinkingConfig：客户端显式传入时归一化并按白名单平滑降级（越界档位降级、
//     OFF/NONE 按不发送处理）；未传入时按 (DefaultThinkingLevel, model) 解析注入控制台默认
//     （自动=不发送，对齐文本家族）。
//
// 本方法只做"默认值填充 + 归一/降级"，白名单清洗（Tools/SafetySettings）统一由 BuildVariables 唯一处理。
func (s *ImageStrategy) Enhance(req *GeminiRequest, cfg config.ConfigProvider) {
	spec := GetImageModelSpec(s.model)

	gc := req.GenerationConfig
	if gc == nil {
		gc = &GenerationConfig{}
		req.GenerationConfig = gc
	}

	ic := gc.ImageConfig
	if gc.ImageConfig == nil {
		ic = &ImageConfig{}
		gc.ImageConfig = ic
	}
	if ic.ImageSize == "" {
		ic.ImageSize = ResolveImageSize(cfg.DefaultImageSize(), s.model)
	}

	if len(gc.ResponseModalities) == 0 {
		if mods := ResolveResponseModalities(cfg.DefaultResponseModalities(), s.model); mods != nil {
			gc.ResponseModalities = mods
		}
	}

	if gc.ThinkingConfig != nil {
		gc.ThinkingConfig = smoothImageThinkingConfig(NormalizeThinkingConfig(gc.ThinkingConfig, s.model), spec)
	} else if tc := ResolveThinkingConfig(cfg.DefaultThinkingLevel(), s.model); tc != nil {
		gc.ThinkingConfig = tc
	}
}

// smoothImageThinkingConfig 将已归一化的 ThinkingConfig 对齐当前图像模型白名单：
//   - 白名单内的档位（MINIMAL/HIGH 等）原样保留；
//   - 白名单外的档位（LOW/MEDIUM 等）按升序 ["MINIMAL","LOW","MEDIUM","HIGH"]
//     降级为第一个受支持的档位（图模型当前均为 MINIMAL）；
//   - OFF/NONE（关闭思考）图模型不支持，按"不发送"处理返回 nil；
//   - 白名单为空或无可降级档位时返回 nil。
//
// 前提：仅适用于 ThinkingLevel 机制模型（当前 4 个图模型均为 Level 机制），
// 返回的 ThinkingConfig 仅携带 ThinkingLevel、丢弃 ThinkingBudget。
// 纯函数、幂等，供 Enhance 与 BuildVariables 共用；仅图像家族策略调用，不影响文本/语音家族。
func smoothImageThinkingConfig(tc *ThinkingConfig, spec ImageModelSpec) *ThinkingConfig {
	if tc == nil || !spec.SupportsThinking {
		return nil
	}
	lvl := strings.ToUpper(strings.TrimSpace(tc.ThinkingLevel))
	if spec.ThinkingLevels[lvl] {
		return &ThinkingConfig{ThinkingLevel: lvl}
	}
	if lvl == "OFF" || lvl == "NONE" {
		return nil
	}
	for _, cand := range []string{"MINIMAL", "LOW", "MEDIUM", "HIGH"} {
		if spec.ThinkingLevels[cand] {
			return &ThinkingConfig{ThinkingLevel: cand}
		}
	}
	return nil
}

// filterAllowedSearchTools 过滤出合法的 GoogleSearch 工具定义（生图仅支持纯搜索）：
// 仅保留 googleSearch 字段非 nil 的 Tool，硬剥离 googleMaps / googleSearchRetrieval /
// functionDeclarations 等一切其它字段；返回全新数组。
func filterAllowedSearchTools(tools []Tool) []Tool {
	var filtered []Tool
	for _, t := range tools {
		if t.GoogleSearch != nil {
			filtered = append(filtered, Tool{GoogleSearch: t.GoogleSearch})
		}
	}
	return filtered
}

// Validate 校验图载荷的 thinkingConfig 合法性。
func (s *ImageStrategy) Validate(req *GeminiRequest) error {
	gc := req.GenerationConfig
	if gc == nil || gc.ThinkingConfig == nil {
		return nil
	}
	mech, allowed, known := ThinkingCapInfo(s.model)
	if !known || mech == ThinkingUnsupported {
		return fmt.Errorf("模型 %s 不支持注入 thinkingConfig (unsupported thinking for %s)", s.model, s.model)
	}
	if lvl := gc.ThinkingConfig.ThinkingLevel; lvl != "" {
		for _, a := range allowed {
			if a == lvl {
				return nil
			}
		}
		return fmt.Errorf("模型 %s 仅允许 thinkingLevel 为 %v，收到 %q", s.model, allowed, lvl)
	}
	return nil
}

// Prepare 图像家族无需特化清洗：SafetySettings 与 Tools 白名单统一由 BuildVariables 承担，
// 本方法保留为空实现（兼容调用点，零破坏）。
func (s *ImageStrategy) Prepare(req *GeminiRequest) {}

// BuildVariables 实现生图家族独占的上行 variables 构建：
// 从零正向构建 GeminiRequest，严格按照生图模型白名单抽取参数，彻底屏蔽语言模型的思维链签名注入与无关字段。
func (s *ImageStrategy) BuildVariables(model string, req *GeminiRequest, cfg config.ConfigProvider) *GeminiVariables {
	if req == nil {
		req = &GeminiRequest{}
	}

	spec := GetImageModelSpec(model)

	// 2. 内容部分：清理角色并过滤空内容
	contents := sanitizeContentRolesTyped(req.Contents)
	contents = filterEmptyContentsTyped(contents)

	// 3. GenerationConfig：正向构建（客户端未传 GC 也构造并注入默认采样参数，对齐官方示例）
	gc := &GenerationConfig{}
	orig := req.GenerationConfig

	// 3.1 基础超参限制：缺省注入 + 逐模型 clamp
	//     temperature (lite-image 上限 1.0，其余 2.0), topP (0~1.0), maxOutputTokens (固定上限 32768)
	if orig != nil && orig.Temperature != nil {
		t := *orig.Temperature
		if t < 0 {
			t = 0
		} else if t > spec.MaxTemperature {
			t = spec.MaxTemperature
		}
		gc.Temperature = &t
	} else {
		gc.Temperature = &spec.DefaultTemperature
	}
	if orig != nil && orig.TopP != nil {
		p := *orig.TopP
		if p < 0 {
			p = 0
		} else if p > spec.MaxTopP {
			p = spec.MaxTopP
		}
		gc.TopP = &p
	} else {
		gc.TopP = &spec.DefaultTopP
	}
	if cfg != nil && cfg.DropMaxTokens() {
		gc.MaxOutputTokens = nil
	} else if orig != nil && orig.MaxOutputTokens != nil {
		tokens := *orig.MaxOutputTokens
		if tokens > spec.MaxOutputTokens {
			tokens = spec.MaxOutputTokens
		}
		gc.MaxOutputTokens = &tokens
	} else {
		gc.MaxOutputTokens = &spec.MaxOutputTokens
	}

	if orig != nil {
		// 3.2 响应模态 (ResponseModalities)
		if len(orig.ResponseModalities) > 0 {
			modalities := make([]string, 0, len(orig.ResponseModalities))
			for _, m := range orig.ResponseModalities {
				mod := strings.ToUpper(strings.TrimSpace(m))
				if mod == "IMAGE" || mod == "TEXT" {
					modalities = append(modalities, mod)
				}
			}
			if len(modalities) > 0 {
				gc.ResponseModalities = modalities
			}
		}

		// 3.3 图像配置 (ImageConfig)：严格白名单
		if orig.ImageConfig != nil {
			origIC := orig.ImageConfig
			ic := &ImageConfig{}

			// aspectRatio 校验与回退
			if origIC.AspectRatio != "" {
				ar := strings.ToLower(strings.TrimSpace(origIC.AspectRatio))
				if spec.SupportedRatios[ar] {
					ic.AspectRatio = ar
				} else if ar == "auto" {
					// pro-image 等不支持 auto 的模型回退为 1:1
					ic.AspectRatio = "1:1"
				}
			}

			// imageSize 校验与回退
			if origIC.ImageSize != "" {
				is := strings.ToUpper(strings.TrimSpace(origIC.ImageSize))
				if spec.SupportedSizes[is] {
					ic.ImageSize = is
				} else {
					// 不支持的档位回退到 1K
					ic.ImageSize = "1K"
				}
			}

			// outputMimeType 校验
			if origIC.OutputMimeType != "" {
				mime := strings.ToLower(strings.TrimSpace(origIC.OutputMimeType))
				if spec.SupportedMimes[mime] {
					ic.OutputMimeType = mime
				}
			}

			if ic.AspectRatio != "" || ic.ImageSize != "" || ic.OutputMimeType != "" {
				gc.ImageConfig = ic
			}
		}

		// 3.4 思考配置 (ThinkingConfig)：仅支持模型正向保留（归一化 + 白名单平滑降级）
		if orig.ThinkingConfig != nil && spec.SupportsThinking {
			gc.ThinkingConfig = smoothImageThinkingConfig(NormalizeThinkingConfig(orig.ThinkingConfig, model), spec)
		}
	}

	// 4. 工具配置 (Tools / ToolConfig)：仅 AllowSearch 模型正向保留 GoogleSearch
	var tools []Tool
	if spec.AllowSearch && len(req.Tools) > 0 {
		tools = filterAllowedSearchTools(req.Tools)
	}

	out := &GeminiRequest{
		Contents:          contents,
		SystemInstruction: req.SystemInstruction,
		Tools:             tools,
		ToolConfig:        nil, // 生图不保留 FunctionCalling ToolConfig
		SafetySettings:    BuildSafetySettingsTyped(cfg),
		GenerationConfig:  gc,
		CachedContent:     req.CachedContent,
		ServiceTier:       req.ServiceTier,
		Store:             req.Store,
	}

	return &GeminiVariables{
		Model:         config.ResolveModelName(model),
		GeminiRequest: out,
	}
}

// CalculateIdleTimeouts 生图放大超时：
// preTimeout = max(base * 4, 60s)，postTimeout = max(base * 2, 30s)。
func (s *ImageStrategy) CalculateIdleTimeouts(baseSeconds int) (preTimeout, postTimeout time.Duration) {
	postTimeout = max(time.Duration(baseSeconds*2)*time.Second, 30*time.Second)
	preTimeout = max(time.Duration(baseSeconds*4)*time.Second, 60*time.Second)
	return preTimeout, postTimeout
}

// isImageTextHybridModel 甄别模型是否支持图文混合输出（flash-image 支持，pro-image/imagen 属于纯生图）。
func isImageTextHybridModel(model string) bool {
	lower := strings.ToLower(strings.TrimSpace(model))
	if strings.Contains(lower, "imagen") || strings.Contains(lower, "pro-image") {
		return false
	}
	return true
}

// IsValidChunk 判定图像 Chunk 是否包含有效 Payload 或真实安全拦截。
func (s *ImageStrategy) IsValidChunk(chunk *GeminiChunk) bool {
	if chunk == nil {
		return false
	}
	// 优先判定 Safety 拦截（真实拦截需放行，防发陷入无限对冲）
	if chunk.PromptFeedback != nil && IsBlockReason(chunk.PromptFeedback.BlockReason) {
		return true
	}
	if len(chunk.Candidates) > 0 {
		for _, cand := range chunk.Candidates {
			if cand == nil {
				continue
			}
			if IsSafetyFinishReason(cand.FinishReason) {
				return true
			}
			if cand.Content != nil {
				for _, p := range cand.Content.Parts {
					// 1. 检查包含有效数据的 InlineData（MIME 包含 image/ 或为空）
					if p.InlineData != nil && p.InlineData.Data != "" {
						mime := strings.ToLower(p.InlineData.MimeType)
						if mime == "" || strings.HasPrefix(mime, "image/") {
							return true
						}
					}
					// 2. 检查 Markdown 图片链接
					if strings.Contains(p.Text, "![") && strings.Contains(p.Text, "data:image/") {
						return true
					}
					// 3. 检查思考过程或代码/功能调用
					if p.Thought || p.FunctionCall != nil || p.ExecutableCode != nil || p.CodeExecutionResult != nil {
						return true
					}
					// 4. 仅图文混合模型放行非空文本 Part，纯生图模型拒绝纯文本 Chunk
					if strings.TrimSpace(p.Text) != "" && isImageTextHybridModel(s.model) {
						return true
					}
				}
			}
		}
	}
	return false
}

// IsValidResponse 判定非流式图像响应是否包含有效交付内容（至少有 1 张图像或安全拦截）。
func (s *ImageStrategy) IsValidResponse(resp *GeminiResponse) bool {
	if resp == nil {
		return false
	}
	if resp.PromptFeedback != nil && IsBlockReason(resp.PromptFeedback.BlockReason) {
		return true
	}
	if len(resp.Candidates) > 0 {
		for _, cand := range resp.Candidates {
			if cand != nil && IsSafetyFinishReason(cand.FinishReason) {
				return true
			}
		}
	}
	// 提取出至少 1 张图像
	imgs := ExtractImagesTyped(resp)
	return len(imgs) > 0
}
