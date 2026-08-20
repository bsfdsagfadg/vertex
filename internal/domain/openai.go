package domain

import (
	"encoding/json"
)

// ChatCompletionRequest represents an OpenAI-compatible chat completion request.
type ChatCompletionRequest struct {
	Model               string          `json:"model"`
	Messages            []ChatMessage   `json:"messages"`
	Tools               []Tool          `json:"tools,omitempty"`
	ToolChoice          any             `json:"tool_choice,omitempty"` // string ("auto", "none", "required") or map/struct
	Temperature         *float64        `json:"temperature,omitempty"`
	TopP                *float64        `json:"top_p,omitempty"`
	TopK                *int            `json:"top_k,omitempty"`
	N                   *int            `json:"n,omitempty"`
	Stream              bool            `json:"stream,omitempty"`
	StreamOptions       *StreamOptions  `json:"stream_options,omitempty"`
	Stop                any             `json:"stop,omitempty"` // string or []string
	MaxTokens           *int            `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int            `json:"max_completion_tokens,omitempty"`
	PresencePenalty     *float64        `json:"presence_penalty,omitempty"`
	FrequencyPenalty    *float64        `json:"frequency_penalty,omitempty"`
	Seed                *int64          `json:"seed,omitempty"`
	ResponseFormat      *ResponseFormat `json:"response_format,omitempty"`
	User                string          `json:"user,omitempty"`
	SafetySettings      []SafetySetting `json:"safety_settings,omitempty"`
	MediaResolution     string          `json:"media_resolution,omitempty"`
	ReasoningEffort     string          `json:"reasoning_effort,omitempty"`
	Thinking            *ThinkingConfig `json:"thinking,omitempty"`
	Logprobs            *bool           `json:"logprobs,omitempty"`
	TopLogprobs         *int            `json:"top_logprobs,omitempty"`
	Modalities          []string        `json:"modalities,omitempty"`
	Audio               *AudioConfig    `json:"audio,omitempty"`
	ExtraBody           map[string]any  `json:"extra_body,omitempty"`
}

// StreamOptions controls streaming behavior (e.g. include_usage).
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

// AudioConfig holds optional audio output specifications.
type AudioConfig struct {
	Voice  string `json:"voice,omitempty"`
	Format string `json:"format,omitempty"`
}

// ResponseFormat defines structured output schema or json mode.
type ResponseFormat struct {
	Type       string      `json:"type"` // "text", "json_object", "json_schema"
	JSONSchema *JSONSchema `json:"json_schema,omitempty"`
}

// JSONSchema holds the schema definition for structured responses.
type JSONSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Schema      map[string]any `json:"schema,omitempty"`
	Strict      *bool          `json:"strict,omitempty"`
}

// ChatMessage represents a single message in an OpenAI chat conversation.
type ChatMessage struct {
	Role             string         `json:"role"` // "system", "user", "assistant", "tool", "function", "developer"
	Content          any            `json:"content,omitempty"` // string or []MessageContentPart
	Name             string         `json:"name,omitempty"`
	ToolCalls        []ToolCall     `json:"tool_calls,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
	FunctionCall     *FunctionCall  `json:"function_call,omitempty"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	Audio            *AudioData     `json:"audio,omitempty"`
}

// AudioData represents incoming or outgoing audio metadata.
type AudioData struct {
	ID        string `json:"id,omitempty"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
	Data      string `json:"data,omitempty"`
	Transcript string `json:"transcript,omitempty"`
}

// MessageContentPart represents a multimodal part in user or assistant content.
type MessageContentPart struct {
	Type       string          `json:"type"` // "text", "image_url", "input_audio", "input_file"
	Text       string          `json:"text,omitempty"`
	ImageURL   *ImageURLPart   `json:"image_url,omitempty"`
	InputAudio *InputAudioPart `json:"input_audio,omitempty"`
}

