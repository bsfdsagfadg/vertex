// Package transform 承载请求/响应协议转换的强类型 DTO 基座。
//
// 本文件定义 Gemini REST API 官方规范的强类型结构体（字段来源为官方规范，
// 未做任何字段增删）。所有嵌套类型使用指针 + omitempty，从根上消除
// "旧版序列化补全空默认值污染"（如 text:""、thoughtSignature:""）问题：
// 指针为 nil / 标量为空时天然不序列化。
package transform

// GeminiRequest 对应 generateContent / streamGenerateContent 请求体。
type GeminiRequest struct {
	Contents          []Content        `json:"contents,omitempty"`
	SystemInstruction *Content         `json:"systemInstruction,omitempty"`
	Tools             []Tool           `json:"tools,omitempty"`
	ToolConfig        *ToolConfig      `json:"toolConfig,omitempty"`
	SafetySettings    []SafetySetting  `json:"safetySettings,omitempty"`
	GenerationConfig  *GenerationConfig `json:"generationConfig,omitempty"`
	CachedContent     string           `json:"cachedContent,omitempty"`
	ServiceTier       string           `json:"serviceTier,omitempty"`
	Store             *bool            `json:"store,omitempty"`
}

// Content 是一个对话回合（user / model / function）。
type Content struct {
	Role  string `json:"role"`          // user / model / function
	Parts []Part `json:"parts,omitempty"`
}

// Part 是对话内容块。用指针字段 + omitempty 表达"键是否存在"，
// 彻底取代旧版"值非空"的脏数据判定（见旧 stream.go ExtractParts）。
type Part struct {
	Text                string               `json:"text,omitempty"`
	Thought             bool                 `json:"thought,omitempty"`
	ThoughtSignature    string               `json:"thoughtSignature,omitempty"`
	InlineData          *InlineData          `json:"inlineData,omitempty"`
	FileData            *FileData            `json:"fileData,omitempty"`
	FunctionCall        *FunctionCall        `json:"functionCall,omitempty"`
	FunctionResponse    *FunctionResponse    `json:"functionResponse,omitempty"`
	ExecutableCode      *ExecutableCode      `json:"executableCode,omitempty"`
	CodeExecutionResult *CodeExecutionResult `json:"codeExecutionResult,omitempty"`
	VideoMetadata       any                  `json:"videoMetadata,omitempty"`
	MediaResolution     string               `json:"mediaResolution,omitempty"`
}

// InlineData 是内联媒体的 mimeType + base64 data。
type InlineData struct {
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"`
}

