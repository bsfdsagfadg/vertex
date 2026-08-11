package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/cli"
	"github.com/bsfdsagfadg/vertex/internal/transform"
	"github.com/bsfdsagfadg/vertex/internal/vertex"
)

// ChatHandler 是 OpenAI /v1/chat/completions 入口。汇聚点全链路已强类型化：
// 入站解码为 *transform.ChatCompletionRequest，经 TextAdaptor 转为 *GeminiRequest，
// 由 coreGenerateTyped / coreStreamGenerateTyped 统一调度（家族路由 + 策略增强 +
// typed 提交通道），响应侧经 TextAdaptor 还原为 OpenAI DTO。
type ChatHandler struct {
	handler
	adaptor *transform.TextAdaptor
}

// handleChatCompletions 处理 OpenAI Chat Completions 请求。
func (c *ChatHandler) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
		return
	}

	var body transform.ChatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		if _, ok := err.(*json.SyntaxError); ok && strings.Contains(err.Error(), "invalid UTF-8") {
			oaiError(w, http.StatusBadRequest, "请求体编码错误，需为 UTF-8 (request body must be UTF-8 encoded)", "invalid_request_error")
			return
		}
		oaiError(w, http.StatusBadRequest, "请求格式错误，JSON 解析失败 (invalid JSON)", "invalid_request_error")
		return
	}

	rawModel := strings.TrimSpace(body.Model)
	if rawModel == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{
			"message": "请求参数有误: 缺少必需字段 model (missing required field 'model')",
			"type":    "invalid_request_error", "code": 400, "param": "model",
		}})
		return
	}

	actualModel, useFake := stripFakePrefix(rawModel, c.cfg.FakePrefixes())
	body.Model = actualModel
	cli.UpdateReqModel(vertex.RequestIDFromContext(r.Context()), actualModel)

	stream := body.Stream
	aggregateStream := stream && c.cfg.AggregateStream()

	geminiReq, modelName, convErr := c.adaptor.ToGeminiRequest(&body, c.cfg)
	if convErr != nil {
		oaiError(w, http.StatusBadRequest, "请求参数有误: "+convErr.Error()+" (invalid argument)", "invalid_request_error")
		return
	}

	n, nErr := resolveN(body.N, c.cfg.MaxN())
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

	requestCtx, cancel := context.WithTimeout(r.Context(), time.Duration(c.cfg.RequestTimeoutSeconds())*time.Second)
	defer cancel()

	log.Printf("[Server] [ChatCompletions] 收到请求: 模型=%s, 真模型=%s, 流式=%v, n=%d", rawModel, actualModel, stream, n)

	if aggregateStream || (stream && useFake) {
		requestID := reqID24()
		sw := newSSEWriter(w, "text/event-stream")
		resp, ve := c.coreGenerateTyped(requestCtx, rawModel, geminiReq)
		if ve != nil {
			if !sw.hasWritten() {
				writeJSON(w, ve.Code, vertexErrorToOAI(ve))
				return
			}
			c.writeStreamError(sw.write, ve, requestID, modelName)
			return
		}
		oai := c.adaptor.FromGeminiResponse(resp, modelName)
		c.oaiSinglePacketSSE(sw, oai, modelName, requestID)
		return
	}

	if stream {
		c.streamChatCompletionsCore(requestCtx, w, modelName, geminiReq)
		return
	}

	if n > 1 {
		type coreResult struct {
			resp *transform.GeminiResponse
			err  *vertex.VertexError
		}
		results := make([]coreResult, n)
		var wg sync.WaitGroup
		wg.Add(n)
		for i := 0; i < n; i++ {
			go func(idx int) {
				defer wg.Done()
				defer func() {
					if rec := recover(); rec != nil {
						results[idx] = coreResult{err: vertex.NewInternalError(fmt.Sprintf("candidate panic: %v", rec), nil)}
					}
				}()
				r, ve := c.coreGenerateTyped(requestCtx, rawModel, geminiReq)
				results[idx] = coreResult{resp: r, err: ve}
			}(i)
		}
		wg.Wait()

		var ok []*transform.GeminiResponse
		var firstErr *vertex.VertexError
		for _, res := range results {
			if res.err != nil {
				if firstErr == nil {
					firstErr = res.err
				}
				continue
			}
			ok = append(ok, res.resp)
		}
		if len(ok) == 0 {
			if firstErr == nil {
				firstErr = vertex.NewInternalError("All candidates failed", nil)
			}
			if isSafetyBlock(firstErr) {
				log.Printf("[Vertex] 请求被 Google 安全审查拦截, 请求ID=%s, 原因: %s", vertex.RequestIDFromContext(r.Context()), firstErr.Status)
				writeJSON(w, http.StatusOK, oaiSafetyResponse(modelName))
				return
			}
			writeJSON(w, firstErr.Code, vertexErrorToOAI(firstErr))
			return
		}
		writeJSON(w, http.StatusOK, c.adaptor.AggregateN(ok, modelName))
		return
	}

	geminiResp, ve := c.coreGenerateTyped(requestCtx, rawModel, geminiReq)
	if ve != nil {
		if isSafetyBlock(ve) {
			log.Printf("[Vertex] 请求被 Google 安全审查拦截, 请求ID=%s, 原因: %s", vertex.RequestIDFromContext(r.Context()), ve.Status)
			writeJSON(w, http.StatusOK, oaiSafetyResponse(modelName))
			return
		}
		writeJSON(w, ve.Code, vertexErrorToOAI(ve))
		return
	}

	oaiResp := c.adaptor.FromGeminiResponse(geminiResp, modelName)
	writeJSON(w, http.StatusOK, oaiResp)
}

