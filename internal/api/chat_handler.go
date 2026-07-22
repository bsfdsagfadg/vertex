package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
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

	rawModel, _ := body["model"].(string)
	if strings.TrimSpace(rawModel) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{
			"message": "请求参数有误: 缺少必需字段 model (missing required field 'model')",
			"type":    "invalid_request_error", "code": 400, "param": "model",
		}})
		return
	}

	actualModel, useFake := stripFakePrefix(rawModel, c.cfg.FakePrefixes())
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

	requestCtx, cancel := context.WithTimeout(r.Context(), time.Duration(c.cfg.RequestTimeoutSeconds())*time.Second)
	defer cancel()

	log.Printf("[Server] [ChatCompletions] 收到请求: 模型=%s, 真模型=%s, 流式=%v, n=%d", rawModel, actualModel, stream, n)

	transform.ApplyImageConfig(geminiPayload, body, actualModel)
	if strings.Contains(strings.ToLower(model), "image") {
		gc, ok := geminiPayload["generationConfig"].(map[string]any)
		if !ok {
			gc = map[string]any{}
			geminiPayload["generationConfig"] = gc
		}
		ic, ok := gc["imageConfig"].(map[string]any)
		if !ok {
			ic = map[string]any{}
			gc["imageConfig"] = ic
		}
		if _, has := ic["imageSize"]; !has {
			ic["imageSize"] = transform.ResolveImageSize(c.cfg.DefaultImageSize(), actualModel)
		}
	}

	if aggregateStream || (stream && useFake) {
		requestID := reqID24()
		sw := newSSEWriter(w, "text/event-stream")
		resp, ve := c.coreGenerate(requestCtx, rawModel, geminiPayload)
		if ve != nil {
			c.writeStreamError(sw.write, ve, requestID, model)
			return
		}
		oai := c.respConv.ToOAI(resp, model)
		c.oaiSinglePacketSSE(sw, oai, model, requestID)
		return
	}

	if stream {
		c.streamChatCompletionsCore(requestCtx, w, model, geminiPayload)
		return
	}

	if n > 1 {
		type coreResult struct {
			resp map[string]any
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
						results[idx] = coreResult{err: vertex.NewInternalError(fmt.Sprintf("candidate panic: %v", rec))}
					}
				}()
				r, ve := c.coreGenerate(requestCtx, rawModel, geminiPayload)
				results[idx] = coreResult{resp: r, err: ve}
			}(i)
		}
		wg.Wait()

		var ok []map[string]any
		var firstErr *vertex.VertexError
		for _, r := range results {
			if r.err != nil {
				if firstErr == nil {
					firstErr = r.err
				}
				continue
			}
			ok = append(ok, r.resp)
		}
		if len(ok) == 0 {
			if firstErr == nil {
				firstErr = vertex.NewInternalError("All candidates failed")
			}
			if isSafetyBlock(firstErr) {
				log.Printf("[Vertex] 请求被 Google 安全审查拦截, 请求ID=%s, 原因: %s", vertex.RequestIDFromContext(r.Context()), firstErr.Status)
				writeJSON(w, http.StatusOK, oaiSafetyResponse(model))
				return
			}
			writeJSON(w, firstErr.Code, vertexErrorToOAI(firstErr))
			return
		}
		writeJSON(w, http.StatusOK, c.respConv.AggregateN(ok, model))
		return
	}

	geminiResp, ve := c.coreGenerate(requestCtx, rawModel, geminiPayload)
	if ve != nil {
		if isSafetyBlock(ve) {
			log.Printf("[Vertex] 请求被 Google 安全审查拦截, 请求ID=%s, 原因: %s", vertex.RequestIDFromContext(r.Context()), ve.Status)
			writeJSON(w, http.StatusOK, oaiSafetyResponse(model))
			return
		}
		writeJSON(w, ve.Code, vertexErrorToOAI(ve))
		return
	}

	oaiResp := c.respConv.ToOAI(geminiResp, model)
	writeJSON(w, http.StatusOK, oaiResp)
}

