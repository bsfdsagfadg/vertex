package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/cli"
	"github.com/bsfdsagfadg/vertex/internal/strutil"
	"github.com/bsfdsagfadg/vertex/internal/toolstate"
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
	cfg := c.requestConfig(r)
	if err := transform.ValidateOpenAIChatFields(body); err != nil {
		var policyErr *transform.PolicyError
		if errors.As(err, &policyErr) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{
				"message": policyErr.Message, "type": "invalid_request_error", "param": policyErr.Param, "code": policyErr.Code,
			}})
			return
		}
	}

	rawModel, _ := body["model"].(string)
	if strings.TrimSpace(rawModel) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{
			"message": "请求参数有误: 缺少必需字段 model (missing required field 'model')",
			"type":    "invalid_request_error", "code": 400, "param": "model",
		}})
		return
	}

	actualModel, useFake, modelOK := resolveConfiguredModel(rawModel, cfg)
	if !modelOK {
		oaiModelNotFound(w, rawModel)
		return
	}
	body["model"] = actualModel
	cli.UpdateReqModel(vertex.RequestIDFromContext(r.Context()), actualModel)

	stream, _ := body["stream"].(bool)
	store, _ := body["store"].(bool)
	if stream && store {
		oaiResourceError(w, http.StatusBadRequest, "unsupported_parameter", "stored chat completions require stream=false so the complete terminal result can be persisted", "store")
		return
	}
	includeUsage := false
	if streamOptions, ok := body["stream_options"].(map[string]any); ok {
		includeUsage, _ = streamOptions["include_usage"].(bool)
	}
	aggregateStream := stream && cfg.AggregateStream()

	model, geminiPayload, convErr := c.reqConv.Convert(body, cfg)
	if convErr != nil {
		var policyErr *transform.PolicyError
		if errors.As(convErr, &policyErr) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{
				"message": policyErr.Message, "type": "invalid_request_error", "param": policyErr.Param, "code": policyErr.Code,
			}})
			return
		}
		oaiError(w, http.StatusBadRequest, "请求参数有误: "+convErr.Error()+" (invalid argument)", "invalid_request_error")
		return
	}
	if c.platform != nil {
		if err := c.platform.expandLocalResources(r.Context(), geminiPayload); err != nil {
			oaiResourceError(w, http.StatusConflict, "state_not_available", err.Error(), nil)
			return
		}
	}
	if c.toolStates != nil {
		if err := c.toolStates.RestoreOpenAIChat(r.Context(), geminiPayload); err != nil {
			c.writeToolStateError(w, err)
			return
		}
	}
	if !applyRequestModelPolicy(w, geminiPayload, actualModel, cfg.OpenAIParameterPolicy(), "openai") {
		return
	}

	n, nErr := resolveN(body["n"], cfg.MaxN())
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
	transform.ApplyImageDefaults(geminiPayload, actualModel, cfg.DefaultImageSize(), cfg.DefaultResponseModalities())

	if aggregateStream {
		w.Header().Set("X-VProxy-Stream-Mode", "simulated")
		c.oaiAggregateStream(r.Context(), w, model, geminiPayload, includeUsage)
		return
	}
	if stream && useFake {
		w.Header().Set("X-VProxy-Stream-Mode", "simulated")
		c.oaiFakeStream(r.Context(), w, model, geminiPayload, includeUsage)
		return
	}

	if stream {
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
		for _, response := range responses {
			transform.AssignExternalFunctionCallIDs(response)
		}
		oaiResp := c.respConv.AggregateN(responses, model)
		responseID, _ := oaiResp["id"].(string)
		for _, response := range responses {
			if err := c.captureToolState(r.Context(), response, responseID); err != nil {
				c.writeToolStateError(w, err)
				return
			}
		}
		if store {
			if err := c.persistStoredCompletion(r.Context(), body, oaiResp, model); err != nil {
				oaiResourceError(w, http.StatusInternalServerError, "resource_store_error", "stored chat completion could not be persisted", nil)
				return
			}
		}
		writeJSON(w, http.StatusOK, oaiResp)
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

	transform.AssignExternalFunctionCallIDs(geminiResp)
	oaiResp := c.respConv.ToOAI(geminiResp, model)
	responseID, _ := oaiResp["id"].(string)
	if err := c.captureToolState(r.Context(), geminiResp, responseID); err != nil {
		c.writeToolStateError(w, err)
		return
	}
	if store {
		if err := c.persistStoredCompletion(r.Context(), body, oaiResp, model); err != nil {
			oaiResourceError(w, http.StatusInternalServerError, "resource_store_error", "stored chat completion could not be persisted", nil)
			return
		}
	}
	writeJSON(w, http.StatusOK, oaiResp)
}

