package transform

import (
	"time"
)

// GeminiJSONToOAIJSON 把 Gemini 非流式响应转为 OpenAI ChatCompletion JSON。
func GeminiJSONToOAIJSON(geminiResp map[string]any, model string) map[string]any {
	candidates := responseCandidates(geminiResp)
	choices := make([]any, 0, len(candidates))
	for index, candidate := range candidates {
		choices = append(choices, candidateToOAIChoice(candidate, index))
	}

	result := map[string]any{
		"id":      "chatcmpl-" + reqID(),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": choices,
	}
	if usageMeta, ok := geminiResp["usageMetadata"].(map[string]any); ok {
		result["usage"] = ConvertUsage(usageMeta)
	}
	return result
}

// GeminiResponsesToOAIJSON 把 N 个 Gemini 非流式响应聚合成一个含 N 个 choice 的 OAI 响应。
func GeminiResponsesToOAIJSON(geminiResponses []map[string]any, model string) map[string]any {
	choices := make([]any, 0, len(geminiResponses))
	totalPrompt, totalCompletion, totalTokens := 0, 0, 0
	anyUsage := false

	for _, resp := range geminiResponses {
		for _, candidate := range responseCandidates(resp) {
			choices = append(choices, candidateToOAIChoice(candidate, len(choices)))
		}

		if usageMeta, ok := resp["usageMetadata"].(map[string]any); ok {
			anyUsage = true
			u := ConvertUsage(usageMeta)
			totalPrompt += numOf(u["prompt_tokens"])
			totalCompletion += numOf(u["completion_tokens"])
			totalTokens += numOf(u["total_tokens"])
		}
	}

	result := map[string]any{
		"id":      "chatcmpl-" + reqID(),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": choices,
	}
	if anyUsage {
		if totalTokens == 0 {
			totalTokens = totalPrompt + totalCompletion
		}
		result["usage"] = map[string]any{
			"prompt_tokens":     totalPrompt,
			"completion_tokens": totalCompletion,
			"total_tokens":      totalTokens,
		}
	}
	return result
}

func responseCandidates(resp map[string]any) []map[string]any {
	rawCandidates, _ := resp["candidates"].([]any)
	candidates := make([]map[string]any, 0, len(rawCandidates))
	for _, rawCandidate := range rawCandidates {
		if candidate, ok := rawCandidate.(map[string]any); ok {
			candidates = append(candidates, candidate)
		}
	}
	return candidates
}

func candidateToOAIChoice(candidate map[string]any, index int) map[string]any {
	parts := candidateParts(candidate)
	finish, _ := candidate["finishReason"].(string)
	text, toolCalls, reasoning := ExtractParts(parts, false)

	oaiFinish := "stop"
	if finish != "" {
		oaiFinish = MapFinishReason(finish, len(toolCalls) > 0)
	} else if len(toolCalls) > 0 {
		oaiFinish = "tool_calls"
	}
	message := map[string]any{"role": "assistant", "content": nil}
	if text != "" {
		message["content"] = text
	}
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
	}
	if reasoning != "" {
		message["reasoning_content"] = reasoning
	}
	return map[string]any{
		"index": index, "message": message, "finish_reason": oaiFinish,
	}
}

// ConvertUsage 把 Gemini usageMetadata 转 OpenAI usage。
func ConvertUsage(meta map[string]any) map[string]any {
	prompt := numOf(meta["promptTokenCount"]) + numOf(meta["toolUsePromptTokenCount"])
	completion := numOf(meta["candidatesTokenCount"]) + numOf(meta["thoughtsTokenCount"])
	total := prompt + completion
	if _, ok := meta["totalTokenCount"]; ok {
		total = numOf(meta["totalTokenCount"])
	}
	result := map[string]any{
		"prompt_tokens":     prompt,
		"completion_tokens": completion,
		"total_tokens":      total,
	}

	promptDetails := map[string]any{}
	if c := numOf(meta["cachedContentTokenCount"]); c > 0 {
		promptDetails["cached_tokens"] = c
	}
	for _, d := range asMapSlice(meta["promptTokensDetails"]) {
		count := numOf(d["tokenCount"])
		switch toString(d["modality"]) {
		case "AUDIO":
			promptDetails["audio_tokens"] = numOf(promptDetails["audio_tokens"]) + count
		case "TEXT":
			promptDetails["text_tokens"] = numOf(promptDetails["text_tokens"]) + count
		}
	}
	if len(promptDetails) > 0 {
		result["prompt_tokens_details"] = promptDetails
	}

	completionDetails := map[string]any{}
	if t := numOf(meta["thoughtsTokenCount"]); t > 0 {
		completionDetails["reasoning_tokens"] = t
	}
	for _, d := range asMapSlice(meta["candidatesTokensDetails"]) {
		count := numOf(d["tokenCount"])
		switch toString(d["modality"]) {
		case "IMAGE":
			completionDetails["image_tokens"] = numOf(completionDetails["image_tokens"]) + count
		case "AUDIO":
			completionDetails["audio_tokens"] = numOf(completionDetails["audio_tokens"]) + count
		case "TEXT":
			completionDetails["text_tokens"] = numOf(completionDetails["text_tokens"]) + count
		}
	}
	if len(completionDetails) > 0 {
		result["completion_tokens_details"] = completionDetails
	}

	return result
}
