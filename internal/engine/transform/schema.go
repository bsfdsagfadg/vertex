package transform

import (
	"strconv"
	"strings"
)

// geminiAllowedSchemaFields 是 functionDeclarations.parameters 的 JSON Schema 字段白名单。
var geminiAllowedSchemaFields = map[string]bool{ //nolint:gochecknoglobals
	"anyOf": true, "default": true, "description": true, "enum": true,
	"example": true, "format": true, "items": true,
	"maxItems": true, "maxLength": true, "maxProperties": true, "maximum": true,
	"minItems": true, "minLength": true, "minProperties": true, "minimum": true,
	"nullable": true, "pattern": true, "properties": true, "propertyOrdering": true,
	"required": true, "title": true, "type": true,
}

// schemaUnsupportedKeys 是 Vertex AI 原生 Schema 不支持、需剥离的 JSON-Schema 关键字。
var schemaUnsupportedKeys = map[string]bool{ //nolint:gochecknoglobals
	"$schema": true, "$id": true, "$defs": true, "$ref": true, "definitions": true,
	"additionalProperties": true, "patternProperties": true, "unevaluatedProperties": true,
	"dependentSchemas": true, "if": true, "then": true, "else": true,
	"allOf": true, "anyOf": true, "oneOf": true, "not": true,
	"title": true,
}

// cleanFunctionParameters 递归用 Gemini 白名单清洗 JSON Schema，剔除上游不支持的字段。
func cleanFunctionParameters(schema any) any {
	switch s := schema.(type) {
	case []any:
		out := make([]any, len(s))
		for i, item := range s {
			out[i] = cleanFunctionParameters(item)
		}
		return out
	case map[string]any:
		cleaned := map[string]any{}
		for key, value := range s {
			if !geminiAllowedSchemaFields[key] {
				continue
			}
			switch key {
			case "properties":
				if vm, ok := value.(map[string]any); ok {
					props := map[string]any{}
					for k, v := range vm {
						props[k] = cleanFunctionParameters(v)
					}
					cleaned[key] = props
					continue
				}
				cleaned[key] = value
			case "items":
				if _, ok := value.(map[string]any); ok {
					cleaned[key] = cleanFunctionParameters(value)
					continue
				}
				cleaned[key] = value
			case "anyOf":
				if vl, ok := value.([]any); ok {
					out := make([]any, len(vl))
					for i, item := range vl {
						out[i] = cleanFunctionParameters(item)
					}
					cleaned[key] = out
					continue
				}
				cleaned[key] = value
			default:
				cleaned[key] = value
			}
		}
		return cleaned
	default:
		return schema
	}
}

// toNativeSchema 把标准 JSON Schema 转为 Vertex AI 匿名 UI 端点要求的原生 Map-style Schema。
func toNativeSchema(schema any) any {
	m, ok := schema.(map[string]any)
	if !ok {
		return schema
	}
	out := map[string]any{}
	for k, v := range m {
		if schemaUnsupportedKeys[k] {
			continue
		}
		out[k] = v
	}

	switch t := out["type"].(type) {
	case []any:
		picked := "string"
		for _, item := range t {
			if s, ok := item.(string); ok && s != "null" {
				picked = s
				break
			}
		}
		out["type"] = strings.ToUpper(picked)
	case string:
		out["type"] = strings.ToUpper(t)
	default:
		out["type"] = "OBJECT"
	}
	validTypes := map[string]bool{
		"STRING": true, "INTEGER": true, "NUMBER": true,
		"BOOLEAN": true, "ARRAY": true, "OBJECT": true,
	}
	if !validTypes[out["type"].(string)] {
		out["type"] = "STRING"
	}

	if props, ok := out["properties"].(map[string]any); ok {
		nativeProps := make([]any, 0, len(props))
		for key, value := range props {
			converted := value
			if vm, ok := value.(map[string]any); ok {
				converted = toNativeSchema(vm)
			}
			nativeProps = append(nativeProps, map[string]any{"key": key, "value": converted})
		}
		out["properties"] = nativeProps
	}

	if items, ok := out["items"].(map[string]any); ok {
		out["items"] = toNativeSchema(items)
	}

	numericConstraints := []string{"minItems", "maxItems", "minProperties", "maxProperties", "minLength", "maxLength"}
	for _, field := range numericConstraints {
		if v, ok := out[field]; ok && v != nil {
			switch n := v.(type) {
			case float64:
				out[field] = strconv.FormatFloat(n, 'f', 0, 64)
			case int:
				out[field] = strconv.Itoa(n)
			case int64:
				out[field] = strconv.FormatInt(n, 10)
			case string:
			}
		}
	}

	return out
}
