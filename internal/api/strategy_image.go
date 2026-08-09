package api

import (
	"fmt"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/transform"
)

// ImageStrategy 图模型家族策略。
//
// 关键硬约束：图载荷的 imageSize / aspectRatio 必须按模型能力矩阵过滤；
// thinkingConfig 仅允许模型能力白名单内的档位（2.5-flash-image / 3-pro-image
// 结构上不生成 thinkingConfig）。
type ImageStrategy struct {
	model string
}

// Family 返回图像家族。
func (s *ImageStrategy) Family() transform.ModelFamily { return transform.FamilyImage }

// Enhance 唯一注入 imageConfig / responseModalities / thinkingConfig：
//   - imageSize：客户端未设置时用默认档位（按能力回退 1K）；
//   - responseModalities：客户端未设置时按默认配置（["TEXT","IMAGE"] 或 ["IMAGE"]）；
//   - thinkingConfig：客户端未显式传入时按能力白名单解析（不支持模型返回 nil）。
func (s *ImageStrategy) Enhance(req *transform.GeminiRequest, cfg config.ConfigProvider) {
	gc := req.GenerationConfig
	if gc == nil {
		gc = &transform.GenerationConfig{}
		req.GenerationConfig = gc
	}

	ic := gc.ImageConfig
	if gc.ImageConfig == nil {
		ic = &transform.ImageConfig{}
		gc.ImageConfig = ic
	}
	if ic.ImageSize == "" {
		ic.ImageSize = transform.ResolveImageSize(cfg.DefaultImageSize(), s.model)
	}

	if len(gc.ResponseModalities) == 0 {
		if mods := transform.ResolveResponseModalities(cfg.DefaultResponseModalities(), s.model); mods != nil {
			gc.ResponseModalities = mods
		}
	}

	if gc.ThinkingConfig != nil {
		gc.ThinkingConfig = transform.NormalizeThinkingConfig(gc.ThinkingConfig, s.model)
	} else {
		if tc := transform.ResolveThinkingConfig(cfg.DefaultThinkingLevel(), s.model); tc != nil {
			gc.ThinkingConfig = tc
		}
	}
}

// Validate 校验图载荷的 thinkingConfig 合法性。
func (s *ImageStrategy) Validate(req *transform.GeminiRequest) error {
	gc := req.GenerationConfig
	if gc == nil || gc.ThinkingConfig == nil {
		return nil
	}
	mech, allowed, known := transform.ThinkingCapInfo(s.model)
	if !known || mech == transform.ThinkingUnsupported {
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