func (c *ChatHandler) streamChatCompletionsCore(ctx context.Context, w http.ResponseWriter, model string, geminiPayload map[string]any) {
	rid := vertex.RequestIDFromContext(ctx)
	if rid == "" {
		rid = reqID24()
	}
	sseID := reqID24()

	flusher, canFlush := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	write := func(line string) bool {
		if _, err := io.WriteString(w, line); err != nil {
			log.Printf("[Server] [Stream] 请求ID=%s 客户端已主动断开连接", rid)
			return false
		}
		if canFlush {
			flusher.Flush()
		}
		return true
	}

	hasFinish := false
	gotContent := false
	streamErrWritten := false
	startTime := time.Now()
	observer := newStreamObserver(startTime)
	isFirst := true

	c.coreStreamGenerate(ctx, model, geminiPayload, func(data map[string]any, err *vertex.VertexError) bool {
		if err != nil {
			c.writeStreamError(write, err, rid, model)
			streamErrWritten = true
			return false
		}
		events := c.respConv.StreamToSSE(data, model, sseID, isFirst)
		isFirst = false
		observer.observe(ctx, vertex.StreamChunk{Data: data}, events)
		for _, ev := range events {
			if strings.Contains(ev, `"finish_reason"`) && !strings.Contains(ev, `"finish_reason":null`) {
				hasFinish = true
			}
			if strings.Contains(ev, `"content":`) || strings.Contains(ev, `"tool_calls":`) || strings.Contains(ev, `"reasoning_content":`) {
				gotContent = true
			}
			if !write(ev) {
				return false
			}
		}
		return true
	})

	writeSilent := func(line string) bool {
		if _, err := io.WriteString(w, line); err != nil {
			return false
		}
		if canFlush {
			flusher.Flush()
		}
		return true
	}

	if !gotContent && !streamErrWritten {
		ee := vertex.NewEmptyResponseError("Upstream returned empty response (no content)")
		c.writeStreamError(write, ee, rid, model)
		return
	}
	if !hasFinish {
		base := streamChunkBase(model, sseID)
		base["choices"] = []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "length"}}
		writeSilent(sseEvent(base))
	}
	writeSilent("data: [DONE]\n\n")
}

func (c *ChatHandler) writeStreamError(write func(string) bool, e *vertex.VertexError, requestID, model string) {
	if isSafetyBlock(e) {
		base := streamChunkBase(model, requestID)
		base["choices"] = []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "content_filter"}}
		_ = write(sseEvent(base))
	} else {
		_ = write(sseEvent(vertexErrorToOAI(e)))
	}
	_ = write("data: [DONE]\n\n")
}

// oaiSinglePacketSSE 把完整 OAI 响应转为单包 SSE 事件 + [DONE]。
// 单个 choice 的 delta 内放入所有可表达内容（text、tool_calls、reasoning_content），
// 事件本身包含 finish_reason 和 usage。
func (c *ChatHandler) oaiSinglePacketSSE(sw *sseWriter, oai map[string]any, model, requestID string) {
	base := streamChunkBase(model, requestID)
	choices := firstChoice(oai)
	if choices == nil {
		_ = sw.write(sseEvent(base))
		_ = sw.write("data: [DONE]\n\n")
		return
	}

	msg, _ := choices["message"].(map[string]any)
	finishReason, _ := choices["finish_reason"].(string)
	if msg == nil {
		msg = map[string]any{}
	}

	delta := map[string]any{}
	if role, ok := msg["role"].(string); ok {
		delta["role"] = role
	}
	if content, ok := msg["content"]; ok && content != nil {
		delta["content"] = content
	}
	if tc, ok := msg["tool_calls"]; ok && tc != nil {
		delta["tool_calls"] = tc
	}
	if rc, ok := msg["reasoning_content"]; ok && rc != nil {
		delta["reasoning_content"] = rc
	}

	choice := map[string]any{"index": 0, "delta": delta}
	if finishReason != "" {
		choice["finish_reason"] = finishReason
	}
	base["choices"] = []any{choice}

	if usage, ok := oai["usage"]; ok {
		base["usage"] = usage
	}
	_ = sw.write(sseEvent(base))
	_ = sw.write("data: [DONE]\n\n")
}

func firstChoice(oai map[string]any) map[string]any {
	choices, ok := oai["choices"].([]any)
	if !ok || len(choices) == 0 {
		return nil
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		return nil
	}
	return choice
}
