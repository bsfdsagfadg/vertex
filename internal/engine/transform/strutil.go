package transform

import (
	"encoding/base64"
	"strings"
	"unicode"
)

// NormalizeBase64 规范化 base64（幂等）：
// 1) 全空白剥离（含中间换行/空格）；2) data URI 前缀剥离（大小写不敏感）；
// 3) URL-safe 字符还原；4) padding 自动补齐。
func NormalizeBase64(data string) string {
	value := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, data)
	if strings.HasPrefix(strings.ToLower(value), "data:") {
		if idx := strings.IndexByte(value, ','); idx >= 0 {
			value = value[idx+1:]
		}
	}
	value = strings.NewReplacer("-", "+", "_", "/").Replace(value)
	if pad := len(value) % 4; pad != 0 {
		value += strings.Repeat("=", 4-pad)
	}
	return value
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
