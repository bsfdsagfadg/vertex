package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/cli"
	"github.com/bsfdsagfadg/vertex/internal/jsonx"
	"github.com/bsfdsagfadg/vertex/internal/transform"
	"github.com/bsfdsagfadg/vertex/internal/vertex"
)

type GeminiHandler struct {
	handler
}

func (g *GeminiHandler) handleModelsSubtree(w http.ResponseWriter, r *http.Request) {
	var rest string
	switch {
	case strings.HasPrefix(r.URL.Path, "/v1beta/models/"):
		rest = strings.TrimPrefix(r.URL.Path, "/v1beta/models/")
	case strings.HasPrefix(r.URL.Path, "/v1/models/"):
		rest = strings.TrimPrefix(r.URL.Path, "/v1/models/")
	default:
		oaiError(w, http.StatusNotFound, "not found", "invalid_request_error")
		return
	}
	if rest == "" {
		oaiError(w, http.StatusNotFound, "not found", "invalid_request_error")
		return
	}

	model := rest
	method := ""
	if idx := strings.LastIndex(rest, ":"); idx != -1 {
		model = rest[:idx]
		method = rest[idx+1:]
	}

	switch method {
	case "":
		if r.Method != http.MethodGet {
			oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
			return
		}
		g.handleModelInfo(w, model)
	case "generateContent":
		g.requirePost(w, r, func() { g.handleGeminiGenerate(w, r, model) })
	case "streamGenerateContent":
		g.requirePost(w, r, func() { g.handleGeminiStreamGenerate(w, r, model) })
	case "countTokens":
		g.requirePost(w, r, func() { g.handleCountTokens(w, r, model) })
	default:
		oaiError(w, http.StatusNotFound, "未知方法 "+method+" (unknown method)", "invalid_request_error")
	}
}

func (g *GeminiHandler) requirePost(w http.ResponseWriter, r *http.Request, fn func()) {
	if r.Method != http.MethodPost {
		oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
		return
	}
	fn()
}

func (g *GeminiHandler) readGeminiBody(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		if _, ok := err.(*json.SyntaxError); ok && strings.Contains(err.Error(), "invalid UTF-8") {
			g.geminiError(w, http.StatusBadRequest, "请求体编码错误，需为 UTF-8 (request body must be UTF-8 encoded)", "INVALID_ARGUMENT")
			return nil, false
		}
		g.geminiError(w, http.StatusBadRequest, "请求格式错误，JSON 解析失败 (invalid JSON)", "INVALID_ARGUMENT")
		return nil, false
	}
	if body == nil {
		body = make(map[string]any)
	}
	return body, true
}

