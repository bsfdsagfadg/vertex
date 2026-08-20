package domain

import (
	"encoding/json"
)

// GenerateContentRequest represents the payload sent to Google Gemini / Vertex AI.
type GenerateContentRequest struct {
	Contents          []Content          `json:"contents"`
	Tools             []GeminiTool       `json:"tools,omitempty"`
	ToolConfig        *ToolConfig        `json:"toolConfig,omitempty"`
	SafetySettings    []SafetySetting    `json:"safetySettings,omitempty"`
	SystemInstruction *Content           `json:"systemInstruction,omitempty"`
	GenerationConfig  *GenerationConfig  `json:"generationConfig,omitempty"`
	CachedContent     string             `json:"cachedContent,omitempty"`
}

// Content represents a single conversation turn in Gemini.
type Content struct {
	Role  string `json:"role,omitempty"` // "user", "model", "function"
	Parts []Part `json:"parts"`
}

// Part represents an individual component of a Content message.
type Part struct {
	Text                string                  `json:"text,omitempty"`
	Thought             any                     `json:"thought,omitempty"` // bool or string in some contexts
	ThoughtSignature    string                  `json:"thoughtSignature,omitempty"`
	InlineData          *Blob                   `json:"inlineData,omitempty"`
	FileData            *FileData               `json:"fileData,omitempty"`
	FunctionCall        *GeminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse    *GeminiFunctionResponse `json:"functionResponse,omitempty"`
	ExecutableCode      *ExecutableCode         `json:"executableCode,omitempty"`
	CodeExecutionResult *CodeExecutionResult    `json:"codeExecutionResult,omitempty"`
	VideoMetadata       map[string]any          `json:"videoMetadata,omitempty"`
	MediaResolution     string                  `json:"mediaResolution,omitempty"`
}

// Blob represents inlined binary content (Base64).
type Blob struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // Standard Base64 string
}

// FileData represents a remote or uploaded file resource by URI.
type FileData struct {
	MimeType string `json:"mimeType,omitempty"`
	FileURI  string `json:"fileUri"`
}

// GeminiFunctionCall represents a function call request produced by the model.
type GeminiFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
	ID   string         `json:"id,omitempty"`
}

// GeminiFunctionResponse represents the execution result of a function call.
type GeminiFunctionResponse struct {
	Name     string         `json:"name,omitempty"`
	Response map[string]any `json:"response"`
	ID       string         `json:"id,omitempty"`
}

// ExecutableCode represents generated code for code execution.
type ExecutableCode struct {
	Language string `json:"language"`
	Code     string `json:"code"`
}

// CodeExecutionResult represents the execution outcome of generated code.
type CodeExecutionResult struct {
	Outcome string `json:"outcome"`
	Output  string `json:"output,omitempty"`
}

// GeminiTool defines tools available to the model.
type GeminiTool struct {
	FunctionDeclarations []FunctionDeclaration `json:"functionDeclarations,omitempty"`
	CodeExecution        *CodeExecutionOption  `json:"codeExecution,omitempty"`
	GoogleSearch         *GoogleSearchOption   `json:"googleSearch,omitempty"`
}

// FunctionDeclaration describes a callable tool function in Gemini schema.
type FunctionDeclaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Response    map[string]any `json:"response,omitempty"`
}

// CodeExecutionOption enables server-side code execution.
type CodeExecutionOption struct{}

// GoogleSearchOption enables grounding with Google Search.
type GoogleSearchOption struct{}

// ToolConfig specifies mode and choice restrictions for tools.
type ToolConfig struct {
	FunctionCallingConfig *FunctionCallingConfig `json:"functionCallingConfig,omitempty"`
}

