package transform

import (
	"strings"
)

// 本文件是 typed 媒体提取：从 *GeminiResponse 抽取图片/音频。
// 语义与 vertex/media.go 旧 map 版完全一致，仅输入改为强类型。

// ImagePayload 是一张抽出的图片（base64 + mime）。
type ImagePayload struct {
	B64JSON  string
	MimeType string
}

// ExtractImagesTyped 从强类型响应抽取图片。
//
// ① 优先 inlineData：每个带 data 的 inlineData → {b64_json, mime_type}。
// ② 退化：若全文以 "![Generated Image](data:" 开头，则从 markdown 抠出 base64。
// 无图返回空切片（路由层据此返 502）。
func ExtractImagesTyped(resp *GeminiResponse) []ImagePayload {
	c, _ := firstCandidateTyped(resp)
	parts := candidatePartsTyped(c)

	var fullText strings.Builder
	var inlineImages []*InlineData
	for i := range parts {
		p := &parts[i]
		if p.Text != "" {
			fullText.WriteString(p.Text)
		}
		if p.InlineData != nil {
			inlineImages = append(inlineImages, p.InlineData)
		}
	}

	// ① inlineData 格式
	if len(inlineImages) > 0 {
		var out []ImagePayload
		for _, img := range inlineImages {
			if img.Data == "" {
				continue
			}
			mime := img.MimeType
			if mime == "" {
				mime = "image/png"
			}
			out = append(out, ImagePayload{B64JSON: img.Data, MimeType: mime})
		}
		return out
	}

	// ② markdown 退化格式
	text := fullText.String()
	if strings.HasPrefix(text, "![Generated Image](data:") {
		startIdx := strings.Index(text, "(")
		endIdx := strings.LastIndex(text, ")")
		if startIdx != -1 && endIdx != -1 && endIdx > startIdx {
			dataURL := text[startIdx+1 : endIdx]
			if comma := strings.Index(dataURL, ","); comma != -1 {
				encoded := dataURL[comma+1:]
				if encoded != "" {
					return []ImagePayload{{B64JSON: encoded}}
				}
			}
		}
	}
	return nil
}

// ExtractAudioTyped 从强类型响应抽取并拼接 TTS 音频。
//
// Gemini TTS 把整段音频切成多段 inlineData（每段一小块 L16 PCM），必须把所有音频段的
// ExtractAudioTyped 从强类型响应抽取并拼接 TTS 音频。
//
// Gemini TTS 把整段音频切成多段 inlineData（每段一小块 L16 PCM），必须把所有音频段的
// 原始字节按序拼接，否则只取第一段会被截断成几十毫秒。返回拼接后整段的 base64 + mime。
func ExtractAudioTyped(resp *GeminiResponse) AudioData {
	c, _ := firstCandidateTyped(resp)
	parts := candidatePartsTyped(c)

	var raw []byte
	mime := ""
	for i := range parts {
		p := &parts[i]
		inline := p.InlineData
		if inline == nil || inline.Data == "" {
			continue
		}
		m := inline.MimeType
		// 仅接受 audio/* 或无 mime 的段。
		if m != "" && !strings.HasPrefix(m, "audio/") {
			continue
		}
		if mime == "" {
			if m != "" {
				mime = m
			} else {
				mime = "audio/L16;rate=24000"
			}
		}
		decoded, err := decodeBase64Loose(inline.Data)
		if err != nil {
			continue // 单段解码失败跳过
		}
		raw = append(raw, decoded...)
	}

	if len(raw) > 0 {
		if mime == "" {
			mime = "audio/L16;rate=24000"
		}
		return AudioData{Bytes: raw, SampleRate: parsePCMRate(mime), MIME: mime}
	}
	return AudioData{}
}

// parsePCMRate 从 mime 里的 rate= 取采样率，缺省 24000。
func parsePCMRate(mimeType string) int {
	const def = 24000
	if mimeType == "" {
		return def
	}
	for _, token := range strings.Split(mimeType, ";") {
		token = strings.ToLower(strings.TrimSpace(token))
		if strings.HasPrefix(token, "rate=") {
			n := 0
			ok := true
			for _, r := range token[5:] {
				if r < '0' || r > '9' {
					ok = false
					break
				}
				n = n*10 + int(r-'0')
			}
			if ok && n > 0 {
				return n
			}
		}
	}
	return def
}

// firstCandidateTyped 从强类型 GeminiResponse 安全获取首个 Candidate。
func firstCandidateTyped(resp *GeminiResponse) (*Candidate, bool) {
	if resp == nil || len(resp.Candidates) == 0 {
		return nil, false
	}
	c := resp.Candidates[0]
	if c == nil {
		return nil, false
	}
	return c, true
}

// candidatePartsTyped 从 Candidate 安全获取 Part 列表。
func candidatePartsTyped(c *Candidate) []Part {
	if c == nil || c.Content == nil {
		return nil
	}
	return c.Content.Parts
}