func (g *GeminiHandler) handleGeminiGenerate(w http.ResponseWriter, r *http.Request, model string) {
	body, ok := g.readGeminiBody(w, r)
	if !ok {
		return
	}
	requestCtx, cancel := context.WithTimeout(r.Context(), time.Duration(g.cfg.RequestTimeoutSeconds())*time.Second)
	defer cancel()

	actualModel, _ := stripFakePrefix(model, g.cfg.FakePrefixes())
	log.Printf("[Server] [GeminiGenerate] 收到请求: 模型=%s, 真模型=%s", model, actualModel)

	transform.ApplyImageConfig(body, nil, actualModel)
	if strings.Contains(strings.ToLower(actualModel), "image") {
		gc, gcOk := body["generationConfig"].(map[string]any)
		if !gcOk {
			gc = map[string]any{}
			body["generationConfig"] = gc
		}
		ic, icOk := gc["imageConfig"].(map[string]any)
		if !icOk {
			ic = map[string]any{}
			gc["imageConfig"] = ic
		}
		if _, has := ic["imageSize"]; !has {
			ic["imageSize"] = transform.ResolveImageSize(g.cfg.DefaultImageSize(), actualModel)
		}
	}
	transform.ApplyResponseModalities(body, g.cfg.DefaultResponseModalities(), actualModel)
	transform.ApplyDefaultThinking(body, g.cfg.DefaultThinkingLevel(), actualModel)

	resp, ve := g.coreGenerate(requestCtx, model, body)
	if ve != nil {
		if isSafetyBlock(ve) {
			writeJSON(w, http.StatusOK, geminiSafetyResponse(ve))
			return
		}
		writeJSON(w, ve.Code, vertexErrorToGemini(ve))
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (g *GeminiHandler) handleGeminiStreamGenerate(w http.ResponseWriter, r *http.Request, model string) {
	body, ok := g.readGeminiBody(w, r)
	if !ok {
		return
	}
	requestCtx, cancel := context.WithTimeout(r.Context(), time.Duration(g.cfg.RequestTimeoutSeconds())*time.Second)
	defer cancel()

	actualModel, useFake := stripFakePrefix(model, g.cfg.FakePrefixes())
	log.Printf("[Server] [GeminiStreamGenerate] 收到请求: 模型=%s, 真模型=%s, 假非流=%v, 聚合流=%v", model, actualModel, useFake, g.cfg.AggregateStream())

	transform.ApplyImageConfig(body, nil, actualModel)
	if strings.Contains(strings.ToLower(actualModel), "image") {
		gc, gcOk := body["generationConfig"].(map[string]any)
		if !gcOk {
			gc = map[string]any{}
			body["generationConfig"] = gc
		}
		ic, icOk := gc["imageConfig"].(map[string]any)
		if !icOk {
			ic = map[string]any{}
			gc["imageConfig"] = ic
		}
		if _, has := ic["imageSize"]; !has {
			ic["imageSize"] = transform.ResolveImageSize(g.cfg.DefaultImageSize(), actualModel)
		}
	}
	transform.ApplyResponseModalities(body, g.cfg.DefaultResponseModalities(), actualModel)
	transform.ApplyDefaultThinking(body, g.cfg.DefaultThinkingLevel(), actualModel)

	sw := newSSEWriter(w, "text/event-stream")

	if useFake || g.cfg.AggregateStream() {
		resp, ve := g.coreGenerate(requestCtx, model, body)
		if ve != nil {
			if !sw.hasWritten() {
				writeJSON(w, ve.Code, vertexErrorToGemini(ve))
				return
			}
			if isSafetyBlock(ve) {
				_ = sw.write(g.geminiSSE(geminiSafetyChunk(ve)))
				return
			}
			_ = sw.write(g.geminiSSE(map[string]any{"error": map[string]any{
				"code": ve.Code, "message": vertex.FriendlyErrorMessage(ve), "status": geminiStatusOf(ve),
			}}))
			return
		}
		_ = sw.write(g.geminiSSE(resp))
		return
	}

	gotValidChunk := false
	hasFinish := false
	streamErrWritten := false
	startTime := time.Now()
	observer := newStreamObserver(startTime)
	g.coreStreamGenerate(requestCtx, model, body, func(data map[string]any, err *vertex.VertexError) bool {
		if err != nil {
			if !sw.hasWritten() {
				writeJSON(w, err.Code, vertexErrorToGemini(err))
				streamErrWritten = true
				return false
			}
			if isSafetyBlock(err) {
				_ = sw.write(g.geminiSSE(geminiSafetyChunk(err)))
			} else {
				_ = sw.write(g.geminiSSE(map[string]any{"error": map[string]any{
					"code": err.Code, "message": vertex.FriendlyErrorMessage(err), "status": geminiStatusOf(err),
				}}))
			}
			streamErrWritten = true
			return false
		}
		if hasGeminiValidOutput(data) {
			gotValidChunk = true
			observer.markTriggered(requestCtx)
		}
		if cands, _ := data["candidates"].([]any); len(cands) > 0 {
			if c, ok := cands[0].(map[string]any); ok {
				if fr, _ := c["finishReason"].(string); fr != "" && fr != "FINISH_REASON_UNSPECIFIED" {
					gotValidChunk = true
					hasFinish = true
				}
			}
		}
		return sw.write(g.geminiSSE(data))
	})

	if streamErrWritten {
		return
	}
	if !gotValidChunk {
		ee := vertex.NewEmptyResponseError("Upstream returned empty response (no content)", nil)
		if !sw.hasWritten() {
			writeJSON(w, ee.Code, vertexErrorToGemini(ee))
			return
		}
		_ = sw.write(g.geminiSSE(map[string]any{"error": map[string]any{
			"code": ee.Code, "message": vertex.FriendlyErrorMessage(ee), "status": geminiStatusOf(ee),
		}}))
		return
	}
	if !hasFinish {
		_ = sw.write(g.geminiSSE(map[string]any{
			"candidates": []any{map[string]any{
				"content":      map[string]any{"parts": []any{}, "role": "model"},
				"finishReason": "STOP",
				"index":        0,
			}},
		}))
	}
}

func (g *GeminiHandler) handleCountTokens(w http.ResponseWriter, r *http.Request, model string) {
	actualModel, _ := stripFakePrefix(model, g.cfg.FakePrefixes())
	body, ok := g.readGeminiBody(w, r)
	if !ok {
		return
	}
	cli.UpdateReqModel(vertex.RequestIDFromContext(r.Context()), actualModel)
	log.Printf("[Server] [CountTokens] 收到请求: 模型=%s, 真模型=%s", model, actualModel)

	var contents []any
	if reqObj, ok2 := body["generateContentRequest"].(map[string]any); ok2 {
		contents, _ = reqObj["contents"].([]any)
	} else {
		contents, _ = body["contents"].([]any)
	}
	if contents == nil {
		contents = []any{}
	}

	total := g.vc.CountTokens(r.Context(), actualModel, contents)
	writeJSON(w, http.StatusOK, map[string]any{"totalTokens": total})
}

func (g *GeminiHandler) handleModelInfo(w http.ResponseWriter, modelName string) {
	name := strings.TrimPrefix(modelName, "models/")
	known := false
	for _, m := range g.cfg.ModelsWithFakeVariants() {
		if m == name {
			known = true
			break
		}
	}
	if !known {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{
			"code": 404, "message": "Model '" + modelName + "' not found.", "status": "NOT_FOUND",
		}})
		return
	}
	writeJSON(w, http.StatusOK, geminiModelInfo(name))
}

