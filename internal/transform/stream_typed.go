package transform

import (
	"time"
)

// 本文件是强类型流式转换核心：*GeminiChunk -> OAI SSE 行。
// 与 stream.go（旧 map 版）并存；旧版作为死代码保留至步骤 9 切换后删除。

// ProcessFunctionCallTyped 复用 tracker，按 name 取稳定 (index, callID)。
func ProcessFunctionCallTyped(tracker *StreamToolCallTracker, name string) (int, string) {
	if tracker == nil {
		return 0, "call_" + reqID()
	}
	idx, callID, _ := tracker.ProcessFunctionCall(name)
	return idx, callID
}

// ConvertRealtimeChunkTyped 把单个 Gemini 增量 chunk 转为 OAI SSE 事件行列表。
// tracker 为可选参数，传入时保证 tool_call 的 id/index 跨帧稳定。
func ConvertRealtimeChunkTyped(chunk *GeminiChunk, model, requestID string, isFirst bool, tracker *StreamToolCallTracker) []string {
	if chunk == nil {
		return nil
	}
	if tracker != nil {
		// 每个 chunk 视为独立帧：帧内同名多次出现视为多个独立调用，跨帧按出现次序续打。
		tracker.BeginFrame()
	}
	created := time.Now().Unix()
	var events []string

	c, _ := firstCandidateTyped(chunk)
	text, reasoning, toolCalls := SplitTextParts(candidatePartsTyped(c))
	if c != nil && c.GroundingMetadata != nil {
		text = ConvertCitationsToMarkdown(text, c.GroundingMetadata)
	}
	var finish string
	if c != nil {
		finish = c.FinishReason
	}

	hasRealContent := text != "" || reasoning != "" || len(toolCalls) > 0 || (finish != "" && finish != FinishReasonUnspecified)

	if isFirst && hasRealContent {
		b := chunkBase(model, requestID, created)
		b.Choices = []ChunkChoice{{
			Index:        0,
			Delta:        ResponseMessage{Role: "assistant"},
			FinishReason: nil,
		}}
		events = append(events, chunkToSSE(b))
	}

	if reasoning != "" {
		b := chunkBase(model, requestID, created)
		b.Choices = []ChunkChoice{{
			Index:        0,
			Delta:        ResponseMessage{ReasoningContent: reasoning},
			FinishReason: nil,
		}}
		events = append(events, chunkToSSE(b))
	}
	if text != "" {
		b := chunkBase(model, requestID, created)
		b.Choices = []ChunkChoice{{
			Index:        0,
			Delta:        ResponseMessage{Content: text},
			FinishReason: nil,
		}}
		events = append(events, chunkToSSE(b))
	}
	if len(toolCalls) > 0 {
		deltas := make([]ResponseToolCall, 0, len(toolCalls))
		for _, tc := range toolCalls {
			var isNew bool
			var index int
			var callID string
			if tracker != nil {
				index, callID, isNew = tracker.ProcessFunctionCall(tc.Function.Name)
			} else {
				index = 0
				callID = "call_" + reqID()
				isNew = true
			}
			delta := tc
			delta.Index = index
			// id/type 在首帧与续传帧中保持一致（strict OpenAI SDK 要求增量帧携带稳定 id）。
			delta.ID = callID
			if delta.Type == "" {
				delta.Type = "function"
			}
			if !isNew {
				// 续传帧：仅保留 arguments 增量，name 不重复输出。
				delta.Function = ResponseToolCallFn{
					Arguments: tc.Function.Arguments,
				}
			}
			deltas = append(deltas, delta)
		}
		b := chunkBase(model, requestID, created)
		b.Choices = []ChunkChoice{{
			Index:        0,
			Delta:        ResponseMessage{ToolCalls: deltas},
			FinishReason: nil,
		}}
		events = append(events, chunkToSSE(b))
	}

	if finish != "" && finish != FinishReasonUnspecified {
		finishEvt := chunkBase(model, requestID, created)
		oaiFinish := MapFinishReason(finish, len(toolCalls) > 0)
		if oaiFinish == "" && len(toolCalls) > 0 {
			oaiFinish = "tool_calls"
		}
		finishEvt.Choices = []ChunkChoice{{Index: 0, Delta: ResponseMessage{}, FinishReason: oaiFinish}}
		if chunk.UsageMetadata != nil && chunk.UsageMetadata.TotalTokenCount > 0 {
			finishEvt.Usage = ConvertUsageTyped(chunk.UsageMetadata)
		}
		events = append(events, chunkToSSE(finishEvt))
	}

	return events
}

// SplitTextParts 只提取 (text, reasoning, tool_calls) 三要素。
func SplitTextParts(parts []Part) (string, string, []ResponseToolCall) {
	text, reasoning, toolCalls, _ := SplitResponseParts(parts)
	return text, reasoning, toolCalls
}
