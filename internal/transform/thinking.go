package transform

import (
	"log"
	"sync"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

// thinkingLevel* 是 Gemini thinkingConfig.thinkingLevel 的枚举常量，
// 供 levelToThinkingLevel、reasoningEffortToThinkingLevel 与 request.go 默认值共同引用，
// 避免多个入口对同一枚举空间各自硬编码造成漂移。
const (
	thinkingLevelNone   = "NONE"
	thinkingLevelMinimal = "MINIMAL"
	thinkingLevelLow     = "LOW"
	thinkingLevelMedium  = "MEDIUM"
	thinkingLevelHigh    = "HIGH"
)

// 档位消费键集必须与 config.ThinkingLevelOptions 同步（去掉"自动"档）。
// 漂移会在启动/测试时直接暴露，而不是静默走 default 分支。
func init() {
	concreteLevels := []string{"最低", "低", "中", "高"}
	expected := make(map[string]bool, len(concreteLevels))
	for _, level := range concreteLevels {
		expected[level] = true
	}
	defined := make(map[string]bool, len(concreteLevels))
	for _, level := range config.ThinkingLevelOptions {
		if level != "自动" {
			defined[level] = true
		}
	}
	for level := range expected {
		if !defined[level] {
			panic("transform: 思考档位消费键集与 config.ThinkingLevelOptions 不同步，缺少 " + level)
		}
	}
	for level := range defined {
		if !expected[level] {
			panic("transform: config.ThinkingLevelOptions 含未知档位 " + level)
		}
	}
	// 档位消费映射（levelToThinkingLevel/levelToBudgetRatio）键集必须与期望档位完全一致。
	for level := range levelToThinkingLevel {
		if !expected[level] {
			panic("transform: 档位映射含未知档位 " + level)
		}
	}
	for level := range expected {
		if _, ok := levelToThinkingLevel[level]; !ok {
			panic("transform: 档位映射缺少 " + level)
		}
		if _, ok := levelToBudgetRatio[level]; !ok {
			panic("transform: 档位映射缺少 " + level)
		}
	}
}

type ThinkingMechanism int

const (
	ThinkingUnsupported ThinkingMechanism = iota
	ThinkingLevel
	ThinkingBudget
)

type thinkingCapability struct {
	mechanism ThinkingMechanism
	maxBudget int
	levels    map[string]bool
}

var modelThinkingCapabilities = map[string]thinkingCapability{
	"gemini-2.5-pro": {
		mechanism: ThinkingBudget,
		maxBudget: 32768,
	},
	"gemini-2.5-flash": {
		mechanism: ThinkingBudget,
		maxBudget: 24576,
	},
	"gemini-2.5-flash-lite": {
		mechanism: ThinkingBudget,
		maxBudget: 24576,
	},
	"gemini-3-flash-preview": {
		mechanism: ThinkingLevel,
	},
	"gemini-3.1-flash-lite": {
		mechanism: ThinkingLevel,
	},
	"gemini-3.1-pro-preview": {
		mechanism: ThinkingLevel,
	},
	"gemini-3.5-flash": {
		mechanism: ThinkingLevel,
	},
	"gemini-3.5-flash-lite": {
		mechanism: ThinkingLevel,
	},
	"gemini-3.6-flash": {
		mechanism: ThinkingLevel,
	},
	"gemini-3.1-flash-image": {
		mechanism: ThinkingLevel,
		levels: map[string]bool{"最低": true, "高": true},
	},
	"gemini-3.1-flash-lite-image": {
		mechanism: ThinkingLevel,
		levels: map[string]bool{"最低": true, "高": true},
	},
	"gemini-2.5-flash-image": {
		mechanism: ThinkingUnsupported,
	},
	"gemini-3-pro-image": {
		mechanism: ThinkingUnsupported,
	},
	"gemini-3.1-flash-tts-preview": {
		mechanism: ThinkingUnsupported,
	},
}

var unknownThinkingModelWarned sync.Map

var levelToThinkingLevel = map[string]string{
	"最低": thinkingLevelMinimal,
	"低":   thinkingLevelLow,
	"中":   thinkingLevelMedium,
	"高":   thinkingLevelHigh,
}

var levelToBudgetRatio = map[string]int{
	"最低": 1,
	"低":  2,
	"中":  3,
	"高":  4,
}

func thinkingCapabilityFor(model string) (thinkingCapability, bool) {
	if c, ok := modelThinkingCapabilities[model]; ok {
		return c, true
	}
	if _, loaded := unknownThinkingModelWarned.LoadOrStore(model, true); !loaded {
		log.Printf("[Thinking] 未知模型 %q，不支持思考能力注入", model)
	}
	return thinkingCapability{}, false
}

func ApplyDefaultThinking(geminiPayload map[string]any, defaultLevel, model string) {
	genCfg, ok := geminiPayload["generationConfig"].(map[string]any)
	if ok {
		if _, has := genCfg["thinkingConfig"]; has {
			return
		}
	}

	cap, ok := thinkingCapabilityFor(model)
	if !ok {
		return
	}

	switch defaultLevel {
	case "自动":
		if cap.mechanism == ThinkingBudget {
			ensureGenCfg(geminiPayload)["thinkingConfig"] = map[string]any{"thinkingBudget": -1}
		}
		return
	case "最低", "低", "中", "高":
		if cap.levels != nil && !cap.levels[defaultLevel] {
			return
		}
	default:
		return
	}

	switch cap.mechanism {
	case ThinkingLevel:
		if level, ok := levelToThinkingLevel[defaultLevel]; ok {
			ensureGenCfg(geminiPayload)["thinkingConfig"] = map[string]any{"thinkingLevel": level}
		}
	case ThinkingBudget:
		if ratio, ok := levelToBudgetRatio[defaultLevel]; ok {
			budget := cap.maxBudget * ratio / 4
			ensureGenCfg(geminiPayload)["thinkingConfig"] = map[string]any{"thinkingBudget": budget}
		}
	}
}
