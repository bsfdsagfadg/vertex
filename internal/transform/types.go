package transform

import (
	"encoding/json"

	"github.com/bsfdsagfadg/vertex/internal/domain"
)

// Aliases for domain types to ensure 100% type compatibility and backward compatibility.
type (
	Part                     = domain.Part
	Blob                     = domain.Blob
	FileData                 = domain.FileData
	FunctionCall             = domain.GeminiFunctionCall
	FunctionResponse         = domain.GeminiFunctionResponse
	Content                  = domain.Content
	Candidate                = domain.Candidate
	SafetyRating             = domain.SafetyRating
	UsageMetadata            = domain.UsageMetadata
	PromptTokenDetail        = domain.PromptTokenDetail
	CandidatesDetail         = domain.CandidatesDetail
	GenerateContentResponse  = domain.GenerateContentResponse
	ResponseError            = domain.ResponseError
	GeminiTool               = domain.GeminiTool
	FunctionDeclaration      = domain.FunctionDeclaration
	ToolConfig               = domain.ToolConfig
	FunctionCallingConfig    = domain.FunctionCallingConfig
	SafetySetting            = domain.SafetySetting
	GenerationConfig         = domain.GenerationConfig
	ThinkingConfig           = domain.ThinkingConfig
	GenerateContentRequest   = domain.GenerateContentRequest
	ChatCompletionRequest    = domain.ChatCompletionRequest
	ChatMessage              = domain.ChatMessage
	MessageContentPart       = domain.MessageContentPart
	ImageURLPart             = domain.ImageURLPart
	InputAudioPart           = domain.InputAudioPart
	Tool                     = domain.Tool
	OpenAIToolCall           = domain.ToolCall
	OpenAIFunctionCall       = domain.FunctionCall
	Usage                    = domain.Usage
	PromptTokensDetails      = domain.PromptTokensDetails
	CompletionTokensDetails  = domain.CompletionTokensDetails
	ChatCompletionResponse   = domain.ChatCompletionResponse
	ChatCompletionChoice     = domain.ChatCompletionChoice
	ChatCompletionChunk      = domain.ChatCompletionChunk
	ChatCompletionChunkChoice = domain.ChatCompletionChunkChoice
	StreamChunkDelta         = domain.StreamChunkDelta
	ResponseFormat           = domain.ResponseFormat
	JSONSchema               = domain.JSONSchema
)

// RawMessageToMap helper for json unmarshaling.
func RawMessageToMap(data []byte) (map[string]any, error) {
	var m map[string]any
	err := json.Unmarshal(data, &m)
	return m, err
}