// FunctionCallingConfig controls tool invocation behavior.
type FunctionCallingConfig struct {
	Mode                 string   `json:"mode,omitempty"` // "AUTO", "ANY", "NONE"
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

// SafetySetting specifies safety threshold per category.
type SafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

// SafetyRating describes content safety evaluation in candidates.
type SafetyRating struct {
	Category         string  `json:"category"`
	Probability      string  `json:"probability,omitempty"`
	ProbabilityScore float64 `json:"probabilityScore,omitempty"`
	Severity         string  `json:"severity,omitempty"`
	SeverityScore    float64 `json:"severityScore,omitempty"`
	Blocked          bool    `json:"blocked,omitempty"`
}

// GenerationConfig holds decoding and sampling parameters for Gemini.
type GenerationConfig struct {
	Temperature        *float64        `json:"temperature,omitempty"`
	TopP               *float64        `json:"topP,omitempty"`
	TopK               *int            `json:"topK,omitempty"`
	CandidateCount     *int            `json:"candidateCount,omitempty"`
	MaxOutputTokens    *int            `json:"maxOutputTokens,omitempty"`
	StopSequences      []string        `json:"stopSequences,omitempty"`
	PresencePenalty    *float64        `json:"presencePenalty,omitempty"`
	FrequencyPenalty   *float64        `json:"frequencyPenalty,omitempty"`
	ResponseMimeType   string          `json:"responseMimeType,omitempty"`
	ResponseSchema     any             `json:"responseSchema,omitempty"`
	Seed               *int64          `json:"seed,omitempty"`
	ResponseLogprobs   *bool           `json:"responseLogprobs,omitempty"`
	Logprobs           *int            `json:"logprobs,omitempty"`
	ThinkingConfig     *ThinkingConfig `json:"thinkingConfig,omitempty"`
	MediaResolution    string          `json:"mediaResolution,omitempty"`
	ResponseModalities []string        `json:"responseModalities,omitempty"`
}

// ThinkingConfig controls reasoning budget and thinking levels.
type ThinkingConfig struct {
	Type           string `json:"type,omitempty"` // "enabled", "disabled"
	ThinkingBudget *int   `json:"thinkingBudget,omitempty"`
	ThinkingLevel  string `json:"thinkingLevel,omitempty"` // "NONE", "LOW", "MEDIUM", "HIGH"
}

// Candidate represents an individual generated output candidate.
type Candidate struct {
	Index            int              `json:"index,omitempty"`
	Content          *Content         `json:"content,omitempty"`
	FinishReason     string           `json:"finishReason,omitempty"`
	FinishMessage    string           `json:"finishMessage,omitempty"`
	SafetyRatings    []SafetyRating   `json:"safetyRatings,omitempty"`
	CitationMetadata map[string]any   `json:"citationMetadata,omitempty"`
	TokenCount       int              `json:"tokenCount,omitempty"`
	AvgLogprobs      float64          `json:"avgLogprobs,omitempty"`
	LogprobsResult   *LogprobsResult  `json:"logprobsResult,omitempty"`
}

// LogprobsResult holds log probability breakdown for output tokens.
type LogprobsResult struct {
	TopCandidates []TopCandidates `json:"topCandidates,omitempty"`
	ChosenTokens  []ChosenToken   `json:"chosenTokens,omitempty"`
}

// TopCandidates contains top candidate tokens at a position.
type TopCandidates struct {
	Candidates []CandidateToken `json:"candidates,omitempty"`
}

// CandidateToken represents one token probability candidate.
type CandidateToken struct {
	Token          string  `json:"token"`
	TokenID        int     `json:"tokenId,omitempty"`
	LogProbability float64 `json:"logProbability"`
}

// ChosenToken represents the actually sampled token.
type ChosenToken struct {
	Token          string  `json:"token"`
	TokenID        int     `json:"tokenId,omitempty"`
	LogProbability float64 `json:"logProbability"`
}

// UsageMetadata represents token consumption statistics.
type UsageMetadata struct {
	PromptTokenCount        int                 `json:"promptTokenCount,omitempty"`
	CandidatesTokenCount    int                 `json:"candidatesTokenCount,omitempty"`
	TotalTokenCount         int                 `json:"totalTokenCount,omitempty"`
	CachedContentTokenCount int                 `json:"cachedContentTokenCount,omitempty"`
	ToolUsePromptTokenCount int                 `json:"toolUsePromptTokenCount,omitempty"`
	ThoughtsTokenCount      int                 `json:"thoughtsTokenCount,omitempty"`
	PromptTokensDetails     []PromptTokenDetail `json:"promptTokensDetails,omitempty"`
	CandidatesTokensDetails []CandidatesDetail  `json:"candidatesTokensDetails,omitempty"`
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
