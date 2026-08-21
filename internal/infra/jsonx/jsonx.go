// Package jsonx 提供关闭 HTML 转义的 JSON 序列化。
//
// Go 标准库 json.Marshal 默认会把 < > & 转义成 < > &，
// 而我们不做这种转义。为了逐字节稳定（既用于发往上游的请求体，也用于返回给客户端的响应体），
// 这里统一用关闭 HTML 转义的编码器。这是里程碑红线之一。
package jsonx

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Marshal 序列化为 JSON，不做 HTML 转义、不转义非 ASCII。
func Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("error: %w", err)

	}
	// json.Encoder.Encode 会在末尾追加一个换行符，去掉以与 json.Marshal 输出一致。
	b := buf.Bytes()
	if n := len(b); n > 0 && b[n-1] == '\n' {
		b = b[:n-1]
	}
	return b, nil
}

// Truthy 复刻动态语言常见的真值语义，用于判断解析出的 JSON 值是否"为真"
// （nil/false/空串/0/空数组/空对象为假，其余为真）。集中一处，避免各包重复实现导致语义漂移。
func Truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
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