// ImageURLPart represents an image reference (data URI or remote HTTP URL).
type ImageURLPart struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"` // "auto", "low", "high"
}

// InputAudioPart represents an inlined audio input.
type InputAudioPart struct {
	Data   string `json:"data"`   // Base64-encoded audio data
	Format string `json:"format"` // "wav", "mp3", etc.
}

// Tool defines an available tool that the model may call.
type Tool struct {
	Type     string              `json:"type"` // "function"
	Function FunctionDeclaration `json:"function"`
}

// FunctionCall represents an invocation of a named function.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string representation
}

// ToolCall represents a tool execution request by the model.
type ToolCall struct {
	Index    *int         `json:"index,omitempty"`
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function FunctionCall `json:"function"`
}

// Usage represents token usage statistics in OpenAI format.
type Usage struct {
	PromptTokens            int                      `json:"prompt_tokens"`
	CompletionTokens        int                      `json:"completion_tokens"`
	TotalTokens             int                      `json:"total_tokens"`
	PromptTokensDetails     *PromptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *CompletionTokensDetails `json:"completion_tokens_details,omitempty"`
}

// PromptTokensDetails provides token breakdown for input.
type PromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
	AudioTokens  int `json:"audio_tokens,omitempty"`
}

// CompletionTokensDetails provides token breakdown for output.
type CompletionTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
	AudioTokens     int `json:"audio_tokens,omitempty"`
}

// ChatCompletionChoice represents a candidate choice in a completion response.
type ChatCompletionChoice struct {
	Index        int          `json:"index"`
	Message      ChatMessage  `json:"message"`
	FinishReason string       `json:"finish_reason"` // "stop", "length", "tool_calls", "content_filter"
	Logprobs     *LogprobInfo `json:"logprobs,omitempty"`
}

// LogprobInfo represents log probabilities metadata.
type LogprobInfo struct {
	Content []LogprobContent `json:"content,omitempty"`
}

// LogprobContent represents log probability of a single token.
type LogprobContent struct {
	Token   string  `json:"token"`
	Logprob float64 `json:"logprob"`
	Bytes   []byte  `json:"bytes,omitempty"`
}

// ChatCompletionResponse represents a complete non-streaming OpenAI chat response.
type ChatCompletionResponse struct {
	ID                string                 `json:"id"`
	Object            string                 `json:"object"` // "chat.completion"
	Created           int64                  `json:"created"`
	Model             string                 `json:"model"`
	SystemFingerprint string                 `json:"system_fingerprint,omitempty"`
	Choices           []ChatCompletionChoice `json:"choices"`
	Usage             *Usage                 `json:"usage,omitempty"`
}

// ChatCompletionChunk represents a single chunk in an OpenAI SSE stream.
type ChatCompletionChunk struct {
	ID                string                     `json:"id"`
	Object            string                     `json:"object"` // "chat.completion.chunk"
	Created           int64                      `json:"created"`
	Model             string                     `json:"model"`
	SystemFingerprint string                     `json:"system_fingerprint,omitempty"`
	Choices           []ChatCompletionChunkChoice `json:"choices"`
	Usage             *Usage                     `json:"usage,omitempty"`
}

// ChatCompletionChunkChoice represents a choice delta in a streaming chunk.
type ChatCompletionChunkChoice struct {
	Index        int              `json:"index"`
	Delta        StreamChunkDelta `json:"delta"`
	FinishReason *string          `json:"finish_reason"`
	Logprobs     *LogprobInfo     `json:"logprobs,omitempty"`
}

// StreamChunkDelta holds the incremental content or tool calls in a streaming chunk.
type StreamChunkDelta struct {
	Role             string     `json:"role,omitempty"`
	Content          string     `json:"content,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
}

// RawMessageToMap helper for json unmarshaling.
func RawMessageToMap(data []byte) (map[string]any, error) {
	var m map[string]any
	err := json.Unmarshal(data, &m)
	return m, err
}
