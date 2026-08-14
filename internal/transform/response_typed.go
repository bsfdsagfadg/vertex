package transform

import (
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/jsonx"
)

// 本文件是强类型响应转换核心：GeminiResponse/GeminiChunk -> OpenAI 形态。
// 与 response.go（旧 map 版）并存；旧版作为死代码保留至步骤 9 切换后删除。

// respID 生成响应/调用 ID（24 位十六进制）。
func respID() string { return reqID() }

// SplitResponseParts 从 Gemini parts 调取 (text, reasoning, toolCalls, images)。
// 类型判定依赖指针字段是否存在（非空值），从根本上杜绝"空默认值"脏数据误判。
func SplitResponseParts(parts []Part) (text, reasoning string, toolCalls []ResponseToolCall, images []string) {
	var texts, thoughts []string

	for _, p := range parts {
		switch {
		case p.FunctionCall != nil && p.FunctionCall.Name != "":
			args := p.FunctionCall.Args
			if args == nil {
				args = map[string]any{}
			}
			argBytes, err := jsonx.Marshal(args)
			if err != nil {
				argBytes = []byte("{}")
			}
			toolCalls = append(toolCalls, ResponseToolCall{
				Type: "function",
				Function: ResponseToolCallFn{
					Name:      p.FunctionCall.Name,
					Arguments: string(argBytes),
				},
			})
		case p.InlineData != nil && p.InlineData.Data != "":
			mime := strings.ToLower(strings.TrimSpace(p.InlineData.MimeType))
			if mime == "" {
				mime = "image/png"
			}
			if strings.HasPrefix(mime, "image/") {
				images = append(images, "\n![image](data:"+mime+";base64,"+p.InlineData.Data+")")
			}
		case p.Thought && p.Text != "":
			thoughts = append(thoughts, p.Text)
		case p.Text != "":
			texts = append(texts, p.Text)
		case p.ExecutableCode != nil && p.ExecutableCode.Code != "":
			lang := strings.ToLower(p.ExecutableCode.CodeLanguage)
			texts = append(texts, "```"+lang+"\n"+p.ExecutableCode.Code+"\n```")
		case p.CodeExecutionResult != nil && p.CodeExecutionResult.Output != "":
			texts = append(texts, "```output\n"+p.CodeExecutionResult.Output+"\n```")
		}
	}
	return strings.Join(texts, "") + strings.Join(images, ""), strings.Join(thoughts, ""), toolCalls, images
}

// firstCandidateTyped 取响应第一候选。
func firstCandidateTyped(resp *GeminiResponse) (*Candidate, bool) {
	if resp == nil {
		return nil, false
	}
	for _, c := range resp.Candidates {
		if c != nil {
			return c, true
		}
	}
	return nil, false
}

// candidatePartsTyped 取候选的 parts 切片。
func candidatePartsTyped(c *Candidate) []Part {
	if c == nil || c.Content == nil {
		return nil
	}
	return c.Content.Parts
}

// toolCallID 生成流式工具调用 ID。
func toolCallID(name string) string { return "call_" + respID() }

// AssignToolCallIDs 给非流式工具调用补齐稳定 id/index。
func AssignToolCallIDs(toolCalls []ResponseToolCall) {
	for i := range toolCalls {
		tc := &toolCalls[i]
		if tc.Index == 0 && i > 0 {
			tc.Index = i
		}
		if tc.ID == "" {
			tc.ID = toolCallID(tc.Function.Name)
		}
	}
}

// BuildChatCompletion 构建 OpenAI 非流式响应对象。
func BuildChatCompletion(resp *GeminiResponse, model string) *ChatCompletionResponse {
	result := &ChatCompletionResponse{
		ID:      "chatcmpl-" + respID(),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []Choice{},
	}
	c, ok := firstCandidateTyped(resp)
	text, reasoning, toolCalls, _ := SplitResponseParts(candidatePartsTyped(c))

	if ok && c.GroundingMetadata != nil {
		text = ConvertCitationsToMarkdown(text, c.GroundingMetadata)
	}

	msg := ResponseMessage{Role: "assistant", Content: nil}
	if text != "" {
		msg.Content = text
	}
	if reasoning != "" {
		msg.ReasoningContent = reasoning
	}
	if len(toolCalls) > 0 {
		AssignToolCallIDs(toolCalls)
		msg.ToolCalls = toolCalls
	}

	var finish string
	if ok {
		finish = c.FinishReason
	}
	oaiFinish := MapFinishReason(finish, len(toolCalls) > 0)
	if finish == "" && len(toolCalls) > 0 {
		oaiFinish = "tool_calls"
	}
	result.Choices = append(result.Choices, Choice{Index: 0, Message: msg, FinishReason: oaiFinish})
	if usage := resp.UsageMetadata; usage != nil {
		result.Usage = ConvertUsageTyped(usage)
	}
	return result
}

