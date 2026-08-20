package transform

import (
	"fmt"
	"sort"
)

var openAIChatFields = map[string]bool{ //nolint:gochecknoglobals
	"model": true, "messages": true, "stream": true, "n": true,
	"temperature": true, "top_p": true, "top_k": true,
	"presence_penalty": true, "frequency_penalty": true,
	"max_tokens": true, "max_completion_tokens": true, "stop": true, "seed": true,
	"response_format": true, "modalities": true, "audio": true,
	"tools": true, "tool_choice": true, "parallel_tool_calls": true,
	"functions": true, "function_call": true,
	"logprobs": true, "top_logprobs": true, "stream_options": true,
	"store": true, "metadata": true, "user": true, "service_tier": true,
	"safety_settings": true, "safetySettings": true, "reasoning_effort": true,
	"thinking": true, "media_resolution": true, "mediaResolution": true,
	"extra_body": true, "size": true, "image_size": true, "imageSize": true,
	"imageConfig": true, "responseModalities": true,
}

func ValidateOpenAIChatFields(body map[string]any) error {
	unknown := make([]string, 0)
	for field := range body {
		if !openAIChatFields[field] {
			unknown = append(unknown, field)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return &PolicyError{
		Code: "unknown_parameter", Param: unknown[0],
		Message: fmt.Sprintf("unsupported or unknown Chat Completions parameter %q", unknown[0]),
	}
}