func (c *ChatHandler) captureToolState(ctx context.Context, response map[string]any, responseID string) error {
	if c.toolStates == nil {
		return nil
	}
	return c.toolStates.CaptureResponse(ctx, response, responseID, "", "chat.completions")
}

func (c *ChatHandler) writeToolStateError(w http.ResponseWriter, err error) {
	var protocolErr *toolstate.ProtocolError
	if errors.As(err, &protocolErr) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]any{
			"message": protocolErr.Message, "type": "invalid_request_error", "param": protocolErr.Param, "code": protocolErr.Code,
		}})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{
		"message": "tool state persistence failed", "type": "server_error", "param": nil, "code": "tool_state_store_error",
	}})
}

func (c *ChatHandler) streamChatCompletions(ctx context.Context, w http.ResponseWriter, model string, geminiPayload map[string]any, includeUsage bool) {
	requestID := strutil.ReqID()

	sw := newSSEWriter(w, "text/event-stream")

	isFirst := true
	hasFinish := false
	gotContent := false
	streamErrWritten := false
	disconnected := false
	startTime := time.Now()
	createdAt := startTime.Unix()
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
				c.writeStreamError(sw.write, ch.Err, requestID, model, createdAt)
			}
			streamErrWritten = true
			return false
		}
		if streamChunkHasFinish(ch.Data) {
			hasFinish = true
		}
		events := transform.ConvertRealtimeChunkWithUsageAt(ch.Data, model, requestID, createdAt, isFirst, includeUsage, toolCallTracker)
		isFirst = false
		for _, ev := range events {
			if strings.Contains(ev, `"content":`) || strings.Contains(ev, `"tool_calls":`) || strings.Contains(ev, `"reasoning_content":`) {
				gotContent = true
			}
			if !sw.write(ev) {
				log.Printf("[Server] [Stream] 请求ID=%s 客户端已主动断开连接", requestID)
				disconnected = true
				return false
			}
		}
		return true
	})
	if disconnected {
		return
	}
	if !streamErrWritten {
		captureErr := toolCallTracker.CaptureError()
		if captureErr == nil {
			captureErr = c.captureToolState(ctx, toolCallTracker.CapturedResponse(), "chatcmpl-"+requestID)
		}
		if captureErr != nil {
			c.writeToolStateStreamError(sw.write, captureErr, requestID, model, createdAt)
			streamErrWritten = true
		}
	}

	if streamErrWritten {
		return
	}
	if !gotContent && !hasFinish {
		ee := vertex.NewEmptyResponseError("Upstream returned empty response (no content)")
		if !sw.hasWritten() {
			writeJSON(w, ee.Code, vertexErrorToOAI(ee))
		} else {
			c.writeStreamError(sw.write, ee, requestID, model, createdAt)
		}
		return
	}
	if !hasFinish {
		base := streamChunkBaseAt(model, requestID, createdAt)
		base["choices"] = []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "length"}}
		sw.write(sseEvent(base))
	}
	sw.write("data: [DONE]\n\n")
}

