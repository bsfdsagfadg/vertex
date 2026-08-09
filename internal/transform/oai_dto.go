// OpenAI 协议强类型 DTO（ChatCompletion 及其扩展字段）。
//
// 与 dto.go 同构：指针 + omitempty 消除空默认值。其中 content/stop 等
// 兼容"字符串或数组"的字段用自定义 UnmarshalJSON 双态支持。
package transform

import (
	"bytes"
	"encoding/json"
)

// ChatCompletionRequest 是 OpenAI /v1/chat/completions 请求体。
type ChatCompletionRequest struct {
	Model          string          `json:"model,omitempty"`
	Messages       []Message       `json:"messages,omitempty"`
	Stream         bool            `json:"stream,omitempty"`
	N              *int            `json:"n,omitempty"`
	Temperature    *float64        `json:"temperature,omitempty"`
	TopP           *float64        `json:"top_p,omitempty"`
	TopK           *float64        `json:"top_k,omitempty"`
	PresencePenalty *float64       `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64      `json:"frequency_penalty,omitempty"`
	Seed           *int64          `json:"seed,omitempty"`
	MaxTokens      *int            `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int       `json:"max_completion_tokens,omitempty"`
	Stop           StopSequences   `json:"stop,omitempty"`
	Tools          []OAITool       `json:"tools,omitempty"`
	Functions      []OAIFunction   `json:"functions,omitempty"`
	ToolChoice     any             `json:"tool_choice,omitempty"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
	ReasoningEffort string         `json:"reasoning_effort,omitempty"`
	Thinking       *ThinkingParam  `json:"thinking,omitempty"`
	SafetySettings any             `json:"safety_settings,omitempty"`
	Logprobs       *bool           `json:"logprobs,omitempty"`
	TopLogprobs    *int            `json:"top_logprobs,omitempty"`
	MediaResolution string         `json:"media_resolution,omitempty"`
	ExtraBody      map[string]any  `json:"extra_body,omitempty"`

	// 图像/多模态扩展字段（OpenAI 兼容层透传 Gemini imageConfig）。
	ImageConfig map[string]any `json:"imageConfig,omitempty"`
	ImageSize   string         `json:"image_size,omitempty"`
	Size        string         `json:"size,omitempty"`
}

// ResponseFormat 是 response_format 参数。
type ResponseFormat struct {
	Type       string         `json:"type,omitempty"`
	JSONSchema *JSONSchema    `json:"json_schema,omitempty"`
}

// JSONSchema 是 json_schema 子对象。
type JSONSchema struct {
	Schema map[string]any `json:"schema,omitempty"`
}

// ThinkingParam 是 thinking 参数（type=enabled/disabled + budget_tokens）。
type ThinkingParam struct {
	Type         string `json:"type,omitempty"`
	BudgetTokens any    `json:"budget_tokens,omitempty"`
}

// Message 是 ChatCompletion 单条消息。Content 支持字符串或内容 part 数组。
type Message struct {
	Role        string          `json:"role"`
	Content     MessageContent  `json:"content"`
	ToolCalls   []OAIToolCall   `json:"tool_calls,omitempty"`
	ToolCallID  string          `json:"tool_call_id,omitempty"`
	Name        string          `json:"name,omitempty"`
}

// MessageContent 双态承载 content：字符串或 []ContentPart。
type MessageContent struct {
	String *string
	Parts  []ContentPart
}

// IsString 返回 content 是否为纯字符串形态。
func (m *MessageContent) IsString() bool { return m.String != nil }

// StringValue 返回字符串形态的值（非字符串形态返回 ""）。
func (m *MessageContent) StringValue() string {
	if m.String != nil {
		return *m.String
	}
	return ""
}

// IsEmpty 表示 content 缺失/为空数组。
func (m *MessageContent) IsEmpty() bool {
	return m.String == nil && len(m.Parts) == 0
}

// UnmarshalJSON 兼容字符串与数组两种 content 形态。
func (m *MessageContent) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		m.String = &s
		return nil
	}
	var parts []ContentPart
	if err := json.Unmarshal(data, &parts); err != nil {
		return err
	}
	m.Parts = parts
	return nil
}

// MarshalJSON 还原字符串或数组形态。
func (m MessageContent) MarshalJSON() ([]byte, error) {
	if m.String != nil {
		return json.Marshal(*m.String)
	}
	if m.Parts == nil {
		return []byte("null"), nil
	}
	return json.Marshal(m.Parts)
}

// ContentPart 是 OpenAI content 数组的单个元素。
type ContentPart struct {
	Type       string  `json:"type,omitempty"`
	Text       string  `json:"text,omitempty"`
	ImageURL   *ImageURLRef `json:"image_url,omitempty"`
	InputImage *ImageURLRef `json:"input_image,omitempty"`
	VideoURL   *ImageURLRef `json:"video_url,omitempty"`
	InputVideo *ImageURLRef `json:"input_video,omitempty"`
	InputAudio *InputAudioRef `json:"input_audio,omitempty"`
	FileURI    string  `json:"fileUri,omitempty"`
	FileURIAlt string  `json:"file_uri,omitempty"`
	URI        string  `json:"uri,omitempty"`
	URL        string  `json:"url,omitempty"`
	MimeType   string  `json:"mimeType,omitempty"`
	MimeTypeAlt string `json:"mime_type,omitempty"`
	Data       string  `json:"data,omitempty"`
	InlineData *InlineDataRef `json:"inlineData,omitempty"`
}

// ImageURLRef 是 image_url / input_image / video_url 的引用（字符串或 {url}）。
type ImageURLRef struct {
	URL string `json:"url,omitempty"`
}

// UnmarshalJSON 兼容字符串与 {url} 两种形态。
func (r *ImageURLRef) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		r.URL = s
		return nil
	}
	var aux struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	r.URL = aux.URL
	return nil
}

// MarshalJSON 还原字符串或 {url} 形态。
func (r ImageURLRef) MarshalJSON() ([]byte, error) {
	if r.URL == "" {
		return []byte("null"), nil
	}
	return json.Marshal(map[string]string{"url": r.URL})
}

// InputAudioRef 是 input_audio 引用（{data, format}）。
type InputAudioRef struct {
	Data   string `json:"data,omitempty"`
	Format string `json:"format,omitempty"`
	URL    string `json:"url,omitempty"`
}

// InlineDataRef 是 inline_data 类型的 part。
type InlineDataRef struct {
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"`
}

