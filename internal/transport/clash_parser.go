package transport

import (
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ParseClashYAML parses a single Clash proxy configuration from YAML bytes.
func ParseClashYAML(data []byte) (map[string]any, error) {
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("clash yaml unmarshal error: %w", err)
	}
	result := NormalizeYAMLMap(raw)
	if len(result) == 0 {
		return nil, fmt.Errorf("clash yaml parsed to empty proxy map")
	}
	return result, nil
}

// ParseClashYAMLProxies parses a list of proxies from YAML containing a top-level `proxies:` key or a list.
func ParseClashYAMLProxies(data []byte) ([]map[string]any, error) {
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("clash yaml unmarshal error: %w", err)
	}

	normalized := NormalizeYAMLValue(raw)
	if list, ok := normalized.([]any); ok {
		var proxies []map[string]any
		for _, item := range list {
			if m, ok := item.(map[string]any); ok && len(m) > 0 {
				proxies = append(proxies, m)
			}
		}
		return proxies, nil
	}

	if m, ok := normalized.(map[string]any); ok {
		if rawProxies, ok := m["proxies"]; ok {
			if list, ok := rawProxies.([]any); ok {
				var proxies []map[string]any
				for _, item := range list {
					if itemMap, ok := item.(map[string]any); ok && len(itemMap) > 0 {
						proxies = append(proxies, itemMap)
					}
				}
				return proxies, nil
			}
		}
		// If it's a single proxy object with a "type" field
		if _, hasType := m["type"]; hasType {
			return []map[string]any{m}, nil
		}
	}

	return nil, fmt.Errorf("no proxies found in clash yaml")
}

// ParseClashInline parses an inline YAML proxy string (e.g. `{name: "foo", type: vmess, ...}`)
// using gopkg.in/yaml.v3 instead of handwritten string chopping.
func ParseClashInline(s string) (map[string]any, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return map[string]any{}, nil
	}

	// Try standard YAML unmarshal
	var raw any
	if err := yaml.Unmarshal([]byte(trimmed), &raw); err != nil {
		// If it failed and didn't have enclosing braces, try wrapping with braces
		if !strings.HasPrefix(trimmed, "{") && !strings.HasSuffix(trimmed, "}") {
			if err2 := yaml.Unmarshal([]byte("{"+trimmed+"}"), &raw); err2 == nil {
				return NormalizeYAMLMap(raw), nil
			}
		}
		// Also try JSON unmarshal as fallback
		var jsonMap map[string]any
		if jsonErr := json.Unmarshal([]byte(trimmed), &jsonMap); jsonErr == nil {
			return jsonMap, nil
		}
		return nil, fmt.Errorf("parse inline clash yaml: %w", err)
	}

	return NormalizeYAMLMap(raw), nil
}

// NormalizeYAMLMap converts any map structure into map[string]any.
func NormalizeYAMLMap(val any) map[string]any {
	normalized := NormalizeYAMLValue(val)
	if m, ok := normalized.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

// NormalizeYAMLValue recursively converts map[any]any and sub-slices to standard Go types.
func NormalizeYAMLValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			out[k] = NormalizeYAMLValue(item)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			out[fmt.Sprintf("%v", k)] = NormalizeYAMLValue(item)
		}
		return out
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, NormalizeYAMLValue(item))
		}
		return out
	default:
		return value
	}
}
