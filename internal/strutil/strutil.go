package strutil

import (
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var (
	camelRe   = regexp.MustCompile(`([a-z0-9])([A-Z])`)
	idCounter uint64
)

// PadB64 normalizes base64url characters (- and _) to standard (+ and /)
// and appends '=' padding until the length is a multiple of 4.
func PadB64(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "-", "+"), "_", "/")
	if pad := len(s) % 4; pad != 0 {
		s += strings.Repeat("=", 4-pad)
	}
	return s
}

// DecodeB64 decodes a base64 string after normalizing and padding.
func DecodeB64(s string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(PadB64(s))
	if err != nil {
		return nil, fmt.Errorf("decode base64: %w", err)
	}
	return b, nil
}

// FirstNonEmpty returns the first non-empty trimmed string from the given values.
func FirstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// FirstNonEmptyStr returns the first non-empty string between a and b.
func FirstNonEmptyStr(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// IsTruthyStr reports whether a string represents a truthy boolean value.
func IsTruthyStr(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// ToStr converts v to string if it is a string type, otherwise returns "".
func ToStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// ToStrOr converts v to string if non-empty, otherwise returns def.
func ToStrOr(v any, def string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return def
}

// ToString converts v to string; if v is string returns it, if nil returns "", else uses fmt.Sprint.
func ToString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// ToInt converts v (float64, int, int64, string) to int, or returns def.
func ToInt(v any, def int) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case string:
		if i, err := strconv.Atoi(n); err == nil {
			return i
		}
	}
	return def
}

// ToMap casts v to map[string]any if possible.
func ToMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

// ReqID returns a 24-character hexadecimal ID from 12 cryptographically random bytes.
func ReqID() string {
	var buf [12]byte
	if _, err := cryptorand.Read(buf[:]); err != nil {
		now := time.Now().UnixNano()
		count := atomic.AddUint64(&idCounter, 1)
		var fallback [12]byte
		fallback[0] = byte(now >> 56)
		fallback[1] = byte(now >> 48)
		fallback[2] = byte(now >> 40)
		fallback[3] = byte(now >> 32)
		fallback[4] = byte(now >> 24)
		fallback[5] = byte(now >> 16)
		fallback[6] = byte(now >> 8)
		fallback[7] = byte(now)
		fallback[8] = byte(count >> 24)
		fallback[9] = byte(count >> 16)
		fallback[10] = byte(count >> 8)
		fallback[11] = byte(count)
		return hex.EncodeToString(fallback[:])
	}
	return hex.EncodeToString(buf[:])
}

// RandomHex returns a cryptographically secure random hexadecimal string of nBytes length (2*nBytes characters).
func RandomHex(nBytes int) string {
	if nBytes <= 0 {
		return ""
	}
	buf := make([]byte, nBytes)
	if _, err := cryptorand.Read(buf); err != nil {
		var out strings.Builder
		for out.Len() < nBytes*2 {
			out.WriteString(ReqID())
		}
		return out.String()[:nBytes*2]
	}
	return hex.EncodeToString(buf)
}

// SnakeToCamel converts snake_case to camelCase.
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

// CamelToSnake converts camelCase to snake_case.
func CamelToSnake(s string) string {
	return strings.ToLower(camelRe.ReplaceAllString(s, "${1}_${2}"))
}

func pyTitle(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + strings.ToLower(s[1:])
}
