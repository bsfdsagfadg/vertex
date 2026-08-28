package transform

import (
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

// modelCapabilityTable is intentionally separate from the dynamic model
// registry. It only describes request fields whose bounds are known for the
// canonical upstream models; unknown models are passed through unchanged.
type modelCapability struct {
	temperature bool
	topP        bool
	maxTokens   bool
}

var modelCapabilityTable = map[string]modelCapability{
	"gemini-2.5-flash":       {temperature: true, topP: true, maxTokens: true},
	"gemini-2.5-flash-lite":  {temperature: true, topP: true, maxTokens: true},
	"gemini-2.5-pro":         {temperature: true, topP: true, maxTokens: true},
	"gemini-3-flash-preview": {temperature: true, topP: true, maxTokens: true},
	"gemini-3.1-flash-lite":  {temperature: true, topP: true, maxTokens: true},
	"gemini-3.1-pro-preview": {temperature: true, topP: true, maxTokens: true},
	"gemini-3.5-flash":       {temperature: true, topP: true, maxTokens: true},
	"gemini-3.5-flash-lite":  {temperature: true, topP: true, maxTokens: true},
	"gemini-3.6-flash":       {temperature: true, topP: true, maxTokens: true},
}

// ApplyModelCapabilities clamps only explicitly supplied values for known
// canonical models. It never injects defaults and leaves unknown models
// untouched for forward compatibility.
func ApplyModelCapabilities(payload map[string]any, model string) {
	canonical := config.ResolveModelName(strings.TrimSpace(model))
	if canonical == "" {
		canonical = model
	}
	capability, ok := modelCapabilityTable[strings.ToLower(strings.TrimSpace(canonical))]
	if !ok {
		return
	}
	gen, ok := payload["generationConfig"].(map[string]any)
	if !ok {
		return
	}
	if capability.temperature {
		if v, exists := gen["temperature"]; exists {
			gen["temperature"] = clampFloat(v, 0, 2)
		}
	}
	if capability.topP {
		if v, exists := gen["topP"]; exists {
			gen["topP"] = clampFloat(v, 0, 1)
		}
	}
	if capability.maxTokens {
		if v, exists := gen["maxOutputTokens"]; exists {
			gen["maxOutputTokens"] = clampInt(v, 1, 65536)
		}
	}
}

func clampFloat(v any, min, max float64) any {
	var n float64
	switch x := v.(type) {
	case float64:
		n = x
	case float32:
		n = float64(x)
	case int:
		n = float64(x)
	default:
		return v
	}
	if n < min {
		n = min
	}
	if n > max {
		n = max
	}
	return n
}

func clampInt(v any, min, max int) any {
	var n int
	switch x := v.(type) {
	case float64:
		n = int(x)
	case int:
		n = x
	case int64:
		n = int(x)
	default:
		return v
	}
	if n < min {
		n = min
	}
	if n > max {
		n = max
	}
	return n
}
