package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/cli"
	"github.com/bsfdsagfadg/vertex/internal/transform"
	"github.com/bsfdsagfadg/vertex/internal/vertex"
)

type ChatHandler struct {
	handler
	reqConv  transform.RequestConverter
	respConv transform.ResponseConverter
}

func (c *ChatHandler) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
		return
	}

	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		if _, ok := err.(*json.SyntaxError); ok && strings.Contains(err.Error(), "invalid UTF-8") {
			oaiError(w, http.StatusBadRequest, "请求体编码错误，需为 UTF-8 (request body must be UTF-8 encoded)", "invalid_request_error")
			return
		}
		oaiError(w, http.StatusBadRequest, "请求格式错误，JSON 解析失败 (invalid JSON)", "invalid_request_error")
		return
	}
	if body == nil {
		body = make(map[string]any)
	}
	if strings.EqualFold(r.Header.Get("X-OpenAI-Strict"), "true") {
		known := map[string]bool{"model":true,"messages":true,"stream":true,"stream_options":true,"n":true,"temperature":true,"top_p":true,"max_tokens":true,"max_completion_tokens":true,"stop":true,"tools":true,"tool_choice":true,"parallel_tool_calls":true,"response_format":true,"reasoning_effort":true,"thinking":true,"media_resolution":true,"safety_settings":true,"safetySettings":true,"user":true,"presence_penalty":true,"frequency_penalty":true,"seed":true,"logprobs":true,"top_logprobs":true,"extra_body":true}
		for k := range body { if !known[k] { oaiError(w, 400, "unknown parameter: "+k, "unsupported_parameter"); return } }
	}

	rawModel, _ := body["model"].(string)
	if strings.TrimSpace(rawModel) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{
			"message": "请求参数有误: 缺少必需字段 model (missing required field 'model')",
			"type":    "invalid_request_error", "code": 400, "param": "model",
		}})
		return
	}

	actualModel, useFake, modelOK := resolveConfiguredModel(rawModel, c.cfg)
	if !modelOK {
		oaiModelNotFound(w, rawModel)
		return
	}
	body["model"] = actualModel
	cli.UpdateReqModel(vertex.RequestIDFromContext(r.Context()), actualModel)

	stream, _ := body["stream"].(bool)
	aggregateStream := stream && c.cfg.AggregateStream()

	model, geminiPayload, convErr := c.reqConv.Convert(body, c.cfg)
	if convErr != nil {
		oaiError(w, http.StatusBadRequest, "请求参数有误: "+convErr.Error()+" (invalid argument)", "invalid_request_error")
		return
	}

	n, nErr := resolveN(body["n"], c.cfg.MaxN())
	if nErr != "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{
			"message": nErr, "type": "invalid_request_error", "code": 400, "param": "n",
		}})
		return
	}
	if stream && n > 1 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{
			"message": "流式不支持 n>1，请设 stream=false 或 n=1 (streaming supports only n=1; set stream=false or n=1 for multiple choices)",
			"type":    "invalid_request_error", "code": 400, "param": "n",
		}})
		return
	}

	log.Printf("[Server] [ChatCompletions] 收到请求: 模型=%s, 真模型=%s, 流式=%v, n=%d", rawModel, actualModel, stream, n)

	transform.ApplyImageConfig(geminiPayload, body, actualModel)
	transform.ApplyImageDefaults(geminiPayload, actualModel, c.cfg.DefaultImageSize(), c.cfg.DefaultResponseModalities())

	if aggregateStream {
		c.oaiAggregateStream(r.Context(), w, model, geminiPayload)
		return
	}
	if stream && useFake {
		c.oaiFakeStream(r.Context(), w, model, geminiPayload)
		return
	}

	if stream {
		includeUsage := false
		if so, ok := body["stream_options"].(map[string]any); ok {
			includeUsage, _ = so["include_usage"].(bool)
		}
		c.streamChatCompletions(r.Context(), w, model, geminiPayload, includeUsage)
		return
	}

	if n > 1 {
		responses, vErr := c.vc.CompleteChatN(r.Context(), model, geminiPayload, n)
		if vErr != nil {
			ve := toVertexError(vErr)
			if isSafetyBlock(ve) {
				log.Printf("[Vertex] 请求被 Google 安全审查拦截, 请求ID=%s, 原因: %s", vertex.RequestIDFromContext(r.Context()), ve.Status)
				writeJSON(w, http.StatusOK, oaiSafetyResponse(model))
				return
			}
			writeJSON(w, ve.Code, vertexErrorToOAI(ve))
			return
		}
		toolCallIDAssigner := transform.NewFunctionCallIDAssigner()
		for _, response := range responses {
			toolCallIDAssigner.Ensure(response)
		}
		writeJSON(w, http.StatusOK, c.respConv.AggregateN(responses, model))
		return
	}

	geminiResp, vErr := c.vc.CompleteChat(r.Context(), model, geminiPayload)
	if vErr != nil {
		ve := toVertexError(vErr)
		if isSafetyBlock(ve) {
			log.Printf("[Vertex] 请求被 Google 安全审查拦截, 请求ID=%s, 原因: %s", vertex.RequestIDFromContext(r.Context()), ve.Status)
			writeJSON(w, http.StatusOK, oaiSafetyResponse(model))
			return
		}
		writeJSON(w, ve.Code, vertexErrorToOAI(ve))
		return
	}

	transform.EnsureFunctionCallIDs(geminiResp)
	oaiResp := c.respConv.ToOAI(geminiResp, model)
	writeJSON(w, http.StatusOK, oaiResp)
}

