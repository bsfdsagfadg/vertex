package transform

import "encoding/base64"

// 本文件是强类型 thoughtSignature 决策器：取代旧 map 版 functioncall.go 的
// skipThoughtSentinel / EncodeThoughtSignature / ensureBase64Signature。
//
// 上游匿名端点对多轮 history 中 model 回合的 functionCall / thought part 强校验
// thoughtSignature：缺失或非 base64 时拒绝多轮请求。本地无法自证签名，故注入
// base64("skip_thought_signature_validator") 伪签名哨兵让校验通过；
// 调试日志依此识别伪造签名。

// SkipThoughtSentinel 是伪签名哨兵明文（encode 后为 base64）。
const SkipThoughtSentinel = "skip_thought_signature_validator"
const skipThoughtSentinel = SkipThoughtSentinel

// SignatureResolver 是 typed part 的 thoughtSignature 决策器（无状态）。
type SignatureResolver struct{}

// NewSignatureResolver 构造解析器。
func NewSignatureResolver() *SignatureResolver { return &SignatureResolver{} }

// EnsureBase64Sig 把所有形态规范为合法 base64：
//
//   - 哨兵明文 → base64 编码
//   - 已是合法 base64 → 规范化保留
//   - 明文 → base64 编码降级
func (r *SignatureResolver) EnsureBase64Sig(sig string) string {
	if sig == SkipThoughtSentinel {
		return base64.StdEncoding.EncodeToString([]byte(sig))
	}
	normSig := NormalizeBase64(sig)
	if decoded, err := base64.StdEncoding.DecodeString(normSig); err == nil &&
		base64.StdEncoding.EncodeToString(decoded) == normSig {
		return normSig
	}
	return base64.StdEncoding.EncodeToString([]byte(sig))
}

// ApplyPart 对单个 typed part 签发/移除 thoughtSignature，语义对齐旧
// finalizeCleanedPart：
//
//   - functionCall part：缺失签名则注入哨兵，已有真实签名保留，不携带 id。
//   - thought part（thought=true）：缺失签名则注入哨兵。
//   - 纯文本 part（text 非空且非 thought 标记）：移除 thought/签名。
func (r *SignatureResolver) ApplyPart(p *Part) {
	if p.FunctionResponse != nil {
		p.Thought = false
		p.ThoughtSignature = ""
		return
	}
	hasFC := p.FunctionCall != nil
	hasThought := p.Thought
	if (hasFC || hasThought) && p.ThoughtSignature == "" {
		p.ThoughtSignature = SkipThoughtSentinel
	}
	if p.Text != "" && !hasThought && !hasFC {
		p.Thought = false
		p.ThoughtSignature = ""
	}
}

// ApplyContents 对一组 contents 逐 part 应用 ApplyPart，并把注入/保留的
// 签名统一规范为合法 base64（对齐旧 EncodeThoughtSignature 语义）。
func (r *SignatureResolver) ApplyContents(contents []Content) {
	for i := range contents {
		parts := contents[i].Parts
		for j := range parts {
			r.ApplyPart(&parts[j])
			if parts[j].ThoughtSignature != "" {
				parts[j].ThoughtSignature = r.EnsureBase64Sig(parts[j].ThoughtSignature)
			}
		}
	}
}

// ResolveSignatures 返回 ApplyContents 的纯函数等价（副本输入处理）。
func (r *SignatureResolver) ResolveSignatures(parts []Part) []Part {
	for j := range parts {
		r.ApplyPart(&parts[j])
		if parts[j].ThoughtSignature != "" {
			parts[j].ThoughtSignature = r.EnsureBase64Sig(parts[j].ThoughtSignature)
		}
	}
	return parts
}