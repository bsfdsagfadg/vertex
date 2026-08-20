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

var levelToThinkingLevel = map[string]string{
	"最低": "MINIMAL",
	"低":  "LOW",
	"中":  "MEDIUM",
	"高":  "HIGH",
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