func (c *ChatHandler) streamChatCompletions(ctx context.Context, w http.ResponseWriter, model string, geminiPayload map[string]any, includeUsage bool) {
	requestID := vertex.RequestIDFromContext(ctx)
	if requestID == "" {
		requestID = reqID24()
	}

	sw := newSSEWriter(w, "text/event-stream")

	isFirst := true
	hasFinish := false
	gotContent := false
	streamErrWritten := false
	startTime := time.Now()
	toolCallTracker := transform.NewStreamToolCallTracker()

	c.vc.StreamChat(ctx, model, geminiPayload, func(ch vertex.StreamChunk) bool {
		if isFirst && ch.Err == nil {
			log.Printf("[Server] [Stream] 请求ID=%s 首字响应耗时: %.2fs", requestID, time.Since(startTime).Seconds())
			cli.UpdateReqState(requestID, "💬 流式打字", "\033[36m", "正在输出...")
		}
		if ch.Err != nil {
			if !sw.hasWritten() {
				ve := toVertexError(ch.Err)
				if isSafetyBlock(ve) {
					log.Printf("[Vertex] 请求被 Google 安全审查拦截, 请求ID=%s, 原因: %s", vertex.RequestIDFromContext(ctx), ve.Status)
					writeJSON(w, http.StatusOK, oaiSafetyResponse(model))
				} else {
					writeJSON(w, ve.Code, vertexErrorToOAI(ve))
				}
			} else {
				c.writeStreamError(sw.write, ch.Err, requestID, model)
			}
			streamErrWritten = true
			return false
		}
		events := c.respConv.StreamToSSE(ch.Data, model, requestID, isFirst, toolCallTracker)
		isFirst = false
		for _, ev := range events {
			if !includeUsage && strings.Contains(ev, `"usage":`) {
				continue
			}
			if strings.Contains(ev, `"finish_reason"`) && !strings.Contains(ev, `"finish_reason":null`) {
				hasFinish = true
			}
			if strings.Contains(ev, `"content":`) || strings.Contains(ev, `"tool_calls":`) || strings.Contains(ev, `"reasoning_content":`) {
				gotContent = true
			}
			if !sw.write(ev) {
				log.Printf("[Server] [Stream] 请求ID=%s 客户端已主动断开连接", requestID)
				return false
			}
		}
		return true
	})

	if streamErrWritten {
		return
	}
	if !gotContent {
		ee := vertex.NewEmptyResponseError("Upstream returned empty response (no content)")
		if !sw.hasWritten() {
			writeJSON(w, ee.Code, vertexErrorToOAI(ee))
		} else {
			c.writeStreamError(sw.write, ee, requestID, model)
		}
		return
	}
	if !hasFinish {
		base := streamChunkBase(model, requestID)
		base["choices"] = []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "length"}}
		sw.write(sseEvent(base))
	}
	sw.write("data: [DONE]\n\n")
}

func (c *ChatHandler) writeStreamError(write func(string) bool, e *vertex.VertexError, requestID, model string) {
	if isSafetyBlock(e) {
		base := streamChunkBase(model, requestID)
		base["choices"] = []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "content_filter"}}
		_ = write(sseEvent(base))
	} else {
		// SSE headers are already committed once a stream has emitted content.
		// Keep the terminal packet valid for OpenAI clients by including choices
		// with finish_reason=error alongside the normal error object.
		base := streamChunkBase(model, requestID)
		base["choices"] = []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "error"}}
		base["error"] = vertexErrorToOAI(e)["error"]
		_ = write(sseEvent(base))
	}
	_ = write("data: [DONE]\n\n")
}

