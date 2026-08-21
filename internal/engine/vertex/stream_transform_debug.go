package vertex

import (
	"encoding/base64"
	"strconv"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────
// 以下为 Debug 取证辅助：仅用于输出摘要，不改变任何转发行为。
// （自 stream_transform.go 按职责拆分，行数红线治理）
// ─────────────────────────────────────────────────────────────────────────

// skipThoughtSentinelStr 是 transform 包注入的伪签名哨兵明文，用于日志识别伪造签名。
const skipThoughtSentinelStr = "skip_thought_signature_validator"

// skipThoughtSentinelBase64 是 skipThoughtSentinel 的 base64 编码值，用于日志识别伪造签名。
var skipThoughtSentinelBase64 = base64.StdEncoding.EncodeToString([]byte(skipThoughtSentinelStr))

// sigKind 识别 thoughtSignature 的值形态：sentinel（伪造哨兵）/ 真实签名（长度+前缀）。
func sigKind(raw string) string {
	if raw == skipThoughtSentinelBase64 {
		return "sentinel"
	}
	if b, err := base64.StdEncoding.DecodeString(raw); err == nil && string(b) == skipThoughtSentinelStr {
		return "sentinel"
	}
	if len(raw) > 16 {
		return raw[:12] + "...(len" + strconv.Itoa(len(raw)) + ")"
	}
	return raw + "(len" + strconv.Itoa(len(raw)) + ")"
}

// describePart 生成单个 part 的紧凑描述（类型 + 关键字段长度/名称）。
func describePart(p map[string]any) string {
	if sig, ok := p["thoughtSignature"].(string); ok {
		return "sig:" + sigKind(sig)
	}
	if v, ok := p["text"].(string); ok {
		return "text:" + strconv.Itoa(len(v))
	}
	if v, ok := p["thought"].(string); ok {
		return "thought:" + strconv.Itoa(len(v))
	}
	if v, ok := p["thought"].(bool); ok {
		return "thought:" + strconv.FormatBool(v)
	}
	if fc, ok := p["functionCall"].(map[string]any); ok {
		name, _ := fc["name"].(string)
		return "fc:" + name
	}
	if fr, ok := p["functionResponse"].(map[string]any); ok {
		name, _ := fr["name"].(string)
		return "fr:" + name
	}
	if _, ok := p["inlineData"].(map[string]any); ok {
		return "inlineData"
	}
	return "other"
}

// describeUsage 提取 usageMetadata 的 token 计数摘要。
func describeUsage(u map[string]any) string {
	var sb strings.Builder
	for _, k := range []string{"promptTokenCount", "candidatesTokenCount", "totalTokenCount"} {
		if v, ok := u[k]; ok {
			sb.WriteByte(' ')
			sb.WriteString(k)
			sb.WriteByte('=')
			sb.WriteString(toStr(v))
		}
	}
	return sb.String()
}

// summarizeChunk 摘要格式化一个标准 chunk（与通过过程化提取后的结构一致）。
func summarizeChunk(chunk map[string]any) string {
	var sb strings.Builder
	if cands, ok := chunk["candidates"].([]any); ok && len(cands) > 0 {
		for i, cRaw := range cands {
			c, _ := cRaw.(map[string]any)
			if c == nil {
				continue
			}
			sb.WriteString(" c[")
			sb.WriteString(strconv.Itoa(i))
			sb.WriteByte(']')
			if fr, _ := c["finishReason"].(string); fr != "" {
				sb.WriteString(" fr=")
				sb.WriteString(fr)
			}
			content, _ := c["content"].(map[string]any)
			if content == nil {
				continue
			}
			role, _ := content["role"].(string)
			sb.WriteString(" :")
			sb.WriteString(role)
			sb.WriteByte('{')
			if ps, ok := content["parts"].([]any); ok {
				descs := make([]string, 0, len(ps))
				for _, pRaw := range ps {
					if p, ok := pRaw.(map[string]any); ok {
						descs = append(descs, describePart(p))
					} else {
						descs = append(descs, "part(non-map)")
					}
				}
				sb.WriteString(strings.Join(descs, ","))
			}
			sb.WriteString("}")
		}
	} else {
		sb.WriteString(" (no candidates)")
	}
	if usage, ok := chunk["usageMetadata"].(map[string]any); ok {
		sb.WriteString(describeUsage(usage))
	}
	if br, ok := chunk["promptFeedback"].(map[string]any); ok {
		if b, _ := br["blockReason"].(string); b != "" {
			sb.WriteString(" blockReason=")
			sb.WriteString(b)
		}
	}
	return strings.TrimSpace(sb.String())
}

// summarizeUpstreamObject 摘要单条上游原始 JSON 对象（results[].data[].ui.streamGenerateContentAnonymous 结构）。
func summarizeUpstreamObject(obj map[string]any) string {
	results, _ := obj["results"].([]any)
	if len(results) == 0 {
		return summarizeChunk(obj)
	}
	var descs []string
	for _, rRaw := range results {
		r, _ := rRaw.(map[string]any)
		if r == nil {
			continue
		}
		if errs, ok := r["errors"].([]any); ok && len(errs) > 0 {
			msg := ""
			if first, ok := errs[0].(map[string]any); ok {
				msg = toStr(first["message"])
			} else {
				msg = toStr(errs[0])
			}
			if len(msg) > 120 {
				msg = msg[:120]
			}
			descs = append(descs, "errors:"+msg)
			continue
		}
		data, _ := r["data"].(map[string]any)
		if data == nil {
			continue
		}
		inner := data
		if ui, ok := data["ui"].(map[string]any); ok {
			if innerRaw, exists := ui["streamGenerateContentAnonymous"]; exists {
				switch v := innerRaw.(type) {
				case map[string]any:
					inner = v
				case []any:
					var sub []string
					for _, itemRaw := range v {
						if item, ok := itemRaw.(map[string]any); ok {
							sub = append(sub, summarizeChunk(item))
						}
					}
					descs = append(descs, "list["+strings.Join(sub, " | ")+"]")
					continue
				}
			}
		}
		descs = append(descs, summarizeChunk(inner))
	}
	if len(descs) == 0 {
		return "(empty)"
	}
	return strings.Join(descs, " | ")
}
