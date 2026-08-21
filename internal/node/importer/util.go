package importer

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ErrToStr 将 error 归一为字符串（nil → 空串），供 api 层测速结果落库复用。
func ErrToStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func decodeSubBase64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, " ", "")
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	if b, err := base64.URLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	t := strings.ReplaceAll(strings.ReplaceAll(s, "-", "+"), "_", "/")
	if pad := len(t) % 4; pad != 0 {
		t += strings.Repeat("=", 4-pad)
	}
	return base64.StdEncoding.DecodeString(t)
}

func parseInlineYamlAttrs(s string) map[string]string {
	attrs := make(map[string]string)
	var currentKey, currentValue strings.Builder
	inQuotes := false
	var quoteChar rune
	isKey := true
	braceDepth := 0
	bracketDepth := 0

	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if inQuotes {
			if r == quoteChar {
				inQuotes = false
			} else if r == '\\' && i+1 < len(runes) {
				if isKey {
					currentKey.WriteRune(runes[i+1])
				} else {
					currentValue.WriteRune(runes[i+1])
				}
				i++
			} else {
				if isKey {
					currentKey.WriteRune(r)
				} else {
					currentValue.WriteRune(r)
				}
			}
			continue
		}

		if r == '"' || r == '\'' {
			inQuotes = true
			quoteChar = r
			continue
		}

		if isKey {
			if r == ':' {
				isKey = false
				if i+1 < len(runes) && runes[i+1] == ' ' {
					i++
				}
			} else if r != ' ' && r != '\t' {
				currentKey.WriteRune(r)
			}
		} else {
			switch r {
			case '{':
				braceDepth++
				currentValue.WriteRune(r)
			case '}':
				if braceDepth > 0 {
					braceDepth--
				}
				currentValue.WriteRune(r)
			case '[':
				bracketDepth++
				currentValue.WriteRune(r)
			case ']':
				if bracketDepth > 0 {
					bracketDepth--
				}
				currentValue.WriteRune(r)
			case ',':
				if braceDepth > 0 || bracketDepth > 0 {
					currentValue.WriteRune(r)
					continue
				}
				key := strings.TrimSpace(currentKey.String())
				val := strings.TrimSpace(currentValue.String())
				if key != "" {
					attrs[key] = val
				}
				currentKey.Reset()
				currentValue.Reset()
				isKey = true
			default:
				currentValue.WriteRune(r)
			}
		}
	}

	key := strings.TrimSpace(currentKey.String())
	val := strings.TrimSpace(currentValue.String())
	if key != "" {
		attrs[key] = val
	}

	return attrs
}

func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func parseInlineYamlObject(s string) map[string]string {
	trimmed := strings.TrimSpace(s)
	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") && len(trimmed) >= 2 {
		trimmed = strings.TrimSpace(trimmed[1 : len(trimmed)-1])
	}
	if trimmed == "" {
		return map[string]string{}
	}
	return parseInlineYamlAttrs(trimmed)
}

func buildProxyURI(scheme, credential, server, port, name string, query url.Values) string {
	u := &url.URL{
		Scheme:   scheme,
		User:     url.User(credential),
		Host:     net.JoinHostPort(server, port),
		Fragment: name,
	}
	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}
	return u.String()
}

func intValue(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int8:
		return int(x)
	case int16:
		return int(x)
	case int32:
		return int(x)
	case int64:
		return int(x)
	case uint:
		return int(x)
	case uint8:
		return int(x)
	case uint16:
		return int(x)
	case uint32:
		return int(x)
	case uint64:
		return int(x)
	case float32:
		return int(x)
	case float64:
		return int(x)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(x))
		return n
	default:
		return 0
	}
}

func boolValue(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return isTruthy(x)
	default:
		return false
	}
}

func mapValue(v any) map[string]any {
	out, _ := normalizeYAMLValue(v).(map[string]any)
	return out
}

func sliceValue(v any) []any {
	out, _ := normalizeYAMLValue(v).([]any)
	return out
}

func firstMapValue(v any) map[string]any {
	items := sliceValue(v)
	if len(items) == 0 {
		return nil
	}
	return mapValue(items[0])
}

func parseJSONMapString(s string) map[string]any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}

func nestedObject(obj map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if key == "" {
			continue
		}
		if nested := mapValue(obj[key]); len(nested) > 0 {
			return nested
		}
		if nested := parseJSONMapString(ValueToString(obj[key])); len(nested) > 0 {
			return nested
		}
	}
	return nil
}

func splitCSV(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseReservedBytes(s string) []int {
	parts := splitCSV(s)
	if len(parts) == 0 {
		return nil
	}
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil
		}
		out = append(out, n)
	}
	return out
}

func splitInterfaceAddresses(s string) (string, string) {
	var ipv4, ipv6 string
	for _, part := range splitCSV(s) {
		if strings.Contains(part, ":") {
			if ipv6 == "" {
				ipv6 = part
			}
			continue
		}
		if ipv4 == "" {
			ipv4 = part
		}
	}
	return ipv4, ipv6
}

func normalizeImportedNetwork(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "", "raw", "tcp":
		return ""
	default:
		return s
	}
}

func importedAllowInsecure(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return strings.EqualFold(strings.TrimSpace(x), "true") || isTruthy(x)
	default:
		return false
	}
}

// ValueToString 将 any 归一为字符串（nil → 空串），供 api 层配置项读取复用。
func ValueToString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", x)
	}
}

func normalizeYAMLValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = normalizeYAMLValue(item)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[fmt.Sprintf("%v", key)] = normalizeYAMLValue(item)
		}
		return out
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, normalizeYAMLValue(item))
		}
		return out
	default:
		return v
	}
}