func (c *ChatHandler) writeToolStateStreamError(write func(string) bool, err error, requestID, model string, createdAt ...int64) {
	code := "tool_state_store_error"
	message := "tool state persistence failed"
	param := any(nil)
	var protocolErr *toolstate.ProtocolError
	if errors.As(err, &protocolErr) {
		code = protocolErr.Code
		message = protocolErr.Message
		param = protocolErr.Param
	}
	base := streamChunkBase(model, requestID)
	if len(createdAt) > 0 {
		base["created"] = createdAt[0]
	}
	base["choices"] = []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "error"}}
	base["error"] = map[string]any{"message": message, "type": "server_error", "param": param, "code": code}
	_ = write(sseEvent(base))
	_ = write("data: [DONE]\n\n")
}

func (c *ChatHandler) writeStreamError(write func(string) bool, e *vertex.VertexError, requestID, model string, createdAt ...int64) {
	base := streamChunkBase(model, requestID)
	if len(createdAt) > 0 {
		base["created"] = createdAt[0]
	}
	if isSafetyBlock(e) {
		base["choices"] = []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "content_filter"}}
		_ = write(sseEvent(base))
	} else {
		// SSE headers are already committed once a stream has emitted content.
		// Keep the terminal packet valid for OpenAI clients by including choices
		// with finish_reason=error alongside the normal error object.
		base["choices"] = []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "error"}}
		base["error"] = vertexErrorToOAI(e)["error"]
		_ = write(sseEvent(base))
	}
	_ = write("data: [DONE]\n\n")
}

func streamChunkHasFinish(chunk map[string]any) bool {
	candidates, _ := chunk["candidates"].([]any)
	for _, raw := range candidates {
		candidate, _ := raw.(map[string]any)
		finish, _ := candidate["finishReason"].(string)
		if finish != "" && finish != transform.FinishReasonUnspecified {
			return true
		}
	}
	return false
}

func (c *ChatHandler) oaiFakeStream(ctx context.Context, w http.ResponseWriter, model string, geminiPayload map[string]any, includeUsage bool) {
	requestID := strutil.ReqID()
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

	transform.AssignExternalFunctionCallIDs(resp)
	oai := c.respConv.ToOAI(resp, model)
	responseID, _ := oai["id"].(string)
	if err := c.captureToolState(ctx, resp, responseID); err != nil {
		c.writeToolStateError(w, err)
		return
	}
	contentText := firstChoiceContent(oai)
	toolCalls := firstChoiceToolCalls(oai)

	createdTS := time.Now().Unix()
	chunks := splitIntoRuneChunks(contentText)
	if len(chunks) == 0 {
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
	if includeUsage {
		if usage, ok := oai["usage"].(map[string]any); ok && len(usage) > 0 {
			base := streamChunkBase(model, requestID)
			base["created"] = createdTS
			base["choices"] = []any{}
			base["usage"] = usage
			if !sw.write(sseEvent(base)) {
				return
			}
		}
	}
	_ = sw.write("data: [DONE]\n\n")
}

func (c *ChatHandler) oaiAggregateStream(ctx context.Context, w http.ResponseWriter, model string, geminiPayload map[string]any, includeUsage bool) {
	requestID := strutil.ReqID()
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
	transform.AssignExternalFunctionCallIDs(resp)
	oai := c.respConv.ToOAI(resp, model)
	responseID, _ := oai["id"].(string)
	if err := c.captureToolState(ctx, resp, responseID); err != nil {
		c.writeToolStateError(w, err)
		return
	}
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
	if includeUsage {
		if usage, ok := oai["usage"].(map[string]any); ok && len(usage) > 0 {
			usageEvent := streamChunkBase(model, requestID)
			usageEvent["created"] = createdTS
			usageEvent["choices"] = []any{}
			usageEvent["usage"] = usage
			sw.write(sseEvent(usageEvent))
		}
	}
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