func (c *ChatHandler) oaiFakeStream(ctx context.Context, w http.ResponseWriter, model string, geminiPayload map[string]any) {
	requestID := vertex.RequestIDFromContext(ctx)
	if requestID == "" {
		requestID = reqID24()
	}
	sw := newSSEWriter(w, "text/event-stream")

	resp, vErr := c.vc.CompleteChat(ctx, model, geminiPayload)
	if vErr != nil {
		ve := toVertexError(vErr)
		if !sw.hasWritten() {
			if isSafetyBlock(ve) {
				log.Printf("[Vertex] 请求被 Google 安全审查拦截, 请求ID=%s, 原因: %s", vertex.RequestIDFromContext(ctx), ve.Status)
				writeJSON(w, http.StatusOK, oaiSafetyResponse(model))
				return
			}
			writeJSON(w, ve.Code, vertexErrorToOAI(ve))
			return
		}
		c.writeStreamError(sw.write, ve, requestID, model)
		return
	}

	transform.EnsureFunctionCallIDs(resp)
	oai := c.respConv.ToOAI(resp, model)
	contentText := firstChoiceContent(oai)
	toolCalls := firstChoiceToolCalls(oai)

	createdTS := time.Now().Unix()
	chunks := splitIntoRuneChunks(contentText)
	if len(chunks) == 0 && len(toolCalls) > 0 {
		chunks = []string{""}
	}
	for i, piece := range chunks {
		base := streamChunkBase(model, requestID)
		base["created"] = createdTS
		var delta map[string]any
		if i == 0 {
			delta = map[string]any{"role": "assistant"}
			if piece != "" {
				delta["content"] = piece
			}
		} else {
			delta = map[string]any{"content": piece}
		}
		choice := map[string]any{"index": 0, "delta": delta}
		if i == len(chunks)-1 && len(toolCalls) == 0 {
			choice["finish_reason"] = "stop"
		}
		base["choices"] = []any{choice}
		if !sw.write(sseEvent(base)) {
			return
		}
	}
	for _, toolCall := range toolCalls {
		base := streamChunkBase(model, requestID)
		base["created"] = createdTS
		base["choices"] = []any{map[string]any{
			"index": 0, "delta": map[string]any{"tool_calls": []any{toolCall}}, "finish_reason": nil,
		}}
		if !sw.write(sseEvent(base)) {
			return
		}
	}
	if len(toolCalls) > 0 {
		base := streamChunkBase(model, requestID)
		base["created"] = createdTS
		base["choices"] = []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}}
		if !sw.write(sseEvent(base)) {
			return
		}
	}
	_ = sw.write("data: [DONE]\n\n")
}

func (c *ChatHandler) oaiAggregateStream(ctx context.Context, w http.ResponseWriter, model string, geminiPayload map[string]any) {
	requestID := vertex.RequestIDFromContext(ctx)
	if requestID == "" {
		requestID = reqID24()
	}
	sw := newSSEWriter(w, "text/event-stream")

	resp, vErr := c.vc.CompleteChat(ctx, model, geminiPayload)
	if vErr != nil {
		ve := toVertexError(vErr)
		if !sw.hasWritten() {
			if isSafetyBlock(ve) {
				log.Printf("[Vertex] 请求被 Google 安全审查拦截, 请求ID=%s, 原因: %s", vertex.RequestIDFromContext(ctx), ve.Status)
				writeJSON(w, http.StatusOK, oaiSafetyResponse(model))
				return
			}
			writeJSON(w, ve.Code, vertexErrorToOAI(ve))
			return
		}
		c.writeStreamError(sw.write, ve, requestID, model)
		return
	}
	transform.EnsureFunctionCallIDs(resp)
	oai := c.respConv.ToOAI(resp, model)
	contentText := firstChoiceContent(oai)
	toolCalls := firstChoiceToolCalls(oai)

	createdTS := time.Now().Unix()
	base := streamChunkBase(model, requestID)
	base["created"] = createdTS

	delta := map[string]any{"role": "assistant"}
	if contentText != "" {
		delta["content"] = contentText
	}
	if len(toolCalls) > 0 {
		delta["tool_calls"] = toolCalls
	}
	choice := map[string]any{
		"index": 0,
		"delta": delta,
	}
	base["choices"] = []any{choice}
	if !sw.write(sseEvent(base)) {
		return
	}

	// Stream end
	baseEnd := streamChunkBase(model, requestID)
	baseEnd["created"] = createdTS
	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}
	choiceEnd := map[string]any{
		"index":         0,
		"delta":         map[string]any{},
		"finish_reason": finishReason,
	}
	baseEnd["choices"] = []any{choiceEnd}
	sw.write(sseEvent(baseEnd))
	_ = sw.write("data: [DONE]\n\n")
}

func firstChoiceContent(oai map[string]any) string {
	choices, ok := oai["choices"].([]any)
	if !ok || len(choices) == 0 {
		return ""
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		return ""
	}
	msg, ok := choice["message"].(map[string]any)
	if !ok {
		return ""
	}
	if c, ok2 := msg["content"].(string); ok2 {
		return c
	}
	return ""
}

func firstChoiceToolCalls(oai map[string]any) []any {
	choices, ok := oai["choices"].([]any)
	if !ok || len(choices) == 0 {
		return nil
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		return nil
	}
	message, ok := choice["message"].(map[string]any)
	if !ok {
		return nil
	}
	calls, _ := message["tool_calls"].([]any)
	indexed := make([]any, 0, len(calls))
	for index, rawCall := range calls {
		call, ok := rawCall.(map[string]any)
		if !ok {
			continue
		}
		copyCall := make(map[string]any, len(call)+1)
		for key, value := range call {
			copyCall[key] = value
		}
		copyCall["index"] = index
		indexed = append(indexed, copyCall)
	}
	return indexed
}