func (g *GeminiHandler) geminiSSE(obj map[string]any) string {
	data, err := jsonx.Marshal(obj)
	if err != nil {
		return "data: {}\n\n"
	}
	return "data: " + string(data) + "\n\n"
}

func (g *GeminiHandler) geminiError(w http.ResponseWriter, status int, msg, geminiStatus string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{
		"code": status, "message": msg, "status": geminiStatus,
	}})
}

func cleanGeminiFinishReason(data map[string]any) string {
	cands, ok := data["candidates"].([]any)
	if !ok || len(cands) == 0 {
		return ""
	}
	var realFR string
	for _, candRaw := range cands {
		cand, ok := candRaw.(map[string]any)
		if !ok {
			continue
		}
		fr, _ := cand["finishReason"].(string)
		if fr == "FINISH_REASON_UNSPECIFIED" {
			delete(cand, "finishReason")
		} else if fr != "" {
			if realFR == "" {
				realFR = fr
			}
		}
	}
	return realFR
}

func vertexErrorToGemini(e *vertex.VertexError) map[string]any {
	msg := vertex.FriendlyErrorMessage(e)
	if e.Message != "" {
		msg += " | Raw: " + e.Message
	}
	if e.UpstreamResponse != "" {
		msg += " | Upstream: " + e.UpstreamResponse
	}
	return map[string]any{"error": map[string]any{
		"code": e.Code, "message": msg, "status": geminiStatusOf(e),
	}}
}

func geminiStatusOf(e *vertex.VertexError) string {
	if e != nil && e.Status != "" {
		return e.Status
	}
	return "INTERNAL"
}

func geminiSafetyResponse(e *vertex.VertexError) map[string]any {
	blockReason := e.Status
	if blockReason == "" {
		blockReason = "SAFETY"
	}
	return map[string]any{
		"candidates": []any{},
		"promptFeedback": map[string]any{
			"blockReason":        blockReason,
			"safetyRatings":      []any{},
			"blockReasonMessage": e.Message,
		},
	}
}

func geminiSafetyChunk(e *vertex.VertexError) map[string]any {
	blockReason := e.Status
	if blockReason == "" {
		blockReason = "SAFETY"
	}
	return map[string]any{
		"candidates": []any{map[string]any{
			"content":       map[string]any{"parts": []any{}, "role": "model"},
			"finishReason":  "SAFETY",
			"safetyRatings": []any{},
			"index":         0,
		}},
		"promptFeedback": map[string]any{
			"blockReason":        blockReason,
			"safetyRatings":      []any{},
			"blockReasonMessage": e.Message,
		},
	}
}


