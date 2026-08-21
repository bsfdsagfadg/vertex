package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/engine/transform"
	"github.com/bsfdsagfadg/vertex/internal/engine/vertex"
	"github.com/bsfdsagfadg/vertex/internal/infra/cli"
	"github.com/bsfdsagfadg/vertex/internal/infra/jsonx"
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
	// 单趟解码为 json.RawMessage 顶层值（保留原始字节，零 map[string]any 树构建）。
	// 复用 decoder 双条件分流：SyntaxError 含 "invalid UTF-8" → 编码错误；其余 → 非法 JSON。
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		if _, ok := err.(*json.SyntaxError); ok && strings.Contains(err.Error(), "invalid UTF-8") {
			writeGeminiError(w, r.Context(), vertex.NewInvalidParamError("请求体编码错误，需为 UTF-8 (request body must be UTF-8 encoded)", "", nil))
			return nil, false
		}
		writeGeminiError(w, r.Context(), vertex.NewInvalidParamError("请求格式错误，JSON 解析失败 (invalid JSON)", "", nil))
		return nil, false
	}
	// 顶层必须是对象（等价旧 decode 到 map 的 UnmarshalTypeError 分支）。
	var indexed map[string]json.RawMessage
	if err := json.Unmarshal(raw, &indexed); err != nil {
		writeGeminiError(w, r.Context(), vertex.NewInvalidParamError("请求格式错误，JSON 解析失败 (invalid JSON)", "", nil))
		return nil, false
	}
	// generateContentRequest 解包（仅当值为对象，等价旧 map 类型断言，null/标量不 unwrap）。
	payload := raw
	if reqObj, ok := indexed["generateContentRequest"]; ok && len(reqObj) > 0 && reqObj[0] == '{' {
		payload = reqObj
	}
	var req transform.GeminiRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		writeGeminiError(w, r.Context(), vertex.NewInvalidParamError("请求参数有误: "+err.Error()+" (invalid argument)", "", nil))
		return nil, false
	}
	return &req, true
}

func (g *GeminiHandler) handleGeminiGenerate(w http.ResponseWriter, r *http.Request, rawModel string) {
	req, ok := g.readGeminiBody(w, r)
	if !ok {
		return
	}
	requestCtx := r.Context()

	resolved := transform.ResolveModel(rawModel, g.cfg)
	log.Printf("[Server] [GeminiGenerate] 收到请求: 模型=%s, 真模型=%s, 家族=%v", rawModel, resolved.ActualModel, resolved.Family)

	var resp *transform.GeminiResponse
	var ve *vertex.VertexError

	switch resolved.Family {
	case transform.FamilyImage:
		// 模态由 ImageStrategy 按后台配置统一注入（非流式与流式端点行为一致）
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
	requestCtx := r.Context()

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
			if isSafetyBlock(ve) {
				_ = sw.write(g.geminiSSE(geminiSafetyChunk(ve)))
				return
			}
			if !sw.hasWritten() {
				writeGeminiError(w, r.Context(), ve)
				return
			}
			_ = sw.write(g.geminiSSE(vertexErrorToGemini(ve)))
			return
		}
		_ = sw.write(g.geminiSSETyped(resp))
		return
	}

	hasFinish := false
	streamErrWritten := false

	g.ExecuteTextStream(requestCtx, resolved, req, func(chunk *transform.GeminiChunk, err *vertex.VertexError) bool {
		if err != nil {
			if isSafetyBlock(err) {
				_ = sw.write(g.geminiSSE(geminiSafetyChunk(err)))
			} else if !sw.hasWritten() {
				writeGeminiError(w, r.Context(), err)
			} else {
				// 已向客户端输出内容后断流：透传真实错误（含 Truncated 标记的真实原因），
				// 而非模糊 finishReason:OTHER；客户端可区分"网络中断"与"安全拦截"。
				_ = sw.write(g.geminiSSE(vertexErrorToGemini(err)))
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
		// 流结束但从未出现真实 finishReason：按匿名端点契约（每帧 FINISH_REASON_UNSPECIFIED
		// 无意义，完整流必以真实 finishReason 终帧收尾）此为异常收尾，绝非完整生成。
		// 已交付过任何帧则标记 Truncated（Committed 语义：绝不重试），否则按普通网络错误帧透传；
		// 严禁补发 FinishReason=STOP 假结束帧。
		e := vertex.NewNetworkError(vertex.ErrStreamIncomplete)
		if sw.hasWritten() {
			e = e.WithTruncated()
		}
		_ = sw.write(g.geminiSSE(vertexErrorToGemini(e)))
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
	total := g.vc.CountTokens(r.Context(), resolved.ActualModel, contents)
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

func (g *GeminiHandler) geminiSSETyped(obj any) string {
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
	errObj := map[string]any{
		"code": e.Code, "message": msg, "status": geminiStatusOf(e),
	}
	// 透传上游结构化 details（Gemini/google.rpc.Status 规范：details 为数组，单元素为原始对象）。
	if len(e.Details) > 0 {
		errObj["details"] = []any{e.Details}
	}
	return map[string]any{"error": errObj}
}

func geminiStatusOf(e *vertex.VertexError) string {
	if e != nil && e.Status != "" {
		return e.Status
	}
	return "INTERNAL"
}

func geminiSafetyResponse(e *vertex.VertexError) map[string]any {
	blockReason := e.Status
	if !transform.IsBlockReason(blockReason) {
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
	if !transform.IsBlockReason(blockReason) {
		blockReason = "SAFETY"
	}
	finishReason := e.Status
	if !transform.IsSafetyFinishReason(finishReason) {
		finishReason = "SAFETY"
	}
	return map[string]any{
		"candidates": []any{map[string]any{
			"content":       map[string]any{"parts": []any{}, "role": "model"},
			"finishReason":  finishReason,
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