// streamChatCompletionsCore 强类型真流式核心：回调收到 typed GeminiChunk，
// 经 TextAdaptor 转 OAI SSE 行并写出；控制流（断开、finish、缺 finish 补 length)
// 语义与旧 map 实现保持完全一致。
func (c *ChatHandler) streamChatCompletionsCore(ctx context.Context, w http.ResponseWriter, model string, geminiReq *transform.GeminiRequest) {
	rid := vertex.RequestIDFromContext(ctx)
	if rid == "" {
		rid = reqID24()
	}
	sseID := reqID24()

	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()

	sw := newSSEWriter(w, "text/event-stream")

	write := func(line string) bool {
		if !sw.write(line) {
			log.Printf("[Server] [Stream] 请求ID=%s 客户端已主动断开连接", rid)
			streamCancel()
			return false
		}
		return true
	}

	hasFinish := false
	streamErrWritten := false
	startTime := time.Now()
	observer := newStreamObserver(startTime)
	isFirst := true
	toolCallTracker := transform.NewStreamToolCallTracker()

	c.coreStreamGenerateTyped(streamCtx, model, geminiReq, func(chunk *transform.GeminiChunk, err *vertex.VertexError) bool {
		if err != nil {
			if !sw.hasWritten() {
				writeJSON(w, err.Code, vertexErrorToOAI(err))
				streamErrWritten = true
				return false
			}
			c.writeStreamError(write, err, rid, model)
			streamErrWritten = true
			return false
		}
		events := c.adaptor.FromGeminiChunk(chunk, model, sseID, isFirst, toolCallTracker)
		observer.observeTyped(ctx, events)
		for _, ev := range events {
			if ev == "" {
				continue
			}
			isFirst = false
			if strings.Contains(ev, `"finish_reason"`) && !strings.Contains(ev, `"finish_reason":null`) {
				hasFinish = true
			}
			write(ev)
		}
		return true
	})

	if streamErrWritten {
		return
	}
	if !hasFinish {
		finishReason := "stop"
		if toolCallTracker.HasCalls() {
			finishReason = "tool_calls"
		}
		base := streamChunkBase(model, sseID)
		base["choices"] = []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finishReason}}
		_ = sw.write(sseEvent(base))
	}
	_ = sw.write("data: [DONE]\n\n")
}

// writeStreamError 写流式中途错误 SSE 包。write 为真流式回调写入器。
func (c *ChatHandler) writeStreamError(write func(string) bool, e *vertex.VertexError, requestID, model string) {
	if isSafetyBlock(e) {
		base := streamChunkBase(model, requestID)
		base["choices"] = []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "content_filter"}}
		_ = write(sseEvent(base))
	} else {
		// 流式中途报错：SSE 头已发送，只能携带合规 choices + finish_reason=error。
		base := streamChunkBase(model, requestID)
		base["choices"] = []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "error"}}
		base["error"] = vertexErrorToOAI(e)["error"]
		_ = write(sseEvent(base))
	}
	_ = write("data: [DONE]\n\n")
}

// oaiSinglePacketSSE 把完整 OAI 响应转为单包 SSE 事件 + [DONE]。
// oai 为 *transform.ChatCompletionResponse（TextAdaptor 输出）。
func (c *ChatHandler) oaiSinglePacketSSE(sw *sseWriter, oai any, model, requestID string) {
	base := streamChunkBase(model, requestID)

	oaiResp, ok := oai.(*transform.ChatCompletionResponse)
	if !ok || len(oaiResp.Choices) == 0 {
		_ = sw.write(sseEvent(base))
		_ = sw.write("data: [DONE]\n\n")
		return
	}

	ch := oaiResp.Choices[0]
	msg := ch.Message
	delta := map[string]any{}
	if msg.Role != "" {
		delta["role"] = msg.Role
	}
	if msg.Content != nil {
		delta["content"] = msg.Content
	}
	if len(msg.ToolCalls) > 0 {
		delta["tool_calls"] = msg.ToolCalls
	}
	if msg.ReasoningContent != "" {
		delta["reasoning_content"] = msg.ReasoningContent
	}

	choice := map[string]any{"index": 0, "delta": delta}
	if ch.FinishReason != "" {
		choice["finish_reason"] = ch.FinishReason
	}
	base["choices"] = []any{choice}

	if oaiResp.Usage != nil {
		base["usage"] = oaiResp.Usage
	}
	_ = sw.write(sseEvent(base))
	_ = sw.write("data: [DONE]\n\n")
}
