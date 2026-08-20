// Package api 暴露 OpenAI 兼容的 HTTP 端点。
//
// 里程碑1 只实现非流式 /v1/chat/completions（+ /、/health、/v1/models）。
// 真流式 SSE、图像/TTS/embeddings、Gemini 原生端点留待后续里程碑。
package api

import (
	"context"
	"log"
	"net/http"
	"net/http/pprof"
	"path/filepath"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/subscriptions"
	"github.com/bsfdsagfadg/vertex/internal/transform"
	"github.com/bsfdsagfadg/vertex/internal/transport"
	"github.com/bsfdsagfadg/vertex/internal/vertex"
)

type Server struct {
	chat          *ChatHandler
	image         *ImageHandler
	audio         *AudioHandler
	gemini        *GeminiHandler
	admin         *AdminHandler
	subscriptions *subscriptions.Service
	mw            *middleware
}

func NewServer(vc *vertex.VertexAIClient, keys *APIKeyManager, cfg config.ConfigProvider) *Server {
	transport.SetProxyNameResolver(nodes.GetNodeName)
	h := handler{vc: vc, keys: keys, cfg: cfg}
	adminHandler := &AdminHandler{handler: h}
	subscriptionService := subscriptions.New(func(ctx context.Context, rawURL, userAgent string) ([]nodes.Node, error) {
		text, err := adminHandler.fetchSubWithFallback(ctx, rawURL, userAgent)
		if err != nil {
			return nil, err
		}
		return parseImportedNodes(text), nil
	})
	adminHandler.subscriptionService = subscriptionService
	srv := &Server{
		chat:          &ChatHandler{handler: h, reqConv: transform.DefaultRequestConverter(), respConv: transform.DefaultResponseConverter()},
		image:         &ImageHandler{h},
		audio:         &AudioHandler{h},
		gemini:        &GeminiHandler{h},
		admin:         adminHandler,
		subscriptions: subscriptionService,
		mw:            &middleware{cfg: cfg, keys: keys},
	}
	if err := subscriptionService.Start(context.Background()); err != nil {
		log.Printf("[Subscriptions] 启动自动更新服务失败: %v", err)
	}
	return srv
}

func (s *Server) Close() {
	if s.subscriptions != nil {
		s.subscriptions.Stop()
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/v1/models", s.handleModelsOAI)
	mux.HandleFunc("/v1beta/models", s.handleModelsGemini)
	mux.HandleFunc("/v1/chat/completions", s.chat.handleChatCompletions)
	mux.HandleFunc("/v1/images/generations", s.image.handleImageGenerations)
	mux.HandleFunc("/v1/images/edits", s.image.handleImageEdits)
	mux.HandleFunc("/v1/images/variations", s.image.handleImageVariations)
	mux.HandleFunc("/v1/audio/speech", s.audio.handleAudioSpeech)
	mux.HandleFunc("/favicon.ico", s.handleFavicon)
	mux.HandleFunc("/admin", s.admin.handleAdminPage)
	mux.HandleFunc("/admin/", s.admin.handleAdminPage)
	mux.HandleFunc("/api/admin/", s.admin.handleAdminAPI)
	mux.HandleFunc("/assets/", s.handleAssets)
	mux.HandleFunc("/v1beta/models/", s.gemini.handleModelsSubtree)
	mux.HandleFunc("/v1/models/", s.gemini.handleModelsSubtree)

	if s.mw.cfg.DebugPprof() {
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
		mux.HandleFunc("/debug/pprof/goroutine", pprof.Handler("goroutine").ServeHTTP)
		mux.HandleFunc("/debug/pprof/heap", pprof.Handler("heap").ServeHTTP)
		mux.HandleFunc("/debug/pprof/threadcreate", pprof.Handler("threadcreate").ServeHTTP)
		mux.HandleFunc("/debug/pprof/block", pprof.Handler("block").ServeHTTP)
		mux.HandleFunc("/debug/pprof/mutex", pprof.Handler("mutex").ServeHTTP)
	}

	return s.mw.withRecover(s.mw.withCORS(s.mw.withMetrics(s.mw.withAPIKey(s.mw.withBodyLimit(mux)))))
}

func (s *Server) handleFavicon(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAssets(w http.ResponseWriter, r *http.Request) {
	assetsDir := filepath.Join(filepath.Dir(s.mw.cfg.ConfigDir()), "assets")
	fs := http.StripPrefix("/assets/", http.FileServer(http.Dir(assetsDir)))
	fs.ServeHTTP(w, r)
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		oaiError(w, http.StatusNotFound, "not found", "invalid_request_error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "Vertex AI Proxy", "version": "2.0-go"})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	log.Printf("[Server] [Health] 收到健康检查请求")
	writeJSON(w, http.StatusOK, map[string]any{
		"status":          "healthy",
		"timestamp":       time.Now().Unix(),
		"api_keys_loaded": s.mw.keys.Count(),
	})
}

func (s *Server) handleModelsOAI(w http.ResponseWriter, r *http.Request) {
	now := time.Now().Unix()
	models := s.mw.cfg.ModelsWithFakeVariants()
	log.Printf("[Server] [Models] 请求 OAI 模型列表，返回 %d 个模型", len(models))
	data := make([]any, 0, len(models))
	for _, m := range models {
		data = append(data, map[string]any{
			"id": m, "object": "model", "created": now, "owned_by": "google", "permission": []any{},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (s *Server) handleModelsGemini(w http.ResponseWriter, r *http.Request) {
	models := s.mw.cfg.ModelsWithFakeVariants()
	data := make([]any, 0, len(models))
	for _, m := range models {
		data = append(data, geminiModelInfo(m))
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": data})
}
