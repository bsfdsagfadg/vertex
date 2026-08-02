package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/jsonx"
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
	actualModel, _, modelOK := resolveConfiguredModel(model, g.cfg)
	if !modelOK {
		geminiModelNotFound(w, model)
		return
	}
	body, ok := g.readGeminiBody(w, r)
	if !ok {
		return
	}
	if reqObj, ok2 := body["generateContentRequest"].(map[string]any); ok2 {
		body = reqObj
	}
	log.Printf("[Server] [GeminiGenerate] 收到请求: 模型=%s, 真模型=%s", model, actualModel)

	resp, vErr := g.vc.CompleteChat(r.Context(), actualModel, body)
	if vErr != nil {
		ve := toVertexError(vErr)
		if isSafetyBlock(ve) {
			writeJSON(w, http.StatusOK, geminiSafetyResponse(ve))
			return
		}
		writeJSON(w, ve.Code, vertexErrorToGemini(ve))
		return
	}
	cleanGeminiFinishReason(resp)
	rewriteGeminiIDs(resp, generateVPSuffix())
	writeJSON(w, http.StatusOK, resp)
}

func (g *GeminiHandler) handleGeminiStreamGenerate(w http.ResponseWriter, r *http.Request, model string) {
	actualModel, useFake, modelOK := resolveConfiguredModel(model, g.cfg)
	if !modelOK {
		geminiModelNotFound(w, model)
		return
	}
	body, ok := g.readGeminiBody(w, r)
	if !ok {
		return
	}
	if reqObj, ok2 := body["generateContentRequest"].(map[string]any); ok2 {
		body = reqObj
	}
	log.Printf("[Server] [GeminiStreamGenerate] 收到请求: 模型=%s, 真模型=%s, 假流式=%v", model, actualModel, useFake)

	sw := newSSEWriter(w, "text/event-stream")

	if useFake {
		g.geminiFakeStream(r.Context(), w, sw, actualModel, body)
		return
	}

	gotChunk := false
	hasFinish := false
	streamErrWritten := false
	suffix := generateVPSuffix()
	g.vc.StreamChat(r.Context(), actualModel, body, func(ch vertex.StreamChunk) bool {
		if ch.Err != nil {
			if !sw.hasWritten() {
				ve := toVertexError(ch.Err)
				if isSafetyBlock(ve) {
					writeJSON(w, http.StatusOK, geminiSafetyResponse(ve))
				} else {
					writeJSON(w, ve.Code, vertexErrorToGemini(ve))
				}
			} else {
				if isSafetyBlock(ch.Err) {
					_ = sw.write(g.geminiSSE(geminiSafetyChunk(ch.Err)))
				} else {
					_ = sw.write(g.geminiSSE(map[string]any{"error": map[string]any{
						"code": ch.Err.Code, "message": vertex.FriendlyErrorMessage(ch.Err), "status": geminiStatusOf(ch.Err),
					}}))
				}
			}
			streamErrWritten = true
			return false
		}
		gotChunk = true
		if fr := cleanGeminiFinishReason(ch.Data); fr != "" {
			hasFinish = true
		}
		rewriteGeminiIDs(ch.Data, suffix)
		return sw.write(g.geminiSSE(ch.Data))
	})

	if streamErrWritten {
		return
	}

	if !gotChunk {
		ee := vertex.NewEmptyResponseError("Upstream returned empty response (no content)")
		if !sw.hasWritten() {
			writeJSON(w, ee.Code, vertexErrorToGemini(ee))
		} else {
			_ = sw.write(g.geminiSSE(map[string]any{
				"error": map[string]any{
					"code": 500, "message": "Upstream returned empty response (no content)", "status": "INTERNAL",
				},
			}))
		}
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

func (g *GeminiHandler) geminiFakeStream(ctx context.Context, w http.ResponseWriter, sw *sseWriter, model string, body map[string]any) {
	resp, vErr := g.vc.CompleteChat(ctx, model, body)
	if vErr != nil {
		ve := toVertexError(vErr)
		if !sw.hasWritten() {
			if isSafetyBlock(ve) {
				writeJSON(w, http.StatusOK, geminiSafetyResponse(ve))
				return
			}
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

	text := geminiResponseText(resp)
	chunks := splitIntoRuneChunks(text)
	for i, piece := range chunks {
		cand := map[string]any{"index": 0, "content": map[string]any{"role": "model", "parts": []any{map[string]any{"text": piece}}}}
		if i == len(chunks)-1 {
			cand["finishReason"] = "STOP"
		}
		chunk := map[string]any{"candidates": []any{cand}}
		if !sw.write(g.geminiSSE(chunk)) {
			return
		}
	}
}
func (g *GeminiHandler) handleCountTokens(w http.ResponseWriter, r *http.Request, model string) {
	actualModel, _, modelOK := resolveConfiguredModel(model, g.cfg)
	if !modelOK {
		geminiModelNotFound(w, model)
		return
	}
	body, ok := g.readGeminiBody(w, r)
	if !ok {
		return
	}
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
	if _, _, ok := resolveConfiguredModel(name, g.cfg); !ok {
		geminiModelNotFound(w, modelName)
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

func geminiResponseText(resp map[string]any) string {
	var sb strings.Builder
	cands, _ := resp["candidates"].([]any)
	for _, cRaw := range cands {
		c, ok := cRaw.(map[string]any)
		if !ok {
			continue
		}
		content, ok := c["content"].(map[string]any)
		if !ok {
			continue
		}
		parts, _ := content["parts"].([]any)
		for _, pRaw := range parts {
			p, ok2 := pRaw.(map[string]any)
			if !ok2 {
				continue
			}
			if isTruthyAny(p["thought"]) {
				continue
			}
			if t, ok3 := p["text"].(string); ok3 {
				sb.WriteString(t)
			}
		}
	}
	return sb.String()
}

func isTruthyAny(v any) bool { return jsonx.Truthy(v) }
func generateVPSuffix() string {
	var buf [4]byte
	_, _ = rand.Read(buf[:])
	return fmt.Sprintf("-vp%08x", buf)
}

func rewriteGeminiIDs(val any, suffix string) {
	switch v := val.(type) {
	case map[string]any:
		for k, mv := range v {
			if s, ok := mv.(string); ok && strings.HasPrefix(s, "gemini-tool-call-") {
				v[k] = s + suffix
			} else {
				rewriteGeminiIDs(mv, suffix)
			}
		}
	case []any:
		for _, item := range v {
			rewriteGeminiIDs(item, suffix)
		}
	}
}
