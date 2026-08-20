package transform

import (
	"encoding/base64"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/domain"
	"github.com/bsfdsagfadg/vertex/internal/strutil"
)

const skipThoughtSentinel = "skip_thought_signature_validator"

// PartFinalizer 集中处理 Part 思考签名校验、Base64 编码规范化、空字段清理与内容块合并。
type PartFinalizer struct{}

// NewPartFinalizer 创建 Part 收尾终结器。
func NewPartFinalizer() *PartFinalizer {
	return &PartFinalizer{}
}

// FinalizeDomainPart 对强类型 Part 进行收尾与思考签名补全。
func (f *PartFinalizer) FinalizeDomainPart(p *domain.Part) {
	if p == nil {
		return
	}

	if p.FunctionResponse != nil {
		p.Thought = nil
		p.ThoughtSignature = ""
	} else {
		hasFC := p.FunctionCall != nil
		hasThought := isTruthy(p.Thought)
		hasSig := p.ThoughtSignature != ""
		if (hasFC || hasThought) && !hasSig {
			p.ThoughtSignature = skipThoughtSentinel
		}
	}

	if p.Text != "" && !isTruthy(p.Thought) {
		p.Thought = nil
		p.ThoughtSignature = ""
	}
}

// EnsureBase64Signature 将签名转换为规范的 Base64 字符串。
func EnsureBase64Signature(signature string) string {
	if signature == skipThoughtSentinel {
		return base64.StdEncoding.EncodeToString([]byte(signature))
	}
	normalized := NormalizeBase64(signature)
	decoded, err := base64.StdEncoding.DecodeString(normalized)
	if err == nil && base64.StdEncoding.EncodeToString(decoded) == normalized {
		return normalized
	}
	return base64.StdEncoding.EncodeToString([]byte(signature))
}

func ensureBase64Signature(signature string) string {
	return EnsureBase64Signature(signature)
}

// NormalizeBase64 规范化 base64（委托至 strutil.NormalizeBase64 统一维护）。
func NormalizeBase64(data string) string {
	return strutil.NormalizeBase64(data)
}

// EncodeThoughtSignature 递归规范化 contents 中所有的 thoughtSignature 为标准 Base64。
func EncodeThoughtSignature(contents any, depth int) any {
	const maxDepth = 64
	if depth > maxDepth {
		return contents
	}
	switch v := contents.(type) {
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = EncodeThoughtSignature(item, depth+1)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			if k == "parts" {
				if parts, ok := val.([]any); ok {
					newParts := make([]any, len(parts))
					for i, p := range parts {
						if pm, ok := p.(map[string]any); ok {
							np := copyMap(pm)
							if sig, ok := np["thoughtSignature"].(string); ok && sig != "" {
								np["thoughtSignature"] = EnsureBase64Signature(sig)
							}
							newParts[i] = np
						} else {
							newParts[i] = p
						}
					}
					out[k] = newParts
					continue
				}
			}
			switch val.(type) {
			case map[string]any, []any:
				out[k] = EncodeThoughtSignature(val, depth+1)
			default:
				out[k] = val
			}
		}
		return out
	default:
		return contents
	}
}

// HandleBase64InContents 递归规范化 contents 中 inlineData 的 base64 数据。
func HandleBase64InContents(contents any) any {
	switch v := contents.(type) {
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = HandleBase64InContents(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			if k == "inlineData" {
				if id, ok := val.(map[string]any); ok {
					if data, ok := id["data"].(string); ok {
						ni := copyMap(id)
						ni["data"] = NormalizeBase64(data)
						out[k] = ni
						continue
					}
				}
			}
			out[k] = HandleBase64InContents(val)
		}
		return out
	default:
		return contents
	}
}

// StripGeminiIDs 剥离生成的内部 synthetic 工具调用 ID。
func StripGeminiIDs(val any) any {
	switch v := val.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, mv := range v {
			if s, ok := mv.(string); ok && (strings.HasPrefix(s, "gemini-tool-call-") || strings.HasPrefix(s, "tool_call_")) {
				if len(s) > 11 && strings.Contains(s, "-vp") {
					idx := strings.LastIndex(s, "-vp")
					if idx > 0 && len(s)-idx == 11 {
						out[k] = s[:idx]
						continue
					}
				}
			}
			out[k] = StripGeminiIDs(mv)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = StripGeminiIDs(item)
		}
		return out
	default:
		return val
	}
}

func stripGeminiIDs(val any) any {
	return StripGeminiIDs(val)
}

// MergeContentBlocks 合并相邻同类型文本块（thought+thought、text+text）。
func MergeContentBlocks(parts []map[string]any) []map[string]any {
	cleaned := make([]map[string]any, 0, len(parts))
	for _, p := range parts {
		if c := cleanSimple(p); c != nil {
			cleaned = append(cleaned, c)
		}
	}
	if len(cleaned) == 0 {
		return []map[string]any{}
	}

	merged := make([]map[string]any, 0, len(cleaned))
	var current map[string]any

	for _, part := range cleaned {
		isText := truthyStr(part["text"])
		if !isText {
			merged = append(merged, part)
			current = nil
			continue
		}
		isThought := isTruthy(part["thought"])
		if current != nil && isTruthy(current["thought"]) == isThought {
			current["text"] = toString(current["text"]) + toString(part["text"])
			if sig, ok := part["thoughtSignature"]; ok {
				if _, exists := current["thoughtSignature"]; !exists {
					current["thoughtSignature"] = sig
				}
			}
		} else {
			current = copyMap(part)
			merged = append(merged, current)
		}
	}
	return merged
}
