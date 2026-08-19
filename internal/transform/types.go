package transform

import "encoding/json"

// Part represents a component of a Gemini Content message.
// It uses omitempty and pointer fields for optional sub-structures
// to avoid GC boxing pressure while serializing cleanly.
type Part struct {
	Text             string            `json:"text,omitempty"`
	Thought          bool              `json:"thought,omitempty"`
	InlineData       *Blob             `json:"inlineData,omitempty"`
	FunctionCall     *FunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *FunctionResponse `json:"functionResponse,omitempty"`
	FileData         *FileData         `json:"fileData,omitempty"`
	VideoMetadata    map[string]any    `json:"videoMetadata,omitempty"`
}

// Blob represents inlined binary data (e.g. base64 image/audio/video).
type Blob struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

// FileData represents a remote or stored file URI.
type FileData struct {
	MimeType string `json:"mimeType,omitempty"`
	FileURI  string `json:"fileUri"`
}

// FunctionCall represents an invocation of a tool function.
type FunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
	ID   string         `json:"id,omitempty"`
}

// FunctionResponse represents the result of a tool function execution.
type FunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
	ID       string         `json:"id,omitempty"`
}

// Content represents a single turn in a conversation.
type Content struct {
	Role  string `json:"role,omitempty"`
	Parts []Part `json:"parts"`
}

// Candidate represents a generated response candidate from Gemini/Vertex.
type Candidate struct {
	Index         int            `json:"index,omitempty"`
	Content       *Content       `json:"content,omitempty"`
	FinishReason  string         `json:"finishReason,omitempty"`
	FinishMessage string         `json:"finishMessage,omitempty"`
	SafetyRatings []SafetyRating `json:"safetyRatings,omitempty"`
	CitationMeta  map[string]any `json:"citationMetadata,omitempty"`
}

// SafetyRating describes content safety evaluation.
type SafetyRating struct {
	Category    string `json:"category"`
	Probability string `json:"probability,omitempty"`
	Blocked     bool   `json:"blocked,omitempty"`
}

// UsageMetadata represents token consumption statistics.
type UsageMetadata struct {
	PromptTokenCount         int                  `json:"promptTokenCount,omitempty"`
	CandidatesTokenCount     int                  `json:"candidatesTokenCount,omitempty"`
	TotalTokenCount          int                  `json:"totalTokenCount,omitempty"`
	CachedContentTokenCount  int                  `json:"cachedContentTokenCount,omitempty"`
	ToolUsePromptTokenCount  int                  `json:"toolUsePromptTokenCount,omitempty"`
	ThoughtsTokenCount       int                  `json:"thoughtsTokenCount,omitempty"`
	PromptTokensDetails      []PromptTokenDetail  `json:"promptTokensDetails,omitempty"`
	CandidatesTokensDetails  []CandidatesDetail   `json:"candidatesTokensDetails,omitempty"`
}

// PromptTokenDetail gives modality breakdown for prompt tokens.
type PromptTokenDetail struct {
	Modality   string `json:"modality"`
	TokenCount int    `json:"tokenCount"`
}

// CandidatesDetail gives modality breakdown for output tokens.
type CandidatesDetail struct {
	Modality   string `json:"modality"`
	TokenCount int    `json:"tokenCount"`
}

// GenerateContentResponse represents the top-level Gemini/Vertex response structure.
type GenerateContentResponse struct {
	Candidates    []Candidate    `json:"candidates,omitempty"`
	UsageMetadata *UsageMetadata `json:"usageMetadata,omitempty"`
	ModelVersion  string         `json:"modelVersion,omitempty"`
	ResponseID    string         `json:"responseId,omitempty"`
	Error         *ResponseError `json:"error,omitempty"`
}

// ResponseError represents an upstream API error embedded in a payload.
type ResponseError struct {
	Code    int             `json:"code,omitempty"`
	Message string          `json:"message,omitempty"`
	Status  string          `json:"status,omitempty"`
	Details json.RawMessage `json:"details,omitempty"`
}
