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

// GeminiHandler 提供 Gemini 原生 REST 入口。
// 入站 Gemini JSON 解包为强类型 *transform.GeminiRequest，经 ModelResolver 解析后按家族路由至物理隔离的 Pipeline。
type GeminiHandler struct {
	handler
}

func (g *GeminiHandler) handleModelsSubtree(w http.ResponseWriter, r *http.Request) {
	var rest string
	switch {
	case strings.HasPrefix(r.URL.Path, "/v1beta/models/"):
		rest = strings.TrimPrefix(r.URL.Path, "/v1beta/models/")
	case strings.HasPrefix(r.URL.Path, "/v1beta1/models/"):
		rest = strings.TrimPrefix(r.URL.Path, "/v1beta1/models/")
	case strings.HasPrefix(r.URL.Path, "/v1alpha/models/"):
		rest = strings.TrimPrefix(r.URL.Path, "/v1alpha/models/")
	case strings.HasPrefix(r.URL.Path, "/v1/models/"):
		rest = strings.TrimPrefix(r.URL.Path, "/v1/models/")
	default:
		g.geminiError(w, http.StatusNotFound, "not found", "NOT_FOUND")
		return
	}
	if rest == "" {
		g.geminiError(w, http.StatusNotFound, "not found", "NOT_FOUND")
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
			g.geminiError(w, http.StatusMethodNotAllowed, "method not allowed", "INVALID_ARGUMENT")
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
		g.geminiError(w, http.StatusNotFound, "未知方法 "+method+" (unknown method)", "NOT_FOUND")
	}
}

func (g *GeminiHandler) requirePost(w http.ResponseWriter, r *http.Request, fn func()) {
	if r.Method != http.MethodPost {
		g.geminiError(w, http.StatusMethodNotAllowed, "method not allowed", "INVALID_ARGUMENT")
		return
	}
	fn()
}

func (g *GeminiHandler) readGeminiBody(w http.ResponseWriter, r *http.Request) (*transform.GeminiRequest, bool) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		if _, ok := err.(*json.SyntaxError); ok && strings.Contains(err.Error(), "invalid UTF-8") {
			writeGeminiError(w, r.Context(), vertex.NewInvalidParamError("请求体编码错误，需为 UTF-8 (request body must be UTF-8 encoded)", "", nil))
			return nil, false
		}
		writeGeminiError(w, r.Context(), vertex.NewInvalidParamError("请求格式错误，JSON 解析失败 (invalid JSON)", "", nil))
		return nil, false
	}
	if body == nil {
		body = make(map[string]any)
	}
	if reqObj, ok := body["generateContentRequest"].(map[string]any); ok {
		body = reqObj
	}
	req, err := normalizeGeminiBodyTyped(body)
	if err != nil {
		writeGeminiError(w, r.Context(), vertex.NewInvalidParamError("请求参数有误: "+err.Error()+" (invalid argument)", "", nil))
		return nil, false
	}
	return req, true
}

