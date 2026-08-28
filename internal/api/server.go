// Package api 暴露 OpenAI 兼容及 Gemini 原生 HTTP 端点。
package api

import (
	"context"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/buildinfo"
	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/db"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
	responseproto "github.com/bsfdsagfadg/vertex/internal/responses"
	"github.com/bsfdsagfadg/vertex/internal/subscriptions"
	"github.com/bsfdsagfadg/vertex/internal/transform"
	"github.com/bsfdsagfadg/vertex/internal/vertex"
)

type Server struct {
	chat              *ChatHandler
	image             *ImageHandler
	audio             *AudioHandler
	gemini            *GeminiHandler
	admin             *AdminHandler
	subscriptions     *subscriptions.Service
	mw                *middleware
	responseStore     *responseproto.ResponseStore
	backgroundMu      sync.Mutex
	backgroundCancels map[string]context.CancelFunc
	responseMaintenanceCancel context.CancelFunc
	responseMaintenanceWG sync.WaitGroup
}

func NewServer(vc *vertex.VertexAIClient, keys *APIKeyManager, cfg config.ConfigProvider) *Server {
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
		chat:              &ChatHandler{handler: h, reqConv: transform.DefaultRequestConverter(), respConv: transform.DefaultResponseConverter()},
		image:             &ImageHandler{h},
		audio:             &AudioHandler{h},
		gemini:            &GeminiHandler{h},
		admin:             adminHandler,
		subscriptions:     subscriptionService,
		mw:                &middleware{cfg: cfg, keys: keys},
		responseStore:     nil,
		backgroundCancels: make(map[string]context.CancelFunc),
	}
	if db.GlobalDB != nil { srv.responseStore = responseproto.NewResponseStore(db.GlobalDB) }
	if srv.responseStore != nil {
		mctx, cancel := context.WithCancel(context.Background()); srv.responseMaintenanceCancel = cancel; srv.responseMaintenanceWG.Add(1)
		go func() { defer srv.responseMaintenanceWG.Done(); ticker:=time.NewTicker(time.Hour); defer ticker.Stop(); for { select { case <-mctx.Done(): return; case now:=<-ticker.C: _=srv.responseStore.DeleteExpired(mctx, now) } } }()
	}
	if srv.responseStore != nil && db.GlobalDB != nil {
		if err := srv.responseStore.MarkInterrupted(context.Background(), time.Now()); err != nil {
			log.Printf("[Responses] 恢复未完成资源失败: %v", err)
		}
	}
	if err := subscriptionService.Start(context.Background()); err != nil {
		log.Printf("[Subscriptions] 启动自动更新服务失败: %v", err)
	}
	return srv
}

func (s *Server) Close() {
	if s.responseMaintenanceCancel != nil { s.responseMaintenanceCancel(); s.responseMaintenanceWG.Wait() }
	if s.subscriptions != nil {
		s.subscriptions.Stop()
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/api/meta/build", s.handleBuildMeta)
	mux.HandleFunc("/v1/models", s.handleModelsOAI)
	mux.HandleFunc("/v1beta/models", s.handleModelsGemini)
	mux.HandleFunc("/v1beta1/models", s.handleModelsGemini)
	mux.HandleFunc("/v1alpha/models", s.handleModelsGemini)
	mux.HandleFunc("/v1/chat/completions", s.chat.handleChatCompletions)
	mux.HandleFunc("/v1/completions", s.handleCompletions)
	mux.HandleFunc("/v1/responses", s.handleResponses)
	mux.HandleFunc("/v1/responses/input_tokens", s.handleResponsesInputTokens)
	mux.HandleFunc("/v1/responses/", s.handleResponseResource)
	mux.HandleFunc("/v1/conversations", s.handleConversations)
	mux.HandleFunc("/v1/conversations/", s.handleConversationResource)
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
	mux.HandleFunc("/v1beta1/models/", s.gemini.handleModelsSubtree)
	mux.HandleFunc("/v1alpha/models/", s.gemini.handleModelsSubtree)
	mux.HandleFunc("/v1/models/", s.handleV1ModelsSubtree)

	if s.mw.cfg.DebugPprof() {
		mux.HandleFunc("/debug/pprof/", pprofIndex)
		mux.HandleFunc("/debug/pprof/cmdline", pprintCmdline)
		mux.HandleFunc("/debug/pprof/profile", pprofProfile)
		mux.HandleFunc("/debug/pprof/symbol", pprofSymbol)
		mux.HandleFunc("/debug/pprof/trace", pprofTrace)
		mux.HandleFunc("/debug/pprof/goroutine", pprofGoroutine)
		mux.HandleFunc("/debug/pprof/heap", pprofHeap)
		mux.HandleFunc("/debug/pprof/threadcreate", pprofThreadcreate)
		mux.HandleFunc("/debug/pprof/block", pprofBlock)
		mux.HandleFunc("/debug/pprof/mutex", pprofMutex)
	}

	return s.mw.withRecover(s.mw.withCORS(s.mw.withMetrics(s.mw.withAPIKey(s.mw.withBodyLimit(mux)))))
}

// handleV1ModelsSubtree keeps OpenAI model detail on the non-action path while
// delegating colon actions (generateContent, streamGenerateContent, countTokens)
// to the Gemini handler.
func (s *Server) handleV1ModelsSubtree(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/models/")
	if rest != "" && !strings.Contains(rest, ":") && r.Method == http.MethodGet {
		model := strings.TrimSpace(rest)
		actual, _, ok := resolveConfiguredModel(model, s.mw.cfg)
		if !ok {
			oaiModelNotFound(w, model)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": model, "object": "model", "created": time.Now().Unix(), "owned_by": "google", "permission": []any{}, "root": actual, "parent": nil})
		return
	}
	s.gemini.handleModelsSubtree(w, r)
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
	writeJSON(w, http.StatusOK, map[string]any{"message": "Vertex AI Proxy", "version": buildinfo.Current().Version})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	log.Printf("[Server] [Health] 收到健康检查请求")
	writeJSON(w, http.StatusOK, map[string]any{
		"status":          "healthy",
		"timestamp":       time.Now().Unix(),
		"api_keys_loaded": s.mw.keys.Count(),
	})
}

func (s *Server) handleBuildMeta(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": map[string]any{"message": "method not allowed", "type": "invalid_request_error", "code": http.StatusMethodNotAllowed}})
		return
	}
	writeJSON(w, http.StatusOK, buildinfo.Current())
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
