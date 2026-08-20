// Package api 暴露 OpenAI 兼容的 HTTP 端点。
//
// 里程碑1 只实现非流式 /v1/chat/completions（+ /、/health、/v1/models）。
// 真流式 SSE、图像/TTS/embeddings、Gemini 原生端点留待后续里程碑。
package api

import (
	"context"
	"encoding/base64"
	"errors"
	"log"
	"net/http"
	"net/http/pprof"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/buildinfo"
	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/repository"
	runtimeroute "github.com/bsfdsagfadg/vertex/internal/runtime/route"
	"github.com/bsfdsagfadg/vertex/internal/scheduler"
	"github.com/bsfdsagfadg/vertex/internal/subscriptions"
	"github.com/bsfdsagfadg/vertex/internal/toolstate"
	"github.com/bsfdsagfadg/vertex/internal/transform"
	"github.com/bsfdsagfadg/vertex/internal/transport"
	"github.com/bsfdsagfadg/vertex/internal/vertex"
)

type Server struct {
	chat          *ChatHandler
	image         *ImageHandler
	audio         *AudioHandler
	gemini        *GeminiHandler
	responses     *ResponsesHandler
	platform      *PlatformHandler
	admin         *AdminHandler
	subscriptions *subscriptions.Service
	mw            *middleware
	build         buildinfo.BuildInfo
	startErr      error
}

func NewServer(vc *vertex.VertexAIClient, keys *APIKeyManager, cfg config.ConfigProvider, build buildinfo.BuildInfo, stores ...toolstate.Store) *Server {
	var toolStates *toolstate.Service
	var repo *repository.SQLite
	if len(stores) > 0 {
		toolStates = toolstate.New(stores[0])
		repo, _ = stores[0].(*repository.SQLite)
	}
	var proxyManager *transport.ProxyManager
	var nodePool *runtimeroute.NodePool
	var routePlanner *scheduler.RoutePlanner
	if vc != nil {
		proxyManager = vc.ProxyManager()
		nodePool = vc.NodePool()
		routePlanner = vc.RoutePlanner()
	}
	h := handler{vc: vc, keys: keys, cfg: cfg, toolStates: toolStates, repository: repo, proxyManager: proxyManager, nodePool: nodePool, routePlanner: routePlanner}
	platformHandler, platformErr := NewPlatformHandler(h)
	h.platform = platformHandler
	adminHandler := &AdminHandler{handler: h}
	fetchSubscription := func(ctx context.Context, rawURL, userAgent string) ([]nodes.Node, error) {
		text, err := adminHandler.fetchSubWithFallback(ctx, rawURL, userAgent)
		if err != nil {
			return nil, err
		}
		return parseImportedNodes(text), nil
	}
	var subscriptionService *subscriptions.Service
	if repo != nil {
		subscriptionStore, err := subscriptions.NewRepositoryStore(repo)
		if err != nil {
			platformErr = errors.Join(platformErr, err)
		} else {
			subscriptionService = subscriptions.NewWithStores(fetchSubscription, subscriptionStore, nodePool)
		}
	}
	if subscriptionService == nil {
		subscriptionService = subscriptions.New(fetchSubscription)
	}
	adminHandler.subscriptionService = subscriptionService
	srv := &Server{
		chat:          &ChatHandler{handler: h, reqConv: transform.DefaultRequestConverter(), respConv: transform.DefaultResponseConverter()},
		image:         &ImageHandler{h},
		audio:         &AudioHandler{h},
		gemini:        &GeminiHandler{h},
		responses:     NewResponsesHandler(h),
		platform:      platformHandler,
		admin:         adminHandler,
		subscriptions: subscriptionService,
		mw:            &middleware{cfg: cfg, keys: keys},
		build:         build,
		startErr:      platformErr,
	}
	return srv
}

// Start starts control-plane background services. Construction remains free of
// goroutine and disk-loading side effects so the composition root owns the full
// application lifecycle.
func (s *Server) Start(ctx context.Context) error {
	if s.startErr != nil {
		return s.startErr
	}
	if s.responses != nil && s.responses.repository != nil {
		if err := s.responses.repository.MarkInterruptedResources(ctx, time.Now()); err != nil {
			return err
		}
	}
	if s.responses != nil {
		s.responses.Start(ctx)
	}
	if s.platform != nil {
		s.platform.Start(ctx)
	}
	if s.subscriptions == nil {
		return nil
	}
	return s.subscriptions.Start(ctx)
}