func (g *GeminiHandler) handleGeminiGenerate(w http.ResponseWriter, r *http.Request, rawModel string) {
	req, ok := g.readGeminiBody(w, r)
	if !ok {
		return
	}
	requestCtx, cancel := context.WithTimeout(r.Context(), time.Duration(g.cfg.RequestTimeoutSeconds())*time.Second)
	defer cancel()

	resolved := transform.ResolveModel(rawModel, g.cfg)
	log.Printf("[Server] [GeminiGenerate] 收到请求: 模型=%s, 真模型=%s, 家族=%v", rawModel, resolved.ActualModel, resolved.Family)

	var resp *transform.GeminiResponse
	var ve *vertex.VertexError

	switch resolved.Family {
	case transform.FamilyImage:
		// Gemini 原生非流式端点强约束纯图片模态
		if req.GenerationConfig == nil {
			req.GenerationConfig = &transform.GenerationConfig{}
		}
		req.GenerationConfig.ResponseModalities = []string{"IMAGE"}
		resp, ve = g.ExecuteImageGenerate(requestCtx, resolved, req)
	case transform.FamilyAudio:
		resp, ve = g.ExecuteAudioSpeech(requestCtx, resolved, req)
	default:
		resp, ve = g.ExecuteTextComplete(requestCtx, resolved, req)
	}

	if ve != nil {
		if isSafetyBlock(ve) {
			writeJSON(w, http.StatusOK, geminiSafetyResponse(ve))
			return
		}
		writeGeminiError(w, r.Context(), ve)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (g *GeminiHandler) handleGeminiStreamGenerate(w http.ResponseWriter, r *http.Request, rawModel string) {
	req, ok := g.readGeminiBody(w, r)
	if !ok {
		return
	}
	requestCtx, cancel := context.WithTimeout(r.Context(), time.Duration(g.cfg.RequestTimeoutSeconds())*time.Second)
	defer cancel()

	resolved := transform.ResolveModel(rawModel, g.cfg)
	streamMode := resolved.Strategy.FamilyStreamMode()
	if resolved.IsFake || g.cfg.AggregateStream() {
		streamMode = transform.StreamModeAggregate
	}

	log.Printf("[Server] [GeminiStreamGenerate] 收到请求: 模型=%s, 真模型=%s, 家族=%v, 流模式=%s", rawModel, resolved.ActualModel, resolved.Family, streamMode)

	sw := newSSEWriter(w, "text/event-stream")

	if streamMode == transform.StreamModeAggregate {
		var resp *transform.GeminiResponse
		var ve *vertex.VertexError

		switch resolved.Family {
		case transform.FamilyImage:
			resp, ve = g.ExecuteImageGenerate(requestCtx, resolved, req)
		case transform.FamilyAudio:
			resp, ve = g.ExecuteAudioSpeech(requestCtx, resolved, req)
		default:
			resp, ve = g.ExecuteTextComplete(requestCtx, resolved, req)
		}

		if ve != nil {
			if !sw.hasWritten() {
				writeGeminiError(w, r.Context(), ve)
				return
			}
			if isSafetyBlock(ve) {
				_ = sw.write(g.geminiSSE(geminiSafetyChunk(ve)))
				return
			}
			_ = sw.write(g.geminiSSE(vertexErrorToGemini(ve)))
			return
		}
		_ = sw.write(g.geminiSSETyped(resp))
		return
	}

	if resolved.Family == transform.FamilyImage {
		g.ExecuteImageStream(requestCtx, resolved, req, func(chunk *transform.GeminiChunk, err *vertex.VertexError) bool {
			if err != nil {
				writeGeminiError(w, r.Context(), err)
				return false
			}
			_ = sw.write(g.geminiSSETyped(&transform.GeminiResponse{
				Candidates:     chunk.Candidates,
				PromptFeedback: chunk.PromptFeedback,
				UsageMetadata:  chunk.UsageMetadata,
				ModelVersion:   chunk.ModelVersion,
			}))
			return true
		})
		return
	}

	hasFinish := false
	streamErrWritten := false

	g.ExecuteTextStream(requestCtx, resolved, req, func(chunk *transform.GeminiChunk, err *vertex.VertexError) bool {
		if err != nil {
			if !sw.hasWritten() {
				writeGeminiError(w, r.Context(), err)
				streamErrWritten = true
				return false
			}
			if isSafetyBlock(err) {
				_ = sw.write(g.geminiSSE(geminiSafetyChunk(err)))
			} else {
				pkt := finishReasonChunk(err)
				_ = sw.write(g.geminiSSE(pkt))
			}
			streamErrWritten = true
			return false
		}

		if chunk == nil {
			return true
		}

		if len(chunk.Candidates) == 0 {
			// 如果没有 candidates，但包含元数据（例如 usageMetadata），补齐 Gemini 官方 SDK 规范的 candidates 骨架包
			if chunk.UsageMetadata != nil || chunk.PromptFeedback != nil || chunk.ModelVersion != "" {
				emptyCand := &transform.Candidate{
					Index: 0,
					Content: &transform.Content{
						Role:  "model",
						Parts: []transform.Part{},
					},
				}
				if hasFinish {
					emptyCand.FinishReason = "STOP"
				}
				chunk.Candidates = []*transform.Candidate{emptyCand}
			} else {
				return true
			}
		}

		for _, cand := range chunk.Candidates {
			if cand == nil {
				continue
			}
			if cand.Content == nil {
				cand.Content = &transform.Content{Role: "model", Parts: []transform.Part{}}
			} else if cand.Content.Role == "" {
				cand.Content.Role = "model"
			}
			if cand.Content.Parts == nil {
				cand.Content.Parts = []transform.Part{}
			}
		}

		if !hasFinish {
			if fr := firstCandidateFinishReason(chunk); fr != "" && fr != transform.FinishReasonUnspecified {
				hasFinish = true
			}
		}
		return sw.write(g.geminiSSETyped(chunk))
	})

	if streamErrWritten {
		return
	}
	if !hasFinish {
		_ = sw.write(g.geminiSSE(map[string]any{
			"candidates": []any{map[string]any{
				"content":      map[string]any{"parts": []any{map[string]any{"text": ""}}, "role": "model"},
				"finishReason": "STOP",
				"index":        0,
			}},
		}))
	}
}

func (g *GeminiHandler) handleCountTokens(w http.ResponseWriter, r *http.Request, rawModel string) {
	resolved := transform.ResolveModel(rawModel, g.cfg)
	body, ok := g.readGeminiBody(w, r)
	if !ok {
		return
	}
	cli.UpdateReqModel(vertex.RequestIDFromContext(r.Context()), resolved.ActualModel)
	log.Printf("[Server] [CountTokens] 收到请求: 模型=%s, 真模型=%s", rawModel, resolved.ActualModel)

	contents := body.Contents
	total := g.vc.CountTokens(r.Context(), resolved.ActualModel, geminiContentsToAny(contents))
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
	writeJSON(w, http.StatusOK, map[string]any{"name": "models/" + name, "displayName": name})
}

func (g *GeminiHandler) geminiSSE(obj map[string]any) string {
	data, err := jsonx.Marshal(obj)
	if err != nil {
		return "data: {}\n\n"
	}
	return "data: " + string(data) + "\n\n"
}

func (g *GeminiHandler) geminiSSETyped(obj *transform.GeminiResponse) string {
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

// finishReasonChunk 构造一个仅带 finishReason 的 Gemini 错误帧。
func finishReasonChunk(_ *vertex.VertexError) map[string]any {
	return map[string]any{
		"candidates": []any{map[string]any{"finishReason": "OTHER", "index": 0}},
	}
}

// normalizeGeminiBodyTyped 把 Gemini 原生 JSON 请求体映射为强类型请求。
func normalizeGeminiBodyTyped(raw map[string]any) (*transform.GeminiRequest, error) {
	data, err := jsonx.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var req transform.GeminiRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, err
	}
	return &req, nil
}

// firstCandidateFinishReason 读取首候选 finishReason（原始未清洗）。
func firstCandidateFinishReason(chunk *transform.GeminiResponse) string {
	if chunk == nil {
		return ""
	}
	for _, c := range chunk.Candidates {
		if c != nil {
			return c.FinishReason
		}
	}
	return ""
}

// geminiContentsToAny 把强类型 contents 逐项 JSON 转回 []any（供 CountTokens 复用旧实现）。
func geminiContentsToAny(contents []transform.Content) []any {
	out := make([]any, 0, len(contents))
	for _, c := range contents {
		b, err := jsonx.Marshal(c)
		if err != nil {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			continue
		}
		out = append(out, m)
	}
	return out
}

// toRawMap 便利序列化：map[string]map typed -> raw map（供 JsonxUnmarshal 使用）。
func toRawMap(v any) map[string]any {
	b, err := jsonx.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
}
