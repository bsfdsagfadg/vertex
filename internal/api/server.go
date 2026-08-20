// Package api 暴露 OpenAI 兼容的 HTTP 端点。
package api

import (
	"context"
	"log"
	"net/http"
	"net/http/pprof"
	"path/filepath"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/logger"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/repository"
	"github.com/bsfdsagfadg/vertex/internal/subscriptions"
	"github.com/bsfdsagfadg/vertex/internal/transform"
	"github.com/bsfdsagfadg/vertex/internal/transport"
	"github.com/bsfdsagfadg/vertex/internal/vertex"
	"github.com/go-chi/chi/v5"
)

// ServerDependencies groups explicit dependencies injected into Server.
type ServerDependencies struct {
	Config       config.ConfigProvider
	ConfigWriter config.ConfigWriter
	VertexClient *vertex.VertexAIClient
	KeyManager   *APIKeyManager
	NodeRepo     repository.NodeRepository
	HealthRepo   repository.HealthRepository
	SubRepo      repository.SubscriptionRepository
	EntryRepo    repository.EntryProxyRepository
	TaskManager  *TaskManager
	Logger       *logger.DailyLogger
}

// ServerOption allows configuring ServerDependencies fluently.
type ServerOption func(*ServerDependencies)

// WithConfigWriter sets the ConfigWriter dependency.
func WithConfigWriter(w config.ConfigWriter) ServerOption {
	return func(d *ServerDependencies) {
		d.ConfigWriter = w
	}
}

// WithNodeRepo sets the NodeRepository dependency.
func WithNodeRepo(r repository.NodeRepository) ServerOption {
	return func(d *ServerDependencies) {
		d.NodeRepo = r
	}
}

// WithHealthRepo sets the HealthRepository dependency.
func WithHealthRepo(r repository.HealthRepository) ServerOption {
	return func(d *ServerDependencies) {
		d.HealthRepo = r
	}
}

// WithSubRepo sets the SubscriptionRepository dependency.
func WithSubRepo(r repository.SubscriptionRepository) ServerOption {
	return func(d *ServerDependencies) {
		d.SubRepo = r
	}
}

// WithEntryRepo sets the EntryProxyRepository dependency.
func WithEntryRepo(r repository.EntryProxyRepository) ServerOption {
	return func(d *ServerDependencies) {
		d.EntryRepo = r
	}
}

// WithTaskManager sets the TaskManager dependency.
func WithTaskManager(tm *TaskManager) ServerOption {
	return func(d *ServerDependencies) {
		d.TaskManager = tm
	}
}

// WithLogger sets the DailyLogger dependency.
func WithLogger(l *logger.DailyLogger) ServerOption {
	return func(d *ServerDependencies) {
		d.Logger = l
	}
}

// Server holds the HTTP handler hierarchy and runtime dependencies.
type Server struct {
	cfg          config.ConfigProvider
	cfgWriter    config.ConfigWriter
	vertexClient *vertex.VertexAIClient
	keyManager   *APIKeyManager
	nodeRepo     repository.NodeRepository
	healthRepo   repository.HealthRepository
	subRepo      repository.SubscriptionRepository
	entryRepo    repository.EntryProxyRepository
	taskManager  *TaskManager
	logger       *logger.DailyLogger

	chat          *ChatHandler
	image         *ImageHandler
	audio         *AudioHandler
	gemini        *GeminiHandler
	admin         *AdminHandler
	subscriptions *subscriptions.Service
	mw            *middleware
}

