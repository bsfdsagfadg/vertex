package transform

import (
	"log"
	"sync"
)

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
	"最低": "MINIMAL",
	"低":   "LOW",
	"中":   "MEDIUM",
	"高":   "HIGH",
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
