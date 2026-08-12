package transform

import (
	"fmt"
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
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

// Enhance 唯一注入 imageConfig / responseModalities / thinkingConfig：
//   - imageSize：客户端未设置时用默认档位（按能力回退 1K）；
//   - responseModalities：客户端未设置时按默认配置（["TEXT","IMAGE"] 或 ["IMAGE"]）；
//   - thinkingConfig：客户端未显式传入时按能力白名单解析（不支持模型返回 nil）。
func (s *ImageStrategy) Enhance(req *GeminiRequest, cfg config.ConfigProvider) {
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
		gc.ThinkingConfig = NormalizeThinkingConfig(gc.ThinkingConfig, s.model)
	}

	if len(req.SafetySettings) == 0 && cfg != nil {
		req.SafetySettings = BuildSafetySettingsTyped(cfg)
	}
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

// Prepare 图像家族特化数据清洗：对 SafetySettings 进行 Trim+Upper 规范化，并独占剥离 JAILBREAK 与 CIVIC_INTEGRITY。
func (s *ImageStrategy) Prepare(req *GeminiRequest) {
	if req.SafetySettings == nil {
		return
	}
	cleaned := make([]SafetySetting, 0, len(req.SafetySettings))
	for _, ss := range req.SafetySettings {
		cat := strings.ToUpper(strings.TrimSpace(ss.Category))
		if cat != "HARM_CATEGORY_JAILBREAK" && cat != "HARM_CATEGORY_CIVIC_INTEGRITY" {
			cleaned = append(cleaned, SafetySetting{
				Category:  cat,
				Threshold: strings.ToUpper(strings.TrimSpace(ss.Threshold)),
			})
		}
	}
	req.SafetySettings = cleaned
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
	if resp.PromptFeedback != nil && resp.PromptFeedback.BlockReason != "" && resp.PromptFeedback.BlockReason != blockReasonUnspecified {
		return true
	}
	if len(resp.Candidates) > 0 {
		for _, cand := range resp.Candidates {
			if cand != nil && cand.FinishReason == "SAFETY" {
				return true
			}
		}
	}
	// 提取出至少 1 张图像
	imgs := ExtractImagesTyped(resp)
	return len(imgs) > 0
}