// FileData 是已上传文件的引用。
type FileData struct {
	FileURI  string `json:"fileUri,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// FunctionCall 是模型发起的工具调用。
type FunctionCall struct {
	Name string `json:"name,omitempty"`
	Args any    `json:"args,omitempty"` // 对象/数组/字符串均可，nil 省略
	ID   string `json:"id,omitempty"`
}

// FunctionResponse 是工具执行结果回传。
type FunctionResponse struct {
	Name     string `json:"name,omitempty"`
	Response any    `json:"response,omitempty"` // 对象（含 {"result": ...} 包装）或标量
	ID       string `json:"id,omitempty"`
}

// ExecutableCode 是模型生成的可执行代码块。
type ExecutableCode struct {
	Code         string `json:"code,omitempty"`
	CodeLanguage string `json:"codeLanguage,omitempty"`
}

// CodeExecutionResult 是代码执行结果。
type CodeExecutionResult struct {
	Output        string `json:"output,omitempty"`
	Outcome       string `json:"outcome,omitempty"`
	OutcomeLanguage string `json:"outcomeLanguage,omitempty"`
}

// GenerationConfig 是生成参数（全部可省略）。
type GenerationConfig struct {
	StopSequences     []string        `json:"stopSequences,omitempty"`
	MaxOutputTokens   *int            `json:"maxOutputTokens,omitempty"`
	Temperature       *float64        `json:"temperature,omitempty"`
	TopP              *float64        `json:"topP,omitempty"`
	TopK              *float64        `json:"topK,omitempty"`
	PresencePenalty   *float64        `json:"presencePenalty,omitempty"`
	FrequencyPenalty  *float64        `json:"frequencyPenalty,omitempty"`
	Seed              *int64          `json:"seed,omitempty"`
	Logprobs          *int            `json:"logprobs,omitempty"`
	ResponseLogprobs  *bool           `json:"responseLogprobs,omitempty"`
	ResponseMimeType  string          `json:"responseMimeType,omitempty"`
	ResponseSchema    any             `json:"responseSchema,omitempty"`
	ResponseModalities []string       `json:"responseModalities,omitempty"`
	ThinkingConfig    *ThinkingConfig `json:"thinkingConfig,omitempty"`
	ImageConfig       *ImageConfig    `json:"imageConfig,omitempty"`
	MediaResolution   string          `json:"mediaResolution,omitempty"`
	SpeechConfig      *SpeechConfig   `json:"speechConfig,omitempty"`
	AudioTimestamp    *bool           `json:"audioTimestamp,omitempty"`
	RoutingConfig     *RoutingConfig  `json:"routingConfig,omitempty"`
}

// ThinkingConfig 是思考预算/等级的注入。二者同一时刻至多其一。
type ThinkingConfig struct {
	ThinkingBudget *int    `json:"thinkingBudget,omitempty"`
	ThinkingLevel  string  `json:"thinkingLevel,omitempty"`
}

// ImageConfig 是图像生成配置。
type ImageConfig struct {
	AspectRatio string `json:"aspectRatio,omitempty"`
	ImageSize   string `json:"imageSize,omitempty"`
	ImageType   string `json:"imageType,omitempty"`
}

// SpeechConfig 是 TTS 语音配置。
type SpeechConfig struct {
	VoiceConfig *VoiceConfig `json:"voiceConfig,omitempty"`
}

// VoiceConfig 组合到预置声线。
type VoiceConfig struct {
	PrebuiltVoiceConfig *PrebuiltVoiceConfig `json:"prebuiltVoiceConfig,omitempty"`
}

// PrebuiltVoiceConfig 是指定声线名。
type PrebuiltVoiceConfig struct {
	VoiceName string `json:"voiceName,omitempty"`
}

// RoutingConfig 是路由留空壳（保留协议字段）。
type RoutingConfig struct {
	AutoRoutingMode any `json:"autoRoutingMode,omitempty"`
	ManualRoutingMode any `json:"manualRoutingMode,omitempty"`
}

// SafetySetting 安全设置条目。
type SafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

// Tool 是函数/内置工具声明容器。functionDeclarations 与内置工具可共存。
type Tool struct {
	FunctionDeclarations []FunctionDeclaration `json:"functionDeclarations,omitempty"`
	GoogleSearch         any                   `json:"googleSearch,omitempty"`
	GoogleSearchRetrieval any                  `json:"googleSearchRetrieval,omitempty"`
	CodeExecution        any                   `json:"codeExecution,omitempty"`
	Retrieval            any                   `json:"retrieval,omitempty"`
	URLContext           any                   `json:"urlContext,omitempty"`
	ComputerUse          any                   `json:"computerUse,omitempty"`
	MCPTool              any                   `json:"mcpServer,omitempty"`
	FileSearch           any                   `json:"fileSearch,omitempty"`
	GoogleMaps           any                   `json:"googleMaps,omitempty"`
}

// FunctionDeclaration 单个函数声明（parameters 保留原生 Schema map）。
type FunctionDeclaration struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

// ToolConfig 控制函数调用模式。
type ToolConfig struct {
	FunctionCallingConfig *FunctionCallingConfig `json:"functionCallingConfig,omitempty"`
}

// FunctionCallingConfig 的函数调用模式。
type FunctionCallingConfig struct {
	Mode                 string   `json:"mode,omitempty"` // NONE / AUTO / ANY
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

// GeminiResponse 是 generateContent / streamGenerateContent 响应骨架。
type GeminiResponse struct {
	Candidates      []*Candidate     `json:"candidates,omitempty"`
	PromptFeedback  *PromptFeedback  `json:"promptFeedback,omitempty"`
	UsageMetadata   *UsageMetadata   `json:"usageMetadata,omitempty"`
	ModelVersion    string           `json:"modelVersion,omitempty"`
	CreateTime      string           `json:"createTime,omitempty"`
	ResponseID      string           `json:"responseId,omitempty"`
	ModelStatus     string           `json:"modelStatus,omitempty"`
}

// Candidate 是单个候选（流式单帧通常只有一个）。
type Candidate struct {
	Index            int                `json:"index"`
	Content          *Content           `json:"content,omitempty"`
	FinishReason     string             `json:"finishReason,omitempty"`
	FinishMessage    any                `json:"finishMessage,omitempty"`
	SafetyRatings    []any              `json:"safetyRatings,omitempty"`
	CitationMetadata any                `json:"citationMetadata,omitempty"`
	GroundingMetadata any               `json:"groundingMetadata,omitempty"`
	TokenCount       int                `json:"tokenCount,omitempty"`
	AvgLogprobs      float64            `json:"avgLogprobs,omitempty"`
	LogprobsResult   any                `json:"logprobsResult,omitempty"`
}

// GeminiChunk 是流式单帧，结构上与响应一致（流式通常无 promptFeedback）。
type GeminiChunk = GeminiResponse

// PromptFeedback 是 prompt 级安全/拦截反馈。
type PromptFeedback struct {
	BlockReason        string `json:"blockReason,omitempty"`
	BlockReasonMessage string `json:"blockReasonMessage,omitempty"`
	SafetyRatings      []any  `json:"safetyRatings,omitempty"`
}

// UsageMetadata 是 token 计数元数据。
type UsageMetadata struct {
	PromptTokenCount        int   `json:"promptTokenCount,omitempty"`
	CandidatesTokenCount    int   `json:"candidatesTokenCount,omitempty"`
	TotalTokenCount         int   `json:"totalTokenCount,omitempty"`
	CachedContentTokenCount int   `json:"cachedContentTokenCount,omitempty"`
	ThoughtsTokenCount      int   `json:"thoughtsTokenCount,omitempty"`
	ToolUsePromptTokenCount int   `json:"toolUsePromptTokenCount,omitempty"`
	PromptTokensDetails     []any `json:"promptTokensDetails,omitempty"`
	CandidatesTokensDetails []any `json:"candidatesTokensDetails,omitempty"`
}

// AudioChunk 是 TTS 音频增量块：采样率为一等字段，不从 MimeType 子串重复猜测。
type AudioChunk struct {
	Data       []byte
	SampleRate int
	MIME       string
}

// AudioData 是从响应抽出的整段音频缓冲。
type AudioData struct {
	Bytes      []byte
	SampleRate int
	MIME       string
}