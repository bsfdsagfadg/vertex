package transform

import (
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/jsonx"
	"github.com/bsfdsagfadg/vertex/internal/strutil"
)

// 本文件是 transform 包内处理 map[string]any / any 动态结构的小工具，
// 用来贴近动态 dict 的语义（truthy 判断、浅拷贝、字符串化等）。

// copyMap 浅拷贝一个 map[string]any。
func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// toString 把任意值转成字符串：字符串原样，其它用 fmt.Sprint。
func toString(v any) string {
	return strutil.ToString(v)
}

// isTruthy 做动态 truthy 判定：nil/""/false/0/空容器为假，其余为真。
func isTruthy(v any) bool {
	return jsonx.Truthy(v)
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

// trimLowerSuffix 是给响应/猜测 mime 用的小工具，去掉 query/fragment 后转小写。
func trimLowerSuffix(s string) string {
	if parts := strings.SplitN(s, "?", 2); len(parts) > 0 {
		s = parts[0]
	}
	if parts := strings.SplitN(s, "#", 2); len(parts) > 0 {
		s = parts[0]
	}
	return strings.ToLower(s)
}

// deepCopyAny 深拷贝 map/slice/any 结构。
func deepCopyAny(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = deepCopyAny(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = deepCopyAny(val)
		}
		return out
	default:
		return v
	}
}

// firstTruthyString 返回参数里第一个非空字符串。
func firstTruthyString(vals ...any) string {
	for _, v := range vals {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// firstMap 返回第一个非空 map（用于 inlineData/inline_data 兼容）。
func firstMap(vals ...any) (map[string]any, bool) {
	for _, v := range vals {
		if m, ok := v.(map[string]any); ok && len(m) > 0 {
			return m, true
		}
	}
	return nil, false
}

// firstPresentRaw 在 map 里依次查 keys，返回第一个存在的原始值（不存在返回 nil）。
func firstPresentRaw(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	return nil
}
