package api

import (
	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/transform"
)

// TextStrategy 文本/思考模型家族策略。
type TextStrategy struct {
	model string
}

// Family 返回文本家族。
func (s *TextStrategy) Family() transform.ModelFamily { return transform.FamilyText }

// Enhance 注入 thinkingConfig 默认档位：
//   - 客户端显式传入的 thinkingConfig 优先（经过 NormalizeThinkingConfig 大写规范化与机制折算，不被覆盖）；
//   - 否则按 (DefaultThinkingLevel, model) 解析注入控制台默认值（自动→-1 预算 / 档位→level 或 budget）。
func (s *TextStrategy) Enhance(req *transform.GeminiRequest, cfg config.ConfigProvider) {
	gc := req.GenerationConfig
	if gc == nil {
		gc = &transform.GenerationConfig{}
		req.GenerationConfig = gc
	}
	if gc.ThinkingConfig != nil {
		gc.ThinkingConfig = transform.NormalizeThinkingConfig(gc.ThinkingConfig, s.model)
		return
	}
	if tc := transform.ResolveThinkingConfig(cfg.DefaultThinkingLevel(), s.model); tc != nil {
		gc.ThinkingConfig = tc
	}
}

// Validate 文本家族无跨家族载荷硬约束。
func (s *TextStrategy) Validate(req *transform.GeminiRequest) error { return nil }

// Prepare 文本家族无特化数据清洗逻辑。
func (s *TextStrategy) Prepare(req *transform.GeminiRequest) {}