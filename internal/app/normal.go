package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/api"
	"github.com/bsfdsagfadg/vertex/internal/buildinfo"
	"github.com/bsfdsagfadg/vertex/internal/cli"
	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/logger"
	"github.com/bsfdsagfadg/vertex/internal/repository"
	runtimeroute "github.com/bsfdsagfadg/vertex/internal/runtime/route"
	"github.com/bsfdsagfadg/vertex/internal/scheduler"
	"github.com/bsfdsagfadg/vertex/internal/spool"
	"github.com/bsfdsagfadg/vertex/internal/transport"
	"github.com/bsfdsagfadg/vertex/internal/vertex"
)

type NormalOptions struct {
	Build         buildinfo.BuildInfo
	ConfigDir     string
	ShutdownGrace time.Duration
}

// Normal is the normal-mode Composition Root. NewNormal is side-effect free;
// all file, goroutine, repository and listener ownership begins in Start/Run.
type Normal struct {
	options NormalOptions

	mu       sync.Mutex
	started  bool
	closed   bool
	closeErr error

	runtimeCancel context.CancelFunc
	dailyLogger   *logger.DailyLogger
	repository    *repository.SQLite
	vertex        *vertex.VertexAIClient
	nodePool      *runtimeroute.NodePool
	routePlanner  *scheduler.RoutePlanner
	proxyManager  *transport.ProxyManager
	apiServer     *api.Server
	httpServer    *http.Server

	proxyGCDone          <-chan struct{}
	sessionCleanupDone   <-chan struct{}
	entryProbeDone       <-chan struct{}
	reloadDone           <-chan struct{}
	toolStateCleanupDone <-chan struct{}
}

func NewNormal(options NormalOptions) (*Normal, error) {
	if strings.TrimSpace(options.ConfigDir) == "" {
		return nil, errors.New("normal app config directory is empty")
	}
	if options.ShutdownGrace <= 0 {
		options.ShutdownGrace = 25 * time.Second
	}
	return &Normal{options: options}, nil
}

func (a *Normal) Start() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.started {
		return nil
	}
	if a.closed {
		return errors.New("normal app is already closed")
	}
	startComplete := false
	defer func() {
		if !startComplete {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), a.options.ShutdownGrace)
			defer cancel()
			_ = a.stopStartedLocked(cleanupCtx)
		}
	}()

	logDir := filepath.Join(filepath.Dir(a.options.ConfigDir), "logs")
	a.dailyLogger = logger.NewDailyLogger(logDir)
	cli.InitTracker(a.dailyLogger)
	log.Printf("[vproxy] build_info version=%s commit=%s build_time=%s dirty=%t source=%s",
		a.options.Build.Version, a.options.Build.Commit, a.options.Build.BuildTime,
		a.options.Build.Dirty, a.options.Build.Source)
	cli.SetAppInfo(a.options.Build, runtime.GOOS, runtime.GOARCH)

	cfg := config.GetProvider()
	repo, err := repository.Open(filepath.Join(a.options.ConfigDir, "data.db"))
	if err != nil {
		return fmt.Errorf("open V2 repository: %w", err)
	}
	a.repository = repo
	spool.SetMaxSpillBytes(int64(cfg.MaxSpillMB()) << 20)
	nodePool, err := runtimeroute.NewNodePool(repo)
	if err != nil {
		return fmt.Errorf("create request node runtime: %w", err)
	}
	routePlanner, err := scheduler.NewRoutePlanner(repo, scheduler.NewHealthTracker())
	if err != nil {
		return fmt.Errorf("create route planner: %w", err)
	}
	a.nodePool = nodePool
	a.routePlanner = routePlanner
	proxyManager := transport.NewProxyManager(func(uri string) string { return resolveProxyNameFromRepository(repo, nodePool, uri) })
	a.proxyManager = proxyManager
	nodePool.SetDeleteCallback(proxyManager.RemoveProxy)

	runtimeCtx, runtimeCancel := context.WithCancel(context.Background())
	a.runtimeCancel = runtimeCancel
	a.toolStateCleanupDone = startToolStateCleanup(runtimeCtx, repo)
	a.proxyGCDone = proxyManager.StartGC(runtimeCtx, 5*time.Minute, 30*time.Minute)

	keys := api.NewAPIKeyManager()
	keys.LoadKeys()
	api.EnsureAdminPasswordWithProvider(cfg)
	a.sessionCleanupDone = api.StartAdminSessionCleanupContext(runtimeCtx, time.Hour)

	a.vertex = vertex.NewVertexAIClient(cfg, vertex.WithNodePool(nodePool), vertex.WithRoutePlanner(routePlanner), vertex.WithProxyManager(proxyManager))
	a.entryProbeDone = api.StartEntryProxyProbeLoopContextV2(runtimeCtx, a.vertex.Net(), cfg, routePlanner)
	a.apiServer = api.NewServer(a.vertex, keys, cfg, a.options.Build, repo)
	if err := a.apiServer.Start(runtimeCtx); err != nil {
		return fmt.Errorf("start subscription service: %w", err)
	}
	a.httpServer = &http.Server{ //nolint:exhaustruct
		Addr:              "0.0.0.0:" + strconv.Itoa(cfg.PortAPI()),
		Handler:           a.apiServer.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		ReadTimeout:       2 * time.Minute,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	a.reloadDone = startReloadLoop(runtimeCtx)
	a.started = true

	pool := "关闭"
	if cfg.ParallelPoolEnabled() {
		pool = strconv.Itoa(cfg.ParallelPoolSize()) + "（开启）"
	}
	log.Printf("[vproxy] 监听 %s（API 密钥 %d 个，max_retries=%d，并发池=%s）",
		a.httpServer.Addr, keys.Count(), cfg.MaxRetries(), pool)
	startComplete = true
	return nil
}