// NewServerWithDeps constructs a Server using explicit dependencies.
func NewServerWithDeps(deps ServerDependencies) *Server {
	if deps.Config == nil {
		deps.Config = config.GetProvider()
	}
	if deps.TaskManager == nil {
		deps.TaskManager = NewTaskManager()
	}

	transport.SetProxyNameResolver(nodes.GetNodeName)
	h := handler{
		vc:          deps.VertexClient,
		keys:        deps.KeyManager,
		cfg:         deps.Config,
		cfgWriter:   deps.ConfigWriter,
		nodeRepo:    deps.NodeRepo,
		healthRepo:  deps.HealthRepo,
		subRepo:     deps.SubRepo,
		entryRepo:   deps.EntryRepo,
		taskManager: deps.TaskManager,
		logger:      deps.Logger,
	}
	adminHandler := &AdminHandler{handler: h}
	subscriptionService := subscriptions.New(func(ctx context.Context, rawURL, userAgent string) ([]nodes.Node, error) {
		text, err := adminHandler.fetchSubWithFallback(ctx, rawURL, userAgent)
		if err != nil {
			return nil, err
		}
		return parseImportedNodes(text), nil
	}, subscriptions.WithSubRepo(deps.SubRepo), subscriptions.WithNodeRepo(deps.NodeRepo))
	adminHandler.subscriptionService = subscriptionService
	srv := &Server{
		cfg:           deps.Config,
		cfgWriter:     deps.ConfigWriter,
		vertexClient:  deps.VertexClient,
		keyManager:    deps.KeyManager,
		nodeRepo:      deps.NodeRepo,
		healthRepo:    deps.HealthRepo,
		subRepo:       deps.SubRepo,
		entryRepo:     deps.EntryRepo,
		taskManager:   deps.TaskManager,
		logger:        deps.Logger,
		chat:          &ChatHandler{handler: h, reqConv: transform.DefaultRequestConverter(), respConv: transform.DefaultResponseConverter()},
		image:         &ImageHandler{h},
		audio:         &AudioHandler{h},
		gemini:        &GeminiHandler{h},
		admin:         adminHandler,
		subscriptions: subscriptionService,
		mw:            &middleware{cfg: deps.Config, keys: deps.KeyManager},
	}
	if err := subscriptionService.Start(context.Background()); err != nil {
		log.Printf("[Subscriptions] 启动自动更新服务失败: %v", err)
	}
	return srv
}

// NewServer constructs a Server instance with backward-compatible parameters and optional options.
func NewServer(vc *vertex.VertexAIClient, keys *APIKeyManager, cfg config.ConfigProvider, opts ...ServerOption) *Server {
	deps := ServerDependencies{
		VertexClient: vc,
		KeyManager:   keys,
		Config:       cfg,
	}
	for _, opt := range opts {
		opt(&deps)
	}
	return NewServerWithDeps(deps)
}

// Close gracefully stops background services.
func (s *Server) Close() {
	if s.subscriptions != nil {
		s.subscriptions.Stop()
	}
}

// Handler returns the root HTTP handler configured with Chi router.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()

	r.HandleFunc("/", s.handleRoot)
	r.HandleFunc("/health", s.handleHealth)
	r.Get("/v1/models", s.handleModelsOAI)
	r.Get("/v1beta/models", s.handleModelsGemini)
	r.Post("/v1/chat/completions", s.chat.handleChatCompletions)
	r.Post("/v1/images/generations", s.image.handleImageGenerations)
	r.Post("/v1/images/edits", s.image.handleImageEdits)
	r.Post("/v1/images/variations", s.image.handleImageVariations)
	r.Post("/v1/audio/speech", s.audio.handleAudioSpeech)
	r.HandleFunc("/favicon.ico", s.handleFavicon)
	r.Get("/admin", s.admin.handleAdminPage)
	r.Get("/admin/*", s.admin.handleAdminPage)
	r.Mount("/api/admin", s.admin.Routes())
	r.Get("/assets/*", s.handleAssets)
	r.HandleFunc("/v1beta/models/*", s.gemini.handleModelsSubtree)
	r.HandleFunc("/v1/models/*", s.gemini.handleModelsSubtree)

	if s.mw.cfg.DebugPprof() {
		r.HandleFunc("/debug/pprof/", pprof.Index)
		r.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		r.HandleFunc("/debug/pprof/profile", pprof.Profile)
		r.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		r.HandleFunc("/debug/pprof/trace", pprof.Trace)
		r.HandleFunc("/debug/pprof/goroutine", pprof.Handler("goroutine").ServeHTTP)
		r.HandleFunc("/debug/pprof/heap", pprof.Handler("heap").ServeHTTP)
		r.HandleFunc("/debug/pprof/threadcreate", pprof.Handler("threadcreate").ServeHTTP)
		r.HandleFunc("/debug/pprof/block", pprof.Handler("block").ServeHTTP)
		r.HandleFunc("/debug/pprof/mutex", pprof.Handler("mutex").ServeHTTP)
	}

	return s.mw.withRecover(s.mw.withCORS(s.mw.withMetrics(s.mw.withAPIKey(s.mw.withBodyLimit(r)))))
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
