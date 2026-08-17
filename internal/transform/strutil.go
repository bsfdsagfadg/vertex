package transform

import (
	"encoding/base64"
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
