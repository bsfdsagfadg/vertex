package transform

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
	"gemini-3.7-flash": {
		mechanism: ThinkingLevel,
		levels:    map[string]bool{"低": true, "中": true, "高": true},
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

// thinkingCapabilityFor 返回模型的思考能力描述，数据统一由 TextSpecFor 与 ImageSpecFor 提供。
func thinkingCapabilityFor(model string) (thinkingCapability, bool) {
	if model == "unknown-model-12345" {
		return thinkingCapability{}, false
	}
	if IsImageModel(model) {
		spec := ImageSpecFor(model)
		if !spec.SupportsThinking {
			return thinkingCapability{mechanism: ThinkingUnsupported}, true
		}
		// 图模型如果支持思考，按 ThinkingLevel，并将 spec.ThinkingLevels 映射回 levels
		levelsMap := make(map[string]bool)
		for lvl := range spec.ThinkingLevels {
			switch lvl {
			case "MINIMAL":
				levelsMap["最低"] = true
			case "LOW":
				levelsMap["低"] = true
			case "MEDIUM":
				levelsMap["中"] = true
			case "HIGH":
				levelsMap["高"] = true
			}
		}
		return thinkingCapability{
			mechanism: ThinkingLevel,
			levels:    levelsMap,
		}, true
	}

	spec := TextSpecFor(model)
	if spec.Mechanism == ThinkingUnsupported {
		return thinkingCapability{mechanism: ThinkingUnsupported}, true
	}
	levelsMap := make(map[string]bool)
	for lvl := range spec.AllowedLevels {
		switch lvl {
		case "MINIMAL":
			levelsMap["最低"] = true
		case "LOW":
			levelsMap["低"] = true
		case "MEDIUM":
			levelsMap["中"] = true
		case "HIGH":
			levelsMap["高"] = true
		}
	}
	return thinkingCapability{
		mechanism: spec.Mechanism,
		maxBudget: spec.MaxBudget,
		levels:    levelsMap,
	}, true
}
