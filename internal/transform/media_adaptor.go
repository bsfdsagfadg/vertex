package transform

import (
	"encoding/json"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

// 本文件为图像家族 ProtocolAdaptor 与音频家族 ProtocolAdaptor。
//
// Image：入站 OAI images API（JSON /vl/images/generations 或 multipart 已解析
// 的 *ImagesRequest），出站 *ImagesResponse。
// Audio：入站 OAI /v1/audio/speech 的 *SpeechRequest，出站 *AudioData。

// ImageAdaptor 是图像家族适配器。
type ImageAdaptor struct{}

// NewImageAdaptor 构造图像适配器。
func NewImageAdaptor() *ImageAdaptor { return &ImageAdaptor{} }

// Family 返回图片模型家族。
func (a *ImageAdaptor) Family() ModelFamily { return FamilyImage }

// ToGeminiRequest 归一化图片生成请求。
func (a *ImageAdaptor) ToGeminiRequest(raw any, cfg config.ConfigProvider) (*GeminiRequest, string, error) {
	switch v := raw.(type) {
	case *ImagesRequest:
		model := ResolveImageModel(v.Model)
		size := v.Size
		if size == "" {
			size = "1024x1024"
		}
		prompt := v.Prompt
		if v.NegativePrompt != "" {
			prompt = AppendNegativePrompt(prompt, v.NegativePrompt)
		}
		promptText := buildImagePrompt(prompt, size, v.Quality, v.Style, v.Background, "", false)

		req := &GeminiRequest{
			Contents: []Content{{Role: "user", Parts: []Part{{Text: promptText}}}},
			GenerationConfig: &GenerationConfig{
				ResponseModalities: []string{"TEXT", "IMAGE"},
			},
		}
		ic := &ImageConfig{}
		if ar := sizeToAspectRatio(size); ar != "" {
			ic.AspectRatio = ar
		}
		if is := sizeToImageSize(size); is != "" && ImageSizeAllowedFor(model, is) {
			ic.ImageSize = is
		}
		if ic.AspectRatio != "" || ic.ImageSize != "" || ic.ImageType != "" {
			req.GenerationConfig.ImageConfig = ic
		}
		// 模型上层策略 Enhance 会补 imageSize 默认档位 / responseModalities 覆盖。
		return req, model, nil
	case *GeminiRequest:
		return v, "", nil
	case map[string]any:
		return normalizeGeminiRequestMap(v)
	default:
		return nil, "", nil
	}
}

// FromGeminiResponse 图片响应 → *ImagesResponse。
func (a *ImageAdaptor) FromGeminiResponse(resp *GeminiResponse, model string) any {
	images := ExtractImagesTyped(resp)
	out := &ImagesResponse{Data: []ImagesResponseItem{}}
	for _, img := range images {
		out.Data = append(out.Data, ImagesResponseItem{B64JSON: img.B64JSON})
	}
	return out
}

// FromGeminiChunk 图片家族无流式转换，返回 nil。
func (a *ImageAdaptor) FromGeminiChunk(chunk *GeminiChunk, model, requestID string, isFirst bool, tracker *StreamToolCallTracker) []string {
	return nil
}

// AggregateN 聚合 n 张图片为单响应。
func (a *ImageAdaptor) AggregateN(resps []*GeminiResponse, model string) any {
	out := &ImagesResponse{Data: []ImagesResponseItem{}}
	for _, resp := range resps {
		images := ExtractImagesTyped(resp)
		for _, img := range images {
			out.Data = append(out.Data, ImagesResponseItem{B64JSON: img.B64JSON})
		}
	}
	return out
}

// AudioAdaptor 为音频（TTS）家族适配器。
type AudioAdaptor struct{}

const ttsDefaultVoice = "Kore"

//nolint:gochecknoglobals // Voice translation map
var ttsVoiceMap = map[string]string{
	"alloy": "Kore", "echo": "Puck", "fable": "Charon", "onyx": "Fenrir",
	"nova": "Aoede", "shimmer": "Leda", "ash": "Orus", "ballad": "Zephyr",
	"coral": "Aoede", "sage": "Charon", "verse": "Puck",
}

//nolint:gochecknoglobals // Valid Gemini voices
var ttsGeminiVoices = map[string]bool{
	"Kore": true, "Puck": true, "Charon": true, "Aoede": true, "Fenrir": true, "Leda": true,
	"Orus": true, "Zephyr": true, "Autonoe": true, "Enceladus": true, "Iapetus": true,
	"Umbriel": true, "Algieba": true, "Despina": true, "Erinome": true, "Algenib": true,
	"Rasalgethi": true, "Laomedeia": true, "Achernar": true, "Alnilam": true, "Schedar": true,
	"Gacrux": true, "Pulcherrima": true, "Achird": true, "Zubenelgenubi": true,
	"Vindemiatrix": true, "Sadachbia": true, "Sadaltager": true, "Sulafat": true,
}

// ResolveVoice 规范化/映射声线名称。
func ResolveVoice(voice string) string {
	v := strings.TrimSpace(voice)
	if v == "" {
		return ttsDefaultVoice
	}
	if ttsGeminiVoices[v] {
		return v
	}
	if mapped, ok := ttsVoiceMap[strings.ToLower(v)]; ok {
		return mapped
	}
	return ttsDefaultVoice
}

// NewAudioAdaptor 构造音频适配器。
func NewAudioAdaptor() *AudioAdaptor { return &AudioAdaptor{} }

// Family 返回音频家族。
func (a *AudioAdaptor) Family() ModelFamily { return FamilyAudio }

// ToGeminiRequest 由 *SpeechRequest 构建 Gemini TTS 请求。
func (a *AudioAdaptor) ToGeminiRequest(raw any, cfg config.ConfigProvider) (*GeminiRequest, string, error) {
	switch v := raw.(type) {
	case *SpeechRequest:
		model := v.Model
		if model == "" || !strings.Contains(model, "gemini") {
			model = "gemini-3.1-flash-tts-preview"
		}
		prompt := v.Input
		if v.Speed != nil {
			spd := *v.Speed
			if spd != 0 && absDiff(spd, 1.0) > 1e-6 {
				pace := "faster"
				if spd < 1.0 {
					pace = "more slowly"
				}
				prompt = "Say the following " + pace + ": " + prompt
			}
		}
		return &GeminiRequest{
			Contents: []Content{{Role: "user", Parts: []Part{{Text: prompt}}}},
			GenerationConfig: &GenerationConfig{
				ResponseModalities: []string{"AUDIO"},
				SpeechConfig: &SpeechConfig{
					VoiceConfig: &VoiceConfig{PrebuiltVoiceConfig: &PrebuiltVoiceConfig{VoiceName: ResolveVoice(v.Voice)}},
				},
			},
		}, model, nil
	case *GeminiRequest:
		return v, "", nil
	case map[string]any:
		return normalizeGeminiRequestMap(v)
	default:
		return nil, "", nil
	}
}

// FromGeminiResponse 音频响应 → *AudioData。
func (a *AudioAdaptor) FromGeminiResponse(resp *GeminiResponse, model string) any {
	audio := ExtractAudioTyped(resp)
	if len(audio.Bytes) == 0 {
		return nil
	}
	return &audio
}

// FromGeminiChunk TTS 无流式转换，返回 nil。
func (a *AudioAdaptor) FromGeminiChunk(chunk *GeminiChunk, model, requestID string, isFirst bool, tracker *StreamToolCallTracker) []string {
	return nil
}

// AggregateN 只取首个含音频的响应。
func (a *AudioAdaptor) AggregateN(resps []*GeminiResponse, model string) any {
	for _, resp := range resps {
		audio := ExtractAudioFrom(resp)
		if len(audio.Bytes) > 0 {
			return &audio
		}
	}
	return nil
}

// ExtractAudioFrom 强类型抽取音频。
func ExtractAudioFrom(resp *GeminiResponse) AudioData { return ExtractAudioTyped(resp) }

// absDiff 返回两浮点差的绝对值。
func absDiff(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}

// jsonxMarshal 序列化 raw map。
func jsonxMarshal(v any) ([]byte, error) { return json.Marshal(v) }

// jsonxUnmarshal 反序列化 raw map。
func jsonxUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// errInvalidProtocol 构造协议错误。
func errInvalidProtocol(msg string) error { return &protocolErr{msg: msg} }

type protocolErr struct{ msg string }

func (e *protocolErr) Error() string { return e.msg }