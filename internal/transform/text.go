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
	MaxOutputTokens    int               // 输出 token 上限（全模型 65535）
	AllowTemperature   bool              // 是否支持 temperature（3.x 部分模型不支持）
	MaxTemperature     float64           // 温度上限（仅 AllowTemperature=true 时有意义）
	DefaultTemperature *float64          // 温度缺省注入值；不支持模型为 nil
	AllowTopP          bool              // 是否支持 topP（与 AllowTemperature 同步）
	DefaultTopP        *float64          // topP 缺省注入值；不支持模型为 nil
	Mechanism          ThinkingMechanism // ThinkingBudget 或 ThinkingLevel 或 ThinkingUnsupported
	MaxBudget          int               // 2.5 系列 Budget 上限（如 32768/24576）
	AllowedLevels      map[string]bool   // 支持的 Gemini 枚举 level（MINIMAL/LOW/MEDIUM/HIGH）
	AllowSearch        bool              // 是否支持 GoogleSearch / GoogleMaps
	AllowCodeExec      bool              // 是否支持 CodeExecution
	SupportsTools      bool              // 是否支持自定义 FunctionDeclarations
	SupportsSystem     bool              // 是否支持顶级 systemInstruction
}

// f64ptr 返回 float64 字面量的指针（供规格矩阵默认注入值使用）。
func f64ptr(v float64) *float64 { return &v }

// MaxOutputTokensFor 返回文本模型的最大输出 token 上限。
func MaxOutputTokensFor(model string) int {
	return TextSpecFor(model).MaxOutputTokens
}

var textModelSpecs = map[string]TextModelSpec{
	"gemini-3.7-flash": {
		MaxOutputTokens: 65535,
		Mechanism:       ThinkingLevel,
		AllowedLevels:   map[string]bool{"LOW": true, "MEDIUM": true, "HIGH": true},
		AllowSearch:     true,
		AllowCodeExec:   true,
		SupportsTools:   true,
		SupportsSystem:  true,
	},
	"gemini-3.6-flash": {
		MaxOutputTokens: 65535,
		Mechanism:       ThinkingLevel,
		AllowedLevels:   map[string]bool{"MINIMAL": true, "LOW": true, "MEDIUM": true, "HIGH": true},
		AllowSearch:     true,
		AllowCodeExec:   true,
		SupportsTools:   true,
		SupportsSystem:  true,
	},
	"gemini-3.5-flash-lite": {
		MaxOutputTokens: 65535,
		Mechanism:       ThinkingLevel,
		AllowedLevels:   map[string]bool{"MINIMAL": true, "LOW": true, "MEDIUM": true, "HIGH": true},
		AllowSearch:     true,
		AllowCodeExec:   true,
		SupportsTools:   true,
		SupportsSystem:  true,
	},
	"gemini-3.5-flash": {
		MaxOutputTokens:    65535,
		AllowTemperature:   true,
		MaxTemperature:     2.0,
		DefaultTemperature: f64ptr(1.0),
		AllowTopP:          true,
		DefaultTopP:        f64ptr(0.95),
		Mechanism:          ThinkingLevel,
		AllowedLevels:      map[string]bool{"MINIMAL": true, "LOW": true, "MEDIUM": true, "HIGH": true},
		AllowSearch:        true,
		AllowCodeExec:      true,
		SupportsTools:      true,
		SupportsSystem:     true,
	},
	"gemini-3.1-flash-lite": {
		MaxOutputTokens:    65535,
		AllowTemperature:   true,
		MaxTemperature:     2.0,
		DefaultTemperature: f64ptr(1.0),
		AllowTopP:          true,
		DefaultTopP:        f64ptr(0.95),
		Mechanism:          ThinkingLevel,
		AllowedLevels:      map[string]bool{"MINIMAL": true, "LOW": true, "MEDIUM": true, "HIGH": true},
		AllowSearch:        true,
		AllowCodeExec:      true,
		SupportsTools:      true,
		SupportsSystem:     true,
	},
	"gemini-3.1-pro-preview": {
		MaxOutputTokens:    65535,
		AllowTemperature:   true,
		MaxTemperature:     2.0,
		DefaultTemperature: f64ptr(1.0),
		AllowTopP:          true,
		DefaultTopP:        f64ptr(0.95),
		Mechanism:          ThinkingLevel,
		AllowedLevels:      map[string]bool{"LOW": true, "MEDIUM": true, "HIGH": true},
		AllowSearch:        true,
		AllowCodeExec:      true,
		SupportsTools:      true,
		SupportsSystem:     true,
	},
	"gemini-2.5-flash": {
		MaxOutputTokens:    65535,
		AllowTemperature:   true,
		MaxTemperature:     2.0,
		DefaultTemperature: f64ptr(1.0),
		AllowTopP:          true,
		DefaultTopP:        f64ptr(1.0),
		Mechanism:          ThinkingBudget,
		MaxBudget:          24576,
		AllowedLevels:      nil,
		AllowSearch:        true,
		AllowCodeExec:      true,
		SupportsTools:      true,
		SupportsSystem:     true,
	},
	"gemini-2.5-flash-lite": {
		MaxOutputTokens:    65535,
		AllowTemperature:   true,
		MaxTemperature:     2.0,
		DefaultTemperature: f64ptr(1.0),
		AllowTopP:          true,
		DefaultTopP:        f64ptr(0.95),
		Mechanism:          ThinkingBudget,
		MaxBudget:          24576,
		AllowedLevels:      nil,
		AllowSearch:        true,
		AllowCodeExec:      true,
		SupportsTools:      true,
		SupportsSystem:     true,
	},
	"gemini-2.5-pro": {
		MaxOutputTokens:    65535,
		AllowTemperature:   true,
		MaxTemperature:     2.0,
		DefaultTemperature: f64ptr(1.0),
		AllowTopP:          true,
		DefaultTopP:        f64ptr(0.95),
		Mechanism:          ThinkingBudget,
		MaxBudget:          32768,
		AllowedLevels:      nil,
		AllowSearch:        true,
		AllowCodeExec:      true,
		SupportsTools:      true,
		SupportsSystem:     true,
	},
}

var unknownTextModelWarned sync.Map

// TextSpecFor 查询文本模型的 capabilities 白名单（遵循 Go 规范，无 Get 前缀）。
// 未知模型按 gemini-3.7-flash 规格保守兜底（Level 思考，不支持温度/TopP）。
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
		MaxOutputTokens: 65535,
		Mechanism:       ThinkingLevel,
		AllowedLevels:   map[string]bool{"LOW": true, "MEDIUM": true, "HIGH": true},
		AllowSearch:     true,
		AllowCodeExec:   true,
		SupportsTools:   true,
		SupportsSystem:  true,
	}
}