func (a *Normal) Run(parent context.Context) error {
	if parent == nil {
		return errors.New("normal app parent context is nil")
	}
	if err := a.Start(); err != nil {
		return err
	}
	serveResult := make(chan error, 1)
	go func() {
		err := a.httpServer.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveResult <- err
	}()

	var runErr error
	select {
	case <-parent.Done():
		log.Printf("[vproxy] 收到退出信号：开始关闭程序，等待在途请求处理完成(最长 %s)…", a.options.ShutdownGrace)
	case <-cli.TUIDone():
		log.Printf("[vproxy] 收到 TUI Ctrl+C：开始关闭程序，等待在途请求处理完成(最长 %s)…", a.options.ShutdownGrace)
	case runErr = <-serveResult:
		if runErr != nil {
			log.Printf("[vproxy] HTTP 服务退出: %v", runErr)
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.options.ShutdownGrace)
	defer cancel()
	shutdownErr := a.Shutdown(shutdownCtx)
	select {
	case serveErr := <-serveResult:
		if runErr == nil {
			runErr = serveErr
		}
	default:
	}
	return errors.Join(runErr, shutdownErr)
}

func (a *Normal) Shutdown(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return a.closeErr
	}
	a.closed = true
	var shutdownErr error
	if a.httpServer != nil {
		shutdownErr = a.httpServer.Shutdown(ctx)
	}
	cleanupErr := a.stopStartedLocked(ctx)
	a.closeErr = errors.Join(shutdownErr, cleanupErr)
	return a.closeErr
}

func (a *Normal) stopStartedLocked(ctx context.Context) error {
	var cleanupErr error
	if a.runtimeCancel != nil {
		a.runtimeCancel()
	}
	if a.apiServer != nil {
		a.apiServer.Close()
	}
	cleanupErr = errors.Join(cleanupErr, waitDone(ctx, "entry proxy probe", a.entryProbeDone))
	cleanupErr = errors.Join(cleanupErr, waitDone(ctx, "proxy GC", a.proxyGCDone))
	cleanupErr = errors.Join(cleanupErr, waitDone(ctx, "admin session cleanup", a.sessionCleanupDone))
	cleanupErr = errors.Join(cleanupErr, waitDone(ctx, "config reload loop", a.reloadDone))
	cleanupErr = errors.Join(cleanupErr, waitDone(ctx, "tool state cleanup", a.toolStateCleanupDone))
	if a.proxyManager != nil {
		a.proxyManager.Close()
	}
	if cleanupErr == nil {
		if a.repository != nil {
			if err := a.repository.Checkpoint(ctx); err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("repository checkpoint: %w", err))
			}
			if cleanupErr == nil {
				if err := a.repository.Close(); err != nil {
					cleanupErr = errors.Join(cleanupErr, fmt.Errorf("repository close: %w", err))
				}
			}
		}
	}
	cli.StopTUI()
	log.SetOutput(os.Stderr)
	if a.dailyLogger != nil {
		if err := a.dailyLogger.Close(); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("logger close: %w", err))
		}
	}
	a.started = false
	return cleanupErr
}

func waitDone(ctx context.Context, name string, done <-chan struct{}) error {
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for %s: %w", name, ctx.Err())
	}
}

func resolveProxyNameFromRepository(repo *repository.SQLite, nodePool *runtimeroute.NodePool, uri string) string {
	if name := nodePool.NodeName(uri); name != "" && name != "Unknown" {
		return name
	}
	proxies, err := repo.ListGlobalProxies(context.Background())
	if err != nil {
		return "Unknown"
	}
	for _, proxy := range proxies {
		if proxy.RawURI == uri && strings.TrimSpace(proxy.Name) != "" {
			return proxy.Name
		}
	}
	return "Unknown"
}

func startReloadLoop(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGHUP)
	go func() {
		defer close(done)
		defer signal.Stop(signals)
		for {
			select {
			case <-ctx.Done():
				return
			case <-signals:
				config.InvalidateCache()
				config.InvalidateModelsCache()
				log.Printf("[vproxy] 收到 SIGHUP：已清配置/模型缓存，下次读取即热重载")
			}
		}
	}()
	return done
}

func startToolStateCleanup(ctx context.Context, repo *repository.SQLite) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = repo.DeleteExpiredToolStates(ctx, time.Now())
		_ = repo.DeleteExpiredResources(ctx, time.Now())
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if err := repo.DeleteExpiredResources(ctx, now); err != nil {
					log.Printf("[Resources] cleanup failed: %v", err)
				}
				if deleted, err := repo.DeleteExpiredToolStates(ctx, now); err != nil {
					log.Printf("[ToolState] cleanup failed: %v", err)
				} else if deleted > 0 {
					log.Printf("[ToolState] removed %d expired entries", deleted)
				}
			}
		}
	}()
	return done
}