func (s *Server) Close() {
	if s.responses != nil {
		s.responses.Close()
	}
	if s.platform != nil {
		s.platform.Close()
	}
	if s.subscriptions != nil {
		s.subscriptions.Stop()
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/api/meta/build", s.handleBuildInfo)
	mux.HandleFunc("/v1/models", s.handleModelsOAI)
	mux.HandleFunc("/v1beta/models", s.handleModelsGemini)
	mux.HandleFunc("/v1/chat/completions", s.chat.handleChatCompletionsRoot)
	mux.HandleFunc("/v1/chat/completions/", s.chat.handleChatCompletionsSubtree)
	mux.HandleFunc("/v1/completions", s.chat.handleLegacyCompletions)
	if s.responses != nil {
		mux.HandleFunc("/v1/responses", s.responses.handleResponses)
		mux.HandleFunc("/v1/responses/", s.responses.handleResponsesSubtree)
		mux.HandleFunc("/v1/conversations", s.responses.handleConversations)
		mux.HandleFunc("/v1/conversations/", s.responses.handleConversationsSubtree)
		mux.HandleFunc("/v1beta/interactions", s.responses.handleInteractions)
		mux.HandleFunc("/v1beta/interactions/", s.responses.handleInteractionsSubtree)
	}
	mux.HandleFunc("/v1/images/generations", s.image.handleImageGenerations)
	mux.HandleFunc("/v1/images/edits", s.image.handleImageEdits)
	mux.HandleFunc("/v1/images/variations", s.image.handleImageVariations)
	mux.HandleFunc("/v1/audio/speech", s.audio.handleAudioSpeech)
	mux.HandleFunc("/v1/audio/transcriptions", s.audio.handleAudioTranscription)
	mux.HandleFunc("/v1/audio/translations", s.audio.handleAudioTranslation)
	if s.platform != nil {
		mux.HandleFunc("/v1/files", s.platform.handleOpenAIFiles)
		mux.HandleFunc("/v1/files/", s.platform.handleOpenAIFilesSubtree)
		mux.HandleFunc("/v1/batches", s.platform.handleBatches)
		mux.HandleFunc("/v1/batches/", s.platform.handleBatchesSubtree)
		mux.HandleFunc("/v1beta/files", s.platform.handleGeminiFiles)
		mux.HandleFunc("/v1beta/files/", s.platform.handleGeminiFilesSubtree)
		mux.HandleFunc("/upload/v1beta/files", s.platform.handleGeminiFiles)
		mux.HandleFunc("/v1beta/cachedContents", s.platform.handleCachedContents)
		mux.HandleFunc("/v1beta/cachedContents/", s.platform.handleCachedContentsSubtree)
		mux.HandleFunc("/v1beta/batches", s.platform.handleGeminiBatches)
		mux.HandleFunc("/v1beta/batches/", s.platform.handleGeminiBatchesSubtree)
	}
	mux.HandleFunc("/favicon.ico", s.handleFavicon)
	mux.HandleFunc("/admin", s.admin.handleAdminPage)
	mux.HandleFunc("/admin/", s.admin.handleAdminPage)
	mux.HandleFunc("/api/admin/", s.admin.handleAdminAPI)
	mux.HandleFunc("/assets/", s.handleAssets)
	mux.HandleFunc("/v1beta/models/", s.gemini.handleModelsSubtree)
	mux.HandleFunc("/v1/models/", s.handleModelOAI)

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

	return s.mw.withRecover(s.mw.withCORS(s.mw.withConfigSnapshot(s.mw.withMetrics(s.mw.withAPIKey(s.mw.withBodyLimit(mux))))))
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
		if endpoint, ok := lookupEndpoint(r.Method, r.URL.Path); ok && endpoint.State != EndpointSupported {
			writeUnsupportedEndpoint(w, endpoint)
			return
		}
		if _, ok := lookupEndpointPath(r.URL.Path); ok {
			oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
			return
		}
		oaiError(w, http.StatusNotFound, "not found", "invalid_request_error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "Vertex AI Proxy", "version": s.build.Version})
}

func (s *Server) handleBuildInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
		return
	}
	writeJSON(w, http.StatusOK, s.build)
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
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
		return
	}
	now := time.Now().Unix()
	models := config.FromContext(r.Context(), s.mw.cfg).BaseModels()
	log.Printf("[Server] [Models] 请求 OAI 模型列表，返回 %d 个模型", len(models))
	data := make([]any, 0, len(models))
	for _, m := range models {
		data = append(data, map[string]any{
			"id": m, "object": "model", "created": now, "owned_by": "google", "permission": []any{},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (s *Server) handleModelOAI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
		return
	}
	model := strings.TrimPrefix(r.URL.Path, "/v1/models/")
	actual, _, ok := resolveConfiguredModel(model, config.FromContext(r.Context(), s.mw.cfg))
	if !ok || model == "" {
		oaiModelNotFound(w, model)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": actual, "object": "model", "created": time.Now().Unix(), "owned_by": "google", "permission": []any{},
	})
}

func (s *Server) handleModelsGemini(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": map[string]any{
			"code": http.StatusMethodNotAllowed, "message": "method not allowed", "status": "METHOD_NOT_ALLOWED", "details": []any{},
		}})
		return
	}
	models := config.FromContext(r.Context(), s.mw.cfg).BaseModels()
	pageSize := 50
	if raw := r.URL.Query().Get("pageSize"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			pageSize = min(parsed, 1000)
		}
	}
	start := 0
	if token := r.URL.Query().Get("pageToken"); token != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{
				"code": http.StatusBadRequest, "message": "invalid pageToken", "status": "INVALID_ARGUMENT", "details": []any{},
			}})
			return
		}
		start, err = strconv.Atoi(string(decoded))
		if err != nil || start < 0 || start > len(models) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{
				"code": http.StatusBadRequest, "message": "invalid pageToken", "status": "INVALID_ARGUMENT", "details": []any{},
			}})
			return
		}
	}
	end := min(start+pageSize, len(models))
	data := make([]any, 0, end-start)
	for _, m := range models[start:end] {
		data = append(data, geminiModelInfo(m))
	}
	response := map[string]any{"models": data}
	if end < len(models) {
		response["nextPageToken"] = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end)))
	}
	writeJSON(w, http.StatusOK, response)
}
