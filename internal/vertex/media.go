package vertex

import (
	"context"
	"encoding/base64"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/strutil"
)

// ImageData 是一张抽出的图片（base64 + mime）。
type ImageData struct {
	B64JSON  string
	MimeType string
}

// AudioData 是抽出的整段音频（base64 + mime）。
type AudioData struct {
	Data     string // 整段音频的 base64（多段 PCM 已拼接）
	MimeType string
}

// CompleteChatImage 走标准非流式请求，再从响应抽取图片数据。
func (c *VertexAIClient) CompleteChatImage(ctx context.Context, model string, geminiPayload map[string]any) ([]ImageData, error) {
	result, err := c.CompleteChat(ctx, model, geminiPayload)
	if err != nil {
		return nil, err
	}
	return extractImageResponse(result), nil
}

// CompleteChatAudio 走标准非流式请求，再从响应抽取（拼接）音频数据。
func (c *VertexAIClient) CompleteChatAudio(ctx context.Context, model string, geminiPayload map[string]any) (AudioData, error) {
	result, err := c.CompleteChat(ctx, model, geminiPayload)
	if err != nil {
		return AudioData{}, err
	}
	return extractAudioResponse(result), nil
}

// extractImageResponse 从完整 Gemini 响应抽取图片。
//
// ① 优先 inlineData：每个带 data 的 inlineData → {b64_json, mime_type}。
// ② 退化：若全文以 "![Generated Image](data:" 开头，则从 markdown 抠出 base64。
// 无图返回空切片（路由层据此返 502）。
func extractImageResponse(result map[string]any) []ImageData {
	allParts := firstCandidateParts(result)

	var fullText strings.Builder
	var inlineImages []map[string]any
	for _, pRaw := range allParts {
		p, ok := pRaw.(map[string]any)
		if !ok {
			continue
		}
		if t, ok := p["text"]; ok {
			fullText.WriteString(toStr(t))
		}
		if id, ok := p["inlineData"].(map[string]any); ok {
			inlineImages = append(inlineImages, id)
		}
	}

	// ① inlineData 格式
	if len(inlineImages) > 0 {
		var out []ImageData
		for _, img := range inlineImages {
			data := toStr(img["data"])
			if data == "" {
				continue
			}
			mime := toStr(img["mimeType"])
			if mime == "" {
				mime = "image/png"
			}
			out = append(out, ImageData{B64JSON: data, MimeType: mime})
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
					return []ImageData{{B64JSON: encoded}}
				}
			}
		}
	}
	return nil
}

// extractAudioResponse 从完整 Gemini 响应抽取并拼接 TTS 音频。
//
// Gemini TTS 把整段音频切成多段 inlineData（每段一小块 L16 PCM），必须把所有音频段的
// 原始字节按序拼接，否则只取第一段会被截断成几十毫秒（血泪教训）。返回拼接后整段的 base64 + mime。
func extractAudioResponse(result map[string]any) AudioData {
	allParts := firstCandidateParts(result)

	var raw []byte
	mime := ""
	for _, pRaw := range allParts {
		p, ok := pRaw.(map[string]any)
		if !ok {
			continue
		}
		inline, ok := p["inlineData"].(map[string]any)
		if !ok {
			continue
		}
		data := toStr(inline["data"])
		if data == "" {
			continue
		}
		m := toStr(inline["mimeType"])
		// 仅接受 audio/* 或无 mime 的段（按既定条件）。
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
		decoded, err := decodeBase64Loose(data)
		if err != nil {
			continue // 单段解码失败跳过
		}
		raw = append(raw, decoded...)
	}

	if len(raw) > 0 {
		if mime == "" {
			mime = "audio/L16;rate=24000"
		}
		return AudioData{Data: base64.StdEncoding.EncodeToString(raw), MimeType: mime}
	}
	return AudioData{}
}

// firstCandidateParts 取 result.candidates[0].content.parts（无则空切片）。
func firstCandidateParts(result map[string]any) []any {
	cands, ok := result["candidates"].([]any)
	if !ok || len(cands) == 0 {
		return nil
	}
	c, ok := cands[0].(map[string]any)
	if !ok {
		return nil
	}
	content, ok := c["content"].(map[string]any)
	if !ok {
		return nil
	}
	parts, _ := content["parts"].([]any)
	return parts
}

// decodeBase64Loose 容错解码 base64（委托至 strutil.DecodeBase64Loose 统一维护）。
func decodeBase64Loose(s string) ([]byte, error) {
	return strutil.DecodeBase64Loose(s)
}
