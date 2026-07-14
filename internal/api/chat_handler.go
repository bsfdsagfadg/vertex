package api

import (
	"context"
	"encoding/json"
	"io"
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

	transform.ApplyImageConfig(geminiPayload, body)
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
			ic["imageSize"] = "1K"
		}
	}


	if aggregateStream {
		c.oaiAggregateStream(requestCtx, w, model, geminiPayload)
		return
	}
	if stream && useFake {
		c.oaiFakeStream(requestCtx, w, model, geminiPayload)
		return
	}

	if stream {
		c.streamChatCompletions(requestCtx, w, model, geminiPayload)
		return
	}

	if n > 1 {
		responses, vErr := c.vc.CompleteChatN(requestCtx, model, geminiPayload, n)
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
		writeJSON(w, http.StatusOK, c.respConv.AggregateN(responses, model))
		return
	}

	geminiResp, vErr := c.vc.CompleteChat(requestCtx, model, geminiPayload)
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

	oaiResp := c.respConv.ToOAI(geminiResp, model)
	writeJSON(w, http.StatusOK, oaiResp)
}

func (c *ChatHandler) streamChatCompletions(ctx context.Context, w http.ResponseWriter, model string, geminiPayload map[string]any) {
	requestID := reqID24()

	flusher, canFlush := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	write := func(line string) bool {
		if _, err := io.WriteString(w, line); err != nil {
			log.Printf("[Server] [Stream] 请求ID=%s 客户端已主动断开连接", requestID)
			return false
		}
		if canFlush {
			flusher.Flush()
		}
		return true
	}

	isFirst := true
	hasFinish := false
	gotContent := false
	streamErrWritten := false
	startTime := time.Now()

	c.vc.StreamChat(ctx, model, geminiPayload, func(ch vertex.StreamChunk) bool {
		if isFirst && ch.Err == nil {
			log.Printf("[Server] [Stream] 请求ID=%s 首字响应耗时: %.2fs", requestID, time.Since(startTime).Seconds())
			cli.UpdateReqState(requestID, "💬 流式打字", "\033[36m", "正在输出...")
		}
		if ch.Err != nil {
			c.writeStreamError(write, ch.Err, requestID, model)
			streamErrWritten = true
			return false
		}
		events := c.respConv.StreamToSSE(ch.Data, model, requestID, isFirst)
		isFirst = false
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
		c.writeStreamError(write, ee, requestID, model)
		return
	}
	if !hasFinish {
		base := streamChunkBase(model, requestID)
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

func (c *ChatHandler) oaiFakeStream(ctx context.Context, w http.ResponseWriter, model string, geminiPayload map[string]any) {
	requestID := reqID24()
	sw := newSSEWriter(w, "text/event-stream")

	resp, vErr := c.vc.CompleteChat(ctx, model, geminiPayload)
	if vErr != nil {
		ve := toVertexError(vErr)
		c.writeStreamError(sw.write, ve, requestID, model)
		return
	}

	oai := c.respConv.ToOAI(resp, model)
	contentText := firstChoiceContent(oai)

	createdTS := time.Now().Unix()
	chunks := splitIntoRuneChunks(contentText)
	for i, piece := range chunks {
		base := streamChunkBase(model, requestID)
		base["created"] = createdTS
		var delta map[string]any
		if i == 0 {
			delta = map[string]any{"role": "assistant", "content": piece}
		} else {
			delta = map[string]any{"content": piece}
		}
		choice := map[string]any{"index": 0, "delta": delta}
		if i == len(chunks)-1 {
			choice["finish_reason"] = "stop"
		}
		base["choices"] = []any{choice}
		if !sw.write(sseEvent(base)) {
			return
		}
	}
	_ = sw.write("data: [DONE]\n\n")
}

func (c *ChatHandler) oaiAggregateStream(ctx context.Context, w http.ResponseWriter, model string, geminiPayload map[string]any) {
	requestID := reqID24()
	sw := newSSEWriter(w, "text/event-stream")

	resp, vErr := c.vc.CompleteChat(ctx, model, geminiPayload)
	if vErr != nil {
		ve := toVertexError(vErr)
		c.writeStreamError(sw.write, ve, requestID, model)
		return
	}

	oai := c.respConv.ToOAI(resp, model)
	contentText := firstChoiceContent(oai)

	createdTS := time.Now().Unix()
	base := streamChunkBase(model, requestID)
	base["created"] = createdTS
	
	choice := map[string]any{
		"index": 0, 
		"delta": map[string]any{"role": "assistant", "content": contentText},
	}
	base["choices"] = []any{choice}
	if !sw.write(sseEvent(base)) {
		return
	}
	
	// Stream end
	baseEnd := streamChunkBase(model, requestID)
	baseEnd["created"] = createdTS
	choiceEnd := map[string]any{
		"index": 0, 
		"delta": map[string]any{},
		"finish_reason": "stop",
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
