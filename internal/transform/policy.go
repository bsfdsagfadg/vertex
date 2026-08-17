package transform

import (
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

// 本文件是模型策略的纯函数基座：输入确定则输出确定、无副作用。
// api 层的 ModelStrategy.Enhance / Validate 复用这里的判定与归一逻辑。

// ResolveThinkingConfig 按 (默认档位, 模型) 解析应注入的 thinkingConfig。
// 客户端显式传入 thinkingConfig 时（由策略层判断）不应调用本函数，保证"客户端优先"。
func ResolveThinkingConfig(defaultLevel, model string) *ThinkingConfig {
	capability, ok := thinkingCapabilityFor(model)
	if !ok || capability.mechanism == ThinkingUnsupported {
		return nil
	}

	targetLevel := defaultLevel
	switch defaultLevel {
	case "自动":
		if capability.mechanism == ThinkingBudget {
			budget := -1
			return &ThinkingConfig{ThinkingBudget: &budget}
		}
		return nil
	case "最低", "低", "中", "高":
		// 如果设定的档位不在 capability.levels 白名单中，执行平滑降级到最近的有效最低档位
		if len(capability.levels) > 0 && !capability.levels[defaultLevel] {
			fallbackLevel := ""
			order := []string{"最低", "低", "中", "高"}
			for _, lvl := range order {
				if capability.levels[lvl] {
					fallbackLevel = lvl
					break
				}
			}
			if fallbackLevel == "" {
				return nil
			}
			targetLevel = fallbackLevel
		}
	default:
		return nil
	}

	switch capability.mechanism {
	case ThinkingLevel:
		if level, ok := levelToThinkingLevel[targetLevel]; ok {
			return &ThinkingConfig{ThinkingLevel: level}
		}
	case ThinkingBudget:
		if ratio, ok := levelToBudgetRatio[targetLevel]; ok {
			budget := capability.maxBudget * ratio / 4
			return &ThinkingConfig{ThinkingBudget: &budget}
		}
	}
	return nil
}

// NormalizeThinkingConfig 规范化并归一化 ThinkingConfig：
//  1. 大写枚举规范化：将小写/混合大小写/中文档位（如 "high", "low", "高"）统一转换为 GraphQL 全大写枚举（HIGH, MEDIUM, LOW, MINIMAL）；
//  2. 2.5 系列 Budget 模型：若客户端显式传入 integer thinkingBudget，精确透传并清空 thinkingLevel；若传入 thinkingLevel 字符串，按模型 maxBudget 自动折算为整数 thinkingBudget 并清空 thinkingLevel；
//  3. 3.x 系列 Level 模型：若客户端传入 thinkingLevel，规范化为全大写 GraphQL 枚举并清空 thinkingBudget；若误传 integer thinkingBudget，自动按数值范围折算为 Level 枚举并清空 thinkingBudget；
//  4. 不支持思考的模型或空参数：返回 nil，保证 omitempty 清理。
func NormalizeThinkingConfig(tc *ThinkingConfig, model string) *ThinkingConfig {
	if tc == nil {
		return nil
	}

	capability, ok := thinkingCapabilityFor(model)
	if !ok || capability.mechanism == ThinkingUnsupported {
		return nil
	}

	switch capability.mechanism {
	case ThinkingBudget:
		// 2.5 系列模型（Budget 机制）
		if tc.ThinkingBudget != nil {
			// 100% 精确透传客户端指定的数字预算，同时清空 thinkingLevel 避免 GraphQL Schema 错误
			tc.ThinkingLevel = ""
			return tc
		}
		if tc.ThinkingLevel != "" {
			b := parseLevelToBudget(tc.ThinkingLevel, capability.maxBudget)
			tc.ThinkingBudget = &b
			tc.ThinkingLevel = ""
			return tc
		}

	case ThinkingLevel:
		// 3.x 系列模型（Level 机制）
		if tc.ThinkingLevel != "" {
			tc.ThinkingLevel = normalizeThinkingLevelEnum(tc.ThinkingLevel)
			tc.ThinkingBudget = nil
			if tc.ThinkingLevel == "" {
				return nil
			}
			return tc
		}
		if tc.ThinkingBudget != nil {
			b := *tc.ThinkingBudget
			tc.ThinkingLevel = parseBudgetToLevelEnum(b)
			tc.ThinkingBudget = nil
			if tc.ThinkingLevel == "" {
				return nil
			}
			return tc
		}
	}

	if tc.ThinkingBudget == nil && tc.ThinkingLevel == "" {
		return nil
	}
	return tc
}

// normalizeThinkingLevelEnum 把任意 level 字符串归一化为 GraphQL 全大写枚举 (HIGH, MEDIUM, LOW, MINIMAL, OFF)。
func normalizeThinkingLevelEnum(level string) string {
	s := strings.TrimSpace(level)
	if s == "" {
		return ""
	}
	upper := strings.ToUpper(s)
	switch upper {
	case "HIGH", "XHIGH", "高":
		return "HIGH"
	case "MEDIUM", "MED", "中":
		return "MEDIUM"
	case "LOW", "低":
		return "LOW"
	case "MINIMAL", "MIN", "最低":
		return "MINIMAL"
	case "OFF", "NONE", "DISABLED":
		return "OFF"
	case "AUTO", "AUTOMATIC", "自动":
		return ""
	default:
		return upper
	}
}

// parseLevelToBudget 把 level 字符串映射到 2.5 模型的 integer budget。
func parseLevelToBudget(level string, maxBudget int) int {
	enum := normalizeThinkingLevelEnum(level)
	if maxBudget <= 0 {
		maxBudget = 32768
	}
	switch enum {
	case "HIGH":
		return maxBudget
	case "MEDIUM":
		return maxBudget * 3 / 4
	case "LOW":
		return maxBudget * 2 / 4
	case "MINIMAL":
		return maxBudget * 1 / 4
	case "OFF":
		return 0
	default:
		return -1
	}
}

// parseBudgetToLevelEnum 把 integer budget 映射到 3.x 模型的 level 枚举。
func parseBudgetToLevelEnum(budget int) string {
	if budget > 20000 {
		return "HIGH"
	}
	if budget > 10000 {
		return "MEDIUM"
	}
	if budget > 0 {
		return "LOW"
	}
	if budget == 0 {
		return "MINIMAL"
	}
	return ""
}

// fixedSafetySettings4OFF 返回固定 4 类别 × OFF 的安全设置基座（单一事实源）。
// 类别与顺序严格对齐官方清单（logs/LLM模型参数清单.txt / logs/image模型参数清单.txt）：
// HATE_SPEECH → DANGEROUS_CONTENT → SEXUALLY_EXPLICIT → HARASSMENT。
// 固定列表已为大写规范枚举，无需再经 prepareNativeSafetySettings 规范化。
func fixedSafetySettings4OFF() []SafetySetting {
	return []SafetySetting{
		{Category: "HARM_CATEGORY_HATE_SPEECH", Threshold: "OFF"},
		{Category: "HARM_CATEGORY_DANGEROUS_CONTENT", Threshold: "OFF"},
		{Category: "HARM_CATEGORY_SEXUALLY_EXPLICIT", Threshold: "OFF"},
		{Category: "HARM_CATEGORY_HARASSMENT", Threshold: "OFF"},
	}
}

// BuildSafetySettingsTyped 返回固定 4×OFF 安全设置列表。
// 忽略 cfg 参数（config.json 的 safety_settings 为死代码，无控制台入口）与客户端传入，
// 三家族 BuildVariables 统一经此取固定列表。
func BuildSafetySettingsTyped(cfg config.ConfigProvider) []SafetySetting {
	_ = cfg
	return fixedSafetySettings4OFF()
}

// ResolveResponseModalities 按默认配置解析图模型的 responseModalities；
// 非图像模型与空默认均返回 nil（不注入）。
func ResolveResponseModalities(defaultModalities, model string) []string {
	if !IsImageModel(model) {
		return nil
	}
	if defaultModalities == "仅图片" {
		return []string{"IMAGE"}
	}
	return []string{"TEXT", "IMAGE"}
}

// ThinkingCapInfo 返回模型族的思考机制与合法思考等级白名单（Gemini 枚举值）。
// allowedLevels 为空说明该模型不支持任何档位注入。mech 为 ThinkingUnsupported 时
// 即使模型已知也不应注入。
func ThinkingCapInfo(model string) (mech ThinkingMechanism, allowedLevels []string, ok bool) {
	capability, found := thinkingCapabilityFor(model)
	if !found {
		return ThinkingUnsupported, nil, false
	}
	levels := make([]string, 0, len(capability.levels))
	for lvl := range capability.levels {
		if gemLvl, mapOK := levelToThinkingLevel[lvl]; mapOK {
			levels = append(levels, gemLvl)
		}
	}
	return capability.mechanism, levels, true
}