// OAITool 是 OpenAI 工具声明（type=function）。
type OAITool struct {
	Type     string        `json:"type,omitempty"`
	Function OAIFunction   `json:"function,omitempty"`
}

// OAIFunction 是 OpenAI function 声明。
type OAIFunction struct {
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// OAIToolCall 是消息里的工具调用。
type OAIToolCall struct {
	ID       string         `json:"id,omitempty"`
	Type     string         `json:"type,omitempty"`
	Function OAIToolCallFn  `json:"function,omitempty"`
}

// OAIToolCallFn 是工具调用的函数名与参数。
type OAIToolCallFn struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// StopSequences 兼容 string 与 []string 的 stop 参数。
type StopSequences []string

// UnmarshalJSON 兼容字符串与数组。
func (s *StopSequences) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '"' {
		var v string
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		*s = []string{v}
		return nil
	}
	var arr []string
	if err := json.Unmarshal(data, &arr); err != nil {
		return err
	}
	*s = arr
	return nil
}

// ChatCompletionResponse 是 OpenAI 非流式响应。
type ChatCompletionResponse struct {
	ID      string   `json:"id,omitempty"`
	Object  string   `json:"object,omitempty"`
	Created int64    `json:"created,omitempty"`
	Model   string   `json:"model,omitempty"`
	Choices []Choice `json:"choices,omitempty"`
	Usage   *Usage   `json:"usage,omitempty"`
}

// Choice 是单个候选。
type Choice struct {
	Index        int             `json:"index"`
	Message      ResponseMessage `json:"message"`
	FinishReason string          `json:"finish_reason,omitempty"`
}

// ResponseMessage 是 assistant 消息（含 reasoning_content）。
type ResponseMessage struct {
	Role             string        `json:"role,omitempty"`
	Content          any           `json:"content"`
	ReasoningContent string        `json:"reasoning_content,omitempty"`
	ToolCalls        []ResponseToolCall `json:"tool_calls,omitempty"`
}

// ResponseToolCall 是响应中的工具调用。
type ResponseToolCall struct {
	Index    int                 `json:"index,omitempty"`
	ID       string              `json:"id,omitempty"`
	Type     string              `json:"type,omitempty"`
	Function ResponseToolCallFn  `json:"function,omitempty"`
}

// ResponseToolCallFn 是工具调用的函数部分。
type ResponseToolCallFn struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// Usage 是 token 用量统计。
type Usage struct {
	PromptTokens     int             `json:"prompt_tokens"`
	CompletionTokens int             `json:"completion_tokens"`
	TotalTokens      int             `json:"total_tokens"`
	PromptDetails    *UsageDetails   `json:"prompt_tokens_details,omitempty"`
	CompletionDetails *UsageDetails  `json:"completion_tokens_details,omitempty"`
}

// UsageDetails 是分类 token 明细。
type UsageDetails struct {
	CachedTokens     int `json:"cached_tokens,omitempty"`
	AudioTokens      int `json:"audio_tokens,omitempty"`
	TextTokens       int `json:"text_tokens,omitempty"`
	ImageTokens      int `json:"image_tokens,omitempty"`
	ReasoningTokens  int `json:"reasoning_tokens,omitempty"`
}

// ChatCompletionChunk 是 OpenAI 流式帧。
type ChatCompletionChunk struct {
	ID      string      `json:"id,omitempty"`
	Object  string      `json:"object,omitempty"`
	Created int64       `json:"created,omitempty"`
	Model   string      `json:"model,omitempty"`
	Choices []ChunkChoice `json:"choices,omitempty"`
	Usage   *Usage      `json:"usage,omitempty"`
}

// ChunkChoice 是流式帧的 choice（delta 承载增量）。
type ChunkChoice struct {
	Index        int              `json:"index"`
	Delta        ResponseMessage  `json:"delta,omitempty"`
	FinishReason any              `json:"finish_reason"`
}