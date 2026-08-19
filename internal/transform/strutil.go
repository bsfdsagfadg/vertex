package transform

import "github.com/bsfdsagfadg/vertex/internal/strutil"

// SnakeToCamel 将 snake_case 转为 camelCase（委托至 strutil.SnakeToCamel 统一维护）。
func SnakeToCamel(s string) string {
	return strutil.SnakeToCamel(s)
}

// CamelToSnake 将 camelCase 转为 snake_case（委托至 strutil.CamelToSnake 统一维护）。
func CamelToSnake(s string) string {
	return strutil.CamelToSnake(s)
}
