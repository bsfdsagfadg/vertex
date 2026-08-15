package transform

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
)

// NormalizeBase64 规范化 base64：剥离 data URI 前缀、URL-safe 字符还原、补 padding。
func NormalizeBase64(data string) string {
	value := strings.TrimSpace(data)
	if strings.Contains(value, ",") && strings.HasPrefix(value, "data:") {
		if idx := strings.Index(value, ","); idx >= 0 {
			value = value[idx+1:]
		}
	}
	value = strings.NewReplacer("-", "+", "_", "/").Replace(value)
	if pad := len(value) % 4; pad != 0 {
		value += strings.Repeat("=", 4-pad)
	}
	return value
}

// camelRe 在小写字母/数字与紧随的大写字母之间插入下划线，正则 ([a-z0-9])([A-Z])。
var camelRe = regexp.MustCompile(`([a-z0-9])([A-Z])`)

// SnakeToCamel 将 snake_case 转为 camelCase。
//
// 无下划线则原样返回（已是 camelCase 的键经此函数不变，这点对 generationConfig
// 的键转换很重要：temperature/topP/topK 等保持不动）。
func SnakeToCamel(s string) string {
	if !strings.Contains(s, "_") {
		return s
	}
	parts := strings.Split(s, "_")
	var b strings.Builder
	b.WriteString(parts[0])
	for _, p := range parts[1:] {
		b.WriteString(pyTitle(p))
	}
	return b.String()
}

// CamelToSnake 将 camelCase 转为 snake_case。
func CamelToSnake(s string) string {
	return strings.ToLower(camelRe.ReplaceAllString(s, "${1}_${2}"))
}

// pyTitle 把单个词归一为首字母大写、其余小写。
// （Go 的 strings.Title 不会把其余字母转小写，故自实现。）
func pyTitle(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + strings.ToLower(s[1:])
}

// DeepCopyAny 深拷贝 map/slice/JSON 结构。
func DeepCopyAny(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = DeepCopyAny(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = DeepCopyAny(item)
		}
		return out
	default:
		return v
	}
}

// toString 把任意值转成字符串：字符串原样，其它用 fmt.Sprint。
func toString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// asMapSlice 把 any 规整成 []map[string]any（非 map 元素丢弃）。
func asMapSlice(v any) []map[string]any {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(arr))
	for _, item := range arr {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// copyMap 浅拷贝一个 map[string]any。
func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// isTruthy 做动态 truthy 判定：nil/""/false/0/空容器为假，其余为真。
func isTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case int:
		return x != 0
	case int64:
		return x != 0
	case float64:
		return x != 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	default:
		return true
	}
}

// trimLowerSuffix 去掉 query/fragment 后转小写。
func trimLowerSuffix(s string) string {
	if parts := strings.SplitN(s, "?", 2); len(parts) > 0 {
		s = parts[0]
	}
	if parts := strings.SplitN(s, "#", 2); len(parts) > 0 {
		s = parts[0]
	}
	return strings.ToLower(s)
}

func decodeBase64Loose(s string) ([]byte, error) {
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	t := strings.ReplaceAll(strings.ReplaceAll(s, "-", "+"), "_", "/")
	if pad := len(t) % 4; pad != 0 {
		t += strings.Repeat("=", 4-pad)
	}
	return base64.StdEncoding.DecodeString(t)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