// AggregateChatCompletions 聚合 N 个 Gemini 响应为多 choice OAI 响应。
func AggregateChatCompletions(resps []*GeminiResponse, model string) *ChatCompletionResponse {
	result := &ChatCompletionResponse{
		ID:      "chatcmpl-" + respID(),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []Choice{},
	}
	var totalPrompt, totalCompletion, totalTokens int
	anyUsage := false
	for idx, resp := range resps {
		c, ok := firstCandidateTyped(resp)
		text, reasoning, toolCalls, _ := SplitResponseParts(candidatePartsTyped(c))
		if ok && c.GroundingMetadata != nil {
			text = ConvertCitationsToMarkdown(text, c.GroundingMetadata)
		}
		msg := ResponseMessage{Role: "assistant", Content: nil}
		if text != "" {
			msg.Content = text
		}
		if reasoning != "" {
			msg.ReasoningContent = reasoning
		}
		if len(toolCalls) > 0 {
			AssignToolCallIDs(toolCalls)
			msg.ToolCalls = toolCalls
		}
		var finish string
		if ok {
			finish = c.FinishReason
		}
		oaiFinish := MapFinishReason(finish, len(toolCalls) > 0)
		if finish == "" && len(toolCalls) > 0 {
			oaiFinish = "tool_calls"
		}
		result.Choices = append(result.Choices, Choice{Index: idx, Message: msg, FinishReason: oaiFinish})
		if usage := resp.UsageMetadata; usage != nil {
			anyUsage = true
			u := ConvertUsageTyped(usage)
			totalPrompt += u.PromptTokens
			totalCompletion += u.CompletionTokens
			totalTokens += u.TotalTokens
		}
	}
	if anyUsage {
		if totalTokens == 0 {
			totalTokens = totalPrompt + totalCompletion
		}
		result.Usage = &Usage{PromptTokens: totalPrompt, CompletionTokens: totalCompletion, TotalTokens: totalTokens}
	}
	return result
}

// ConvertUsageTyped 把 typed usageMetadata 转 OpenAI usage。
func ConvertUsageTyped(meta *UsageMetadata) *Usage {
	u := &Usage{
		PromptTokens:     meta.PromptTokenCount + meta.ToolUsePromptTokenCount,
		CompletionTokens: meta.CandidatesTokenCount + meta.ThoughtsTokenCount,
	}
	if meta.TotalTokenCount > 0 {
		u.TotalTokens = meta.TotalTokenCount
	} else {
		u.TotalTokens = u.PromptTokens + u.CompletionTokens
	}

	pd := &UsageDetails{}
	if meta.CachedContentTokenCount > 0 {
		pd.CachedTokens = meta.CachedContentTokenCount
	}
	for _, d := range asMapSlice(meta.PromptTokensDetails) {
		count := numOf(d["tokenCount"])
		switch toString(d["modality"]) {
		case "AUDIO":
			pd.AudioTokens += count
		case "TEXT":
			pd.TextTokens += count
		}
	}
	if pd.AudioTokens > 0 || pd.CachedTokens > 0 || pd.TextTokens > 0 {
		u.PromptDetails = pd
	}

	cd := &UsageDetails{}
	if meta.ThoughtsTokenCount > 0 {
		cd.ReasoningTokens = meta.ThoughtsTokenCount
	}
	for _, d := range asMapSlice(meta.CandidatesTokensDetails) {
		count := numOf(d["tokenCount"])
		switch toString(d["modality"]) {
		case "IMAGE":
			cd.ImageTokens += count
		case "AUDIO":
			cd.AudioTokens += count
		case "TEXT":
			cd.TextTokens += count
		}
	}
	if cd.ImageTokens > 0 || cd.AudioTokens > 0 || cd.TextTokens > 0 || cd.ReasoningTokens > 0 {
		u.CompletionDetails = cd
	}
	return u
}

// sseLineTyped 把对象序列化成 SSE 数据行。
func sseLineTyped(v any) string {
	data, err := jsonx.Marshal(v)
	if err != nil {
		return ""
	}
	return "data: " + string(data) + "\n\n"
}

// chunkToSSE 把流式 chunk 转 SSE 行。
func chunkToSSE(chunk *ChatCompletionChunk) string { return sseLineTyped(chunk) }

// chunkBase 构建流式帧公共头。
func chunkBase(model, requestID string, created int64) *ChatCompletionChunk {
	return &ChatCompletionChunk{
		ID:      "chatcmpl-" + requestID,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
	}
}