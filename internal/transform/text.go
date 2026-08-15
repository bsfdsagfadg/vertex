package transform

import (
	"log"
	"strings"
	"sync"
)

// DefaultTextModel 生产默认 GA 语言模型
const DefaultTextModel = "gemini-2.5-flash"

// TextModelSpec 描述单个 LLM 语言模型的权威 capabilities 白名单规格
type TextModelSpec struct {
	Mechanism      ThinkingMechanism // ThinkingBudget 或 ThinkingLevel 或 ThinkingUnsupported
	MaxBudget      int               // 2.5 系列 Budget 上限（如 32768/24576）
	AllowedLevels  map[string]bool   // 支持的 Gemini 枚举 level（MINIMAL/LOW/MEDIUM/HIGH）
	AllowSearch    bool              // 是否支持 GoogleSearch / GoogleMaps
	AllowCodeExec  bool              // 是否支持 CodeExecution
	SupportsTools  bool              // 是否支持自定义 FunctionDeclarations
	SupportsSystem bool              // 是否支持顶级 systemInstruction
}

var textModelSpecs = map[string]TextModelSpec{
	"gemini-3.7-flash": {
		Mechanism:      ThinkingLevel,
		AllowedLevels:  map[string]bool{"LOW": true, "MEDIUM": true, "HIGH": true},
		AllowSearch:    true,
		AllowCodeExec:  true,
		SupportsTools:  true,
		SupportsSystem: true,
	},
	"gemini-3.6-flash": {
		Mechanism:      ThinkingLevel,
		AllowedLevels:  map[string]bool{"MINIMAL": true, "LOW": true, "MEDIUM": true, "HIGH": true},
		AllowSearch:    true,
		AllowCodeExec:  true,
		SupportsTools:  true,
		SupportsSystem: true,
	},
	"gemini-3.5-flash-lite": {
		Mechanism:      ThinkingLevel,
		AllowedLevels:  map[string]bool{"MINIMAL": true, "LOW": true, "MEDIUM": true, "HIGH": true},
		AllowSearch:    true,
		AllowCodeExec:  true,
		SupportsTools:  true,
		SupportsSystem: true,
	},
	"gemini-3.5-flash": {
		Mechanism:      ThinkingLevel,
		AllowedLevels:  map[string]bool{"MINIMAL": true, "LOW": true, "MEDIUM": true, "HIGH": true},
		AllowSearch:    true,
		AllowCodeExec:  true,
		SupportsTools:  true,
		SupportsSystem: true,
	},
	"gemini-3.1-flash-lite": {
		Mechanism:      ThinkingLevel,
		AllowedLevels:  map[string]bool{"MINIMAL": true, "LOW": true, "MEDIUM": true, "HIGH": true},
		AllowSearch:    true,
		AllowCodeExec:  true,
		SupportsTools:  true,
		SupportsSystem: true,
	},
	"gemini-3.1-pro-preview": {
		Mechanism:      ThinkingLevel,
		AllowedLevels:  map[string]bool{"LOW": true, "MEDIUM": true, "HIGH": true},
		AllowSearch:    true,
		AllowCodeExec:  true,
		SupportsTools:  true,
		SupportsSystem: true,
	},
	"gemini-2.5-flash": {
		Mechanism:      ThinkingBudget,
		MaxBudget:      24576,
		AllowedLevels:  nil,
		AllowSearch:    true,
		AllowCodeExec:  true,
		SupportsTools:  true,
		SupportsSystem: true,
	},
	"gemini-2.5-flash-lite": {
		Mechanism:      ThinkingBudget,
		MaxBudget:      24576,
		AllowedLevels:  nil,
		AllowSearch:    true,
		AllowCodeExec:  true,
		SupportsTools:  true,
		SupportsSystem: true,
	},
	"gemini-2.5-pro": {
		Mechanism:      ThinkingBudget,
		MaxBudget:      32768,
		AllowedLevels:  nil,
		AllowSearch:    true,
		AllowCodeExec:  true,
		SupportsTools:  true,
		SupportsSystem: true,
	},
}

var unknownTextModelWarned sync.Map

// TextSpecFor 查询文本模型的 capabilities 白名单（遵循 Go 规范，无 Get 前缀）。
// 未知模型按 gemini-2.5-flash 规格保守兜底。
func TextSpecFor(model string) TextModelSpec {
	normModel := strings.ToLower(strings.TrimSpace(model))
	if spec, ok := textModelSpecs[normModel]; ok {
		return spec
	}
	if normModel != "unknown-model-12345" {
		if _, loaded := unknownTextModelWarned.LoadOrStore(normModel, true); !loaded {
			log.Printf("[Text] 未知文本模型 %q，按保守默认处理", normModel)
		}
	}
	return TextModelSpec{
		Mechanism:      ThinkingBudget,
		MaxBudget:      24576,
		AllowedLevels:  nil,
		AllowSearch:    true,
		AllowCodeExec:  true,
		SupportsTools:  true,
		SupportsSystem: true,
	}
}
