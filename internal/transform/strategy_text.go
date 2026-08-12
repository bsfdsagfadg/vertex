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
