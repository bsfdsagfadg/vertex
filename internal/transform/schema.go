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

var validNativeSchemaTypes = map[string]bool{ //nolint:gochecknoglobals
	"STRING": true, "INTEGER": true, "NUMBER": true,
	"BOOLEAN": true, "ARRAY": true, "OBJECT": true,
}

var numericConstraintFields = []string{ //nolint:gochecknoglobals
	"minItems", "maxItems", "minProperties", "maxProperties", "minLength", "maxLength",
}

// SchemaVisitor 定义 JSON Schema 遍历与转换的访问者接口。
type SchemaVisitor interface {
	Visit(schema any) any
	VisitMap(m map[string]any) map[string]any
	VisitSlice(arr []any) []any
	VisitPrimitive(val any) any
}

// SchemaSanitizer 基于访问者模式实现 Schema 递归过滤、字段裁剪与格式转换。
type SchemaSanitizer struct {
	AllowedFields          map[string]bool
	UnsupportedKeys        map[string]bool
	FormatNumericToString  bool
	NativePropertiesSlice  bool
	CoerceUppercaseType    bool
}

// NewFunctionParameterSanitizer 创建用于 Gemini Tool 函数 parameters 的白名单清洗器。
func NewFunctionParameterSanitizer() *SchemaSanitizer {
	return &SchemaSanitizer{
		AllowedFields:         geminiAllowedSchemaFields,
		FormatNumericToString: false,
		NativePropertiesSlice: false,
		CoerceUppercaseType:   false,
	}
}

// NewNativeSchemaSanitizer 创建用于 Vertex AI 匿名 GraphQL 端点的原生 Map-style Schema 清洗器。
func NewNativeSchemaSanitizer() *SchemaSanitizer {
	return &SchemaSanitizer{
		UnsupportedKeys:        schemaUnsupportedKeys,
		FormatNumericToString:  true,
		NativePropertiesSlice:  true,
		CoerceUppercaseType:    true,
	}
}

// Sanitize 净化给定的 schema。
func (s *SchemaSanitizer) Sanitize(schema any) any {
	return s.Visit(schema)
}

// Visit 统一入口派发。
func (s *SchemaSanitizer) Visit(schema any) any {
	switch v := schema.(type) {
	case map[string]any:
		return s.VisitMap(v)
	case []any:
		return s.VisitSlice(v)
	default:
		return s.VisitPrimitive(v)
	}
}

// VisitSlice 遍历切片。
func (s *SchemaSanitizer) VisitSlice(arr []any) []any {
	out := make([]any, len(arr))
	for i, item := range arr {
		out[i] = s.Visit(item)
	}
	return out
}

// VisitPrimitive 处理基本类型标量。
func (s *SchemaSanitizer) VisitPrimitive(val any) any {
	return val
}

// VisitMap 处理对象节点并执行清洗策略。
func (s *SchemaSanitizer) VisitMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}

	cleaned := make(map[string]any, len(m))

	for key, value := range m {
		// 1. 白名单过滤（如果设置了 AllowedFields）
		if len(s.AllowedFields) > 0 && !s.AllowedFields[key] {
			continue
		}
		// 2. 黑名单过滤（如果设置了 UnsupportedKeys）
		if len(s.UnsupportedKeys) > 0 && s.UnsupportedKeys[key] {
			continue
		}

		switch key {
		case "properties":
			if propsMap, ok := value.(map[string]any); ok {
				if s.NativePropertiesSlice {
					// 转换为 [{key: "foo", value: ...}]
					nativeProps := make([]any, 0, len(propsMap))
					for k, v := range propsMap {
						nativeProps = append(nativeProps, map[string]any{
							"key":   k,
							"value": s.Visit(v),
						})
					}
					cleaned[key] = nativeProps
				} else {
					props := make(map[string]any, len(propsMap))
					for k, v := range propsMap {
						props[k] = s.Visit(v)
					}
					cleaned[key] = props
				}
				continue
			}
			cleaned[key] = value

		case "items":
			if itemMap, ok := value.(map[string]any); ok {
				cleaned[key] = s.Visit(itemMap)
				continue
			}
			cleaned[key] = value

		case "anyOf", "oneOf", "allOf":
			if list, ok := value.([]any); ok {
				cleaned[key] = s.VisitSlice(list)
				continue
			}
			cleaned[key] = value

		default:
			cleaned[key] = value
		}
	}

	// 3. 处理 Type 规范化
	if s.CoerceUppercaseType {
		typeStr := "OBJECT"
		switch t := cleaned["type"].(type) {
		case []any:
			picked := "string"
			for _, item := range t {
				if str, ok := item.(string); ok && str != "null" {
					picked = str
					break
				}
			}
			typeStr = strings.ToUpper(picked)
		case string:
			typeStr = strings.ToUpper(t)
		}
		if !validNativeSchemaTypes[typeStr] {
			typeStr = "STRING"
		}
		cleaned["type"] = typeStr
	}

	// 4. 处理数值边界转为字符串
	if s.FormatNumericToString {
		for _, field := range numericConstraintFields {
			if v, ok := cleaned[field]; ok && v != nil {
				switch n := v.(type) {
				case float64:
					cleaned[field] = strconv.FormatFloat(n, 'f', 0, 64)
				case int:
					cleaned[field] = strconv.Itoa(n)
				case int64:
					cleaned[field] = strconv.FormatInt(n, 10)
				case string:
					cleaned[field] = n
				}
			}
		}
	}

	return cleaned
}

// CleanFunctionParameters 递归用 Gemini 白名单清洗 JSON Schema，剔除上游不支持的字段。
func CleanFunctionParameters(schema any) any {
	return NewFunctionParameterSanitizer().Sanitize(schema)
}

// ToNativeSchema 把标准 JSON Schema 转为 Vertex AI 匿名 UI 端点要求的原生 Map-style Schema。
func ToNativeSchema(schema any) any {
	return NewNativeSchemaSanitizer().Sanitize(schema)
}

// toNativeSchema 包内别名。
func toNativeSchema(schema any) any {
	return ToNativeSchema(schema)
}
