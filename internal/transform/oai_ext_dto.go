package transform

// Images API 与 Audio Speech API 的强类型 DTO。
// 与 oai_dto.go 同构：指针 + omitempty。

// ImagesRequest 是 OpenAI /v1/images/generations JSON 请求体。
type ImagesRequest struct {
	Model          string `json:"model,omitempty"`
	Prompt         string `json:"prompt,omitempty"`
	N              *int   `json:"n,omitempty"`
	Size           string `json:"size,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
	Quality        string `json:"quality,omitempty"`
	Style          string `json:"style,omitempty"`
	Background     string `json:"background,omitempty"`
	NegativePrompt string `json:"negative_prompt,omitempty"`
}

// ImagesResponseItem 是 images.data 单项。
type ImagesResponseItem struct {
	B64JSON string `json:"b64_json,omitempty"`
	URL     string `json:"url,omitempty"`
}

// ImagesResponse 是 OpenAI /v1/images/* 响应。
type ImagesResponse struct {
	Created int64               `json:"created"`
	Data    []ImagesResponseItem `json:"data"`
}

// SpeechRequest 是 OpenAI /v1/audio/speech 请求体。
type SpeechRequest struct {
	Model          string  `json:"model,omitempty"`
	Input          string  `json:"input,omitempty"`
	Voice          string  `json:"voice,omitempty"`
	ResponseFormat string  `json:"response_format,omitempty"`
	Speed          *float64 `json:"speed,omitempty"`
}

// SpeechFormat 是 TTS 输出格式元信息。
type SpeechFormat struct {
	ContentType string
	WrapWAV     bool
}

// SpeechFormatMap 是输出格式查表。
func SpeechFormatMap() map[string]SpeechFormat {
	return map[string]SpeechFormat{
		"mp3":  {"audio/wav", true},
		"wav":  {"audio/wav", true},
		"pcm":  {"audio/L16", false},
		"opus": {"audio/wav", true},
		"aac":  {"audio/wav", true},
		"flac": {"audio/wav", true},
	}
}

// DefaultSpeechFormat 返回缺省 output 格式。
func DefaultSpeechFormat() string { return "mp3" }