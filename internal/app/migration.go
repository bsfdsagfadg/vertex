package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/api"
	"github.com/bsfdsagfadg/vertex/internal/buildinfo"
	"github.com/bsfdsagfadg/vertex/internal/migration"
)

type MigrationOptions struct {
	Build         buildinfo.BuildInfo
	Service       *migration.Service
	Status        migration.Status
	ShutdownGrace time.Duration
}

// Migration is an isolated Composition Root: it owns only migration HTTP and
// authentication resources and cannot start normal business dependencies.
type Migration struct {
	options MigrationOptions

	mu            sync.Mutex
	started       bool
	closed        bool
	server        *http.Server
	apiServer     *api.MigrationServer
	runtimeCancel context.CancelFunc
	rollback      chan struct{}
	closeErr      error
}

func NewMigration(options MigrationOptions) (*Migration, error) {
	if options.Service == nil {
		return nil, errors.New("migration app service is nil")
	}
	if !options.Status.Required {
		return nil, errors.New("migration app requires an active migration gate")
	}
	if options.ShutdownGrace <= 0 {
		options.ShutdownGrace = 25 * time.Second
	}
	return &Migration{options: options, rollback: make(chan struct{}, 1)}, nil
}

func (a *Migration) Start() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.started {
		return nil
	}
	if a.closed {
		return errors.New("migration app is already closed")
	}
	bootstrap := a.options.Service.LoadBootstrapConfig()
	credential, err := a.options.Service.ResolveCredential(bootstrap)
	if err != nil {
		return fmt.Errorf("initialize migration authentication: %w", err)
	}
	log.Printf("[Migration] 检测到 V1 数据，业务端点已暂停；状态=%s，管理地址=/admin/", a.options.Status.State)
	for _, finding := range a.options.Status.Findings {
		log.Printf("[Migration] 原因=%s 路径=%s", finding.Code, finding.Path)
	}
	if credential.Created {
		log.Printf("[Migration] 一次性迁移令牌（仅本次显示）: %s", credential.Secret)
		log.Printf("[Migration] 令牌文件: %s", credential.TokenPath)
	}
	migrationServer := api.NewMigrationServer(
		a.options.Service, a.options.Build, bootstrap, credential,
		api.WithRollbackPrepared(func() {
			select {
			case a.rollback <- struct{}{}:
			default:
			}
		}),
	)
	runtimeCtx, runtimeCancel := context.WithCancel(context.Background())
	if err := migrationServer.Start(runtimeCtx); err != nil {
		runtimeCancel()
		return err
	}
	a.apiServer = migrationServer
	a.runtimeCancel = runtimeCancel
	a.server = &http.Server{ //nolint:exhaustruct
		Addr:              "0.0.0.0:" + strconv.Itoa(bootstrap.Port),
		Handler:           migrationServer.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		ReadTimeout:       2 * time.Minute,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	a.started = true
	log.Printf("[Migration] 请打开 http://<host>:%d/admin/ 完成迁移", bootstrap.Port)
	return nil
}

func (a *Migration) Run(parent context.Context) error {
	if parent == nil {
		return errors.New("migration app parent context is nil")
	}
	if err := a.Start(); err != nil {
		return err
	}
	serveResult := make(chan error, 1)
	go func() {
		err := a.server.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveResult <- err
	}()
	var runErr error
	select {
	case <-parent.Done():
	case <-a.rollback:
		log.Printf("[Migration] V1 回滚数据已准备完成，正在停止迁移服务")
	case runErr = <-serveResult:
	}
	ctx, cancel := context.WithTimeout(context.Background(), a.options.ShutdownGrace)
	defer cancel()
	shutdownErr := a.Shutdown(ctx)
	select {
	case serveErr := <-serveResult:
		if runErr == nil {
			runErr = serveErr
		}
	default:
	}
	return errors.Join(runErr, shutdownErr)
}

func (a *Migration) Shutdown(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return a.closeErr
	}
	a.closed = true
	if a.server != nil {
		a.closeErr = a.server.Shutdown(ctx)
	}
	if a.runtimeCancel != nil {
		a.runtimeCancel()
	}
	if a.apiServer != nil {
		a.apiServer.Close()
	}
	a.started = false
	return a.closeErr
}
