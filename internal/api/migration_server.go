package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/admin"
	"github.com/bsfdsagfadg/vertex/internal/buildinfo"
	"github.com/bsfdsagfadg/vertex/internal/migration"
)

type MigrationServer struct {
	service            *migration.Service
	build              buildinfo.BuildInfo
	bootstrap          migration.BootstrapConfig
	credential         migration.Credential
	onRestartRequested func()
	onRollbackPrepared func()
	loginMu            sync.Mutex
	login              map[string]loginWindow
	operationsMu       sync.Mutex
	operationsCtx      context.Context
	operationsCancel   context.CancelFunc
	operationsClosed   bool
	operationsWG       sync.WaitGroup
}

type loginWindow struct {
	Started  time.Time
	Failures int
}

type MigrationServerOption func(*MigrationServer)

func WithRestartRequested(callback func()) MigrationServerOption {
	return func(server *MigrationServer) { server.onRestartRequested = callback }
}

func WithRollbackPrepared(callback func()) MigrationServerOption {
	return func(server *MigrationServer) { server.onRollbackPrepared = callback }
}

func NewMigrationServer(
	service *migration.Service,
	build buildinfo.BuildInfo,
	bootstrap migration.BootstrapConfig,
	credential migration.Credential,
	options ...MigrationServerOption,
) *MigrationServer {
	server := &MigrationServer{
		service: service, build: build, bootstrap: bootstrap, credential: credential,
		login: map[string]loginWindow{},
	}
	for _, option := range options {
		if option != nil {
			option(server)
		}
	}
	return server
}

func (s *MigrationServer) Start(ctx context.Context) error {
	if ctx == nil {
		return errors.New("migration operations context is nil")
	}
	s.operationsMu.Lock()
	defer s.operationsMu.Unlock()
	if s.operationsClosed {
		return errors.New("migration operations are already closed")
	}
	if s.operationsCancel != nil {
		return nil
	}
	s.operationsCtx, s.operationsCancel = context.WithCancel(ctx)
	return nil
}

func (s *MigrationServer) Close() {
	s.operationsMu.Lock()
	if s.operationsClosed {
		s.operationsMu.Unlock()
		s.operationsWG.Wait()
		return
	}
	s.operationsClosed = true
	cancel := s.operationsCancel
	s.operationsMu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.operationsWG.Wait()
}

func (s *MigrationServer) beginOperation() (context.Context, bool) {
	s.operationsMu.Lock()
	defer s.operationsMu.Unlock()
	if s.operationsClosed || s.operationsCtx == nil {
		return nil, false
	}
	s.operationsWG.Add(1)
	return s.operationsCtx, true
}

func (s *MigrationServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/api/meta/build", s.handleBuild)
	mux.HandleFunc("/admin", s.handleAdmin)
	mux.HandleFunc("/admin/", s.handleAdmin)
	mux.HandleFunc("/api/admin/login", s.handleLogin)
	mux.HandleFunc("/api/admin/check-auth", s.handleCheckAuth)
	for _, prefix := range []string{"/api/admin/migration/", "/admin/api/migration/"} {
		mux.HandleFunc(prefix+"status", s.requireAdmin(s.handleStatus))
		mux.HandleFunc(prefix+"prepare", s.requireAdmin(s.requireSafeMutation(s.handlePrepare)))
		mux.HandleFunc(prefix+"apply", s.requireAdmin(s.requireSafeMutation(s.handleApply)))
		mux.HandleFunc(prefix+"restart", s.requireAdmin(s.requireSafeMutation(s.handleRestart)))
		mux.HandleFunc(prefix+"rollback/prepare", s.requireAdmin(s.requireSafeMutation(s.handleRollbackPrepare)))
		mux.HandleFunc(prefix+"rollback/apply", s.requireAdmin(s.requireSafeMutation(s.handleRollbackApply)))
	}
	mux.HandleFunc("/", s.handleBlocked)
	return mux
}

func (s *MigrationServer) handleAdmin(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/admin" {
		http.Redirect(w, r, "/admin/", http.StatusFound)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/admin/")
	if name == "" {
		name = "migration.html"
	}
	allowed := map[string]bool{
		"migration.html": true, "migration.js": true, "migration.css": true,
		"admin.css": true, "background.jpg": true,
	}
	if !allowed[name] {
		s.handleBlocked(w, r)
		return
	}
	data, err := fs.ReadFile(admin.Assets, "assets/"+name)
	if err != nil {
		oaiError(w, http.StatusNotFound, "not found", "invalid_request_error")
		return
	}
	w.Header().Set("Content-Type", contentTypeFor(name))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func (s *MigrationServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	status, err := s.service.CurrentStatus(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "migration_required"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "migration_required", "migration_state": status.State})
}

func (s *MigrationServer) handleBuild(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
		return
	}
	writeJSON(w, http.StatusOK, s.build)
}

func (s *MigrationServer) handleBlocked(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]any{
		"message": "V1 data migration is required before business traffic can start",
		"type":    "migration_required", "code": "migration_required",
	}})
}

func (s *MigrationServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, adminErr("方法不允许"))
		return
	}
	remoteIP, _, _ := net.SplitHostPort(r.RemoteAddr)
	if remoteIP == "" {
		remoteIP = r.RemoteAddr
	}
	if s.loginBlocked(remoteIP) {
		writeJSON(w, http.StatusTooManyRequests, adminErr("登录尝试过多，请稍后重试"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, adminErr("请求格式错误"))
		return
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(body.Password)), []byte(s.credential.Secret)) != 1 {
		s.recordLoginFailure(remoteIP)
		writeJSON(w, http.StatusUnauthorized, adminErr("凭据错误"))
		return
	}
	s.clearLoginFailures(remoteIP)
	token := issueAdminToken()
	http.SetCookie(w, &http.Cookie{
		Name: adminCookieName, Value: token, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		MaxAge: int(adminSessionTTL / time.Second),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *MigrationServer) handleCheckAuth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": requireAdmin(r), "credential_source": s.credential.Source,
		"background_image": s.bootstrap.BackgroundImage, "font_size": s.bootstrap.FontSize,
		"font_color_type": s.bootstrap.FontColorType, "font_color": s.bootstrap.FontColor,
		"custom_bg_presets": s.bootstrap.CustomBgPresets,
	})
}

func (s *MigrationServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSON(w, http.StatusMethodNotAllowed, adminErr("方法不允许"))
		return
	}
	status, err := s.service.CurrentStatus(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *MigrationServer) handlePrepare(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, adminErr("方法不允许"))
		return
	}
	plan, err := s.service.Prepare(r.Context())
	if err != nil {
		writeJSON(w, http.StatusConflict, adminErr(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s *MigrationServer) handleApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, adminErr("方法不允许"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var body struct {
		PlanHash           string `json:"plan_hash"`
		BackupConfirmed    bool   `json:"backup_confirmed"`
		RollbackUnderstood bool   `json:"rollback_understood"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, adminErr("请求格式错误"))
		return
	}
	if !body.BackupConfirmed || !body.RollbackUnderstood || body.PlanHash == "" {
		writeJSON(w, http.StatusBadRequest, adminErr("必须确认备份与回滚说明并提交 plan_hash"))
		return
	}
	status, err := s.service.CurrentStatus(r.Context())
	forwardFailure := status.State == migration.StateFailedRecoverable && status.FailedFrom != migration.StateRollingBack
	if err != nil || (status.State != migration.StatePrepared && !forwardFailure && status.State != migration.StateFinalizing) {
		writeJSON(w, http.StatusConflict, adminErr("迁移计划未准备或状态已变化"))
		return
	}
	operationCtx, ok := s.beginOperation()
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, adminErr("迁移服务正在关闭"))
		return
	}
	go func(planHash string) {
		defer s.operationsWG.Done()
		var applyErr error
		if status.State == migration.StateFailedRecoverable || status.State == migration.StateFinalizing {
			_, applyErr = s.service.Resume(operationCtx, planHash)
		} else {
			_, applyErr = s.service.Apply(operationCtx, planHash)
		}
		if applyErr != nil {
			log.Printf("[Migration] 执行迁移失败: %v", applyErr)
		}
	}(body.PlanHash)
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "state": migration.StateMigrating})
}

func (s *MigrationServer) handleRollbackPrepare(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, adminErr("方法不允许"))
		return
	}
	plan, err := s.service.PrepareRollback(r.Context())
	if err != nil {
		writeJSON(w, http.StatusConflict, adminErr(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s *MigrationServer) handleRollbackApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, adminErr("方法不允许"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var body struct {
		PlanHash             string `json:"plan_hash"`
		V1BinaryConfirmed    bool   `json:"v1_binary_confirmed"`
		TrafficStopConfirmed bool   `json:"traffic_stop_confirmed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, adminErr("请求格式错误"))
		return
	}
	if body.PlanHash == "" || !body.V1BinaryConfirmed || !body.TrafficStopConfirmed {
		writeJSON(w, http.StatusBadRequest, adminErr("必须确认 V1 二进制可用、业务已停止并提交 plan_hash"))
		return
	}
	status, err := s.service.CurrentStatus(r.Context())
	if err != nil || (status.State != migration.StateRollbackReady &&
		(status.State != migration.StateFailedRecoverable || status.FailedFrom != migration.StateRollingBack)) {
		writeJSON(w, http.StatusConflict, adminErr("回滚计划未准备或状态已变化"))
		return
	}
	operationCtx, ok := s.beginOperation()
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, adminErr("迁移服务正在关闭"))
		return
	}
	go func(planHash string, resume bool) {
		defer s.operationsWG.Done()
		var applyErr error
		if resume {
			_, applyErr = s.service.ResumeRollback(operationCtx, planHash)
		} else {
			_, applyErr = s.service.ApplyRollback(operationCtx, planHash)
		}
		if applyErr != nil {
			log.Printf("[Migration] 准备回滚失败: %v", applyErr)
			return
		}
	}(body.PlanHash, status.State == migration.StateFailedRecoverable)
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "state": migration.StateRollingBack})
}

func (s *MigrationServer) handleRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, adminErr("方法不允许"))
		return
	}
	status, err := s.service.CurrentStatus(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr(err.Error()))
		return
	}
	var callback func()
	mode := ""
	switch status.State {
	case migration.StateCompleted:
		callback = s.onRestartRequested
		mode = "restart_v2"
	case migration.StateRollbackPrepared:
		callback = s.onRollbackPrepared
		mode = "exit_for_v1"
	default:
		writeJSON(w, http.StatusConflict, adminErr("迁移尚未完成，暂不能重启"))
		return
	}
	if callback == nil {
		writeJSON(w, http.StatusServiceUnavailable, adminErr("重启协调器不可用"))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "mode": mode})
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	// Let the client receive the accepted response before stopping its server.
	time.AfterFunc(150*time.Millisecond, callback)
}

func (s *MigrationServer) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(r) {
			writeJSON(w, http.StatusUnauthorized, adminErr("未登录或会话已过期"))
			return
		}
		next(w, r)
	}
}

func (s *MigrationServer) requireSafeMutation(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-VProxy-Action") != "migration" || !sameOriginRequest(r) {
			writeJSON(w, http.StatusForbidden, adminErr("迁移操作来源校验失败"))
			return
		}
		next(w, r)
	}
}

func sameOriginRequest(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return strings.HasPrefix(strings.ToLower(r.Header.Get("Authorization")), "bearer ")
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host) && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func (s *MigrationServer) loginBlocked(ip string) bool {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	window, ok := s.login[ip]
	if !ok || time.Since(window.Started) > 5*time.Minute {
		delete(s.login, ip)
		return false
	}
	return window.Failures >= 5
}

func (s *MigrationServer) recordLoginFailure(ip string) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	window := s.login[ip]
	if window.Started.IsZero() || time.Since(window.Started) > 5*time.Minute {
		window = loginWindow{Started: time.Now()}
	}
	window.Failures++
	s.login[ip] = window
}

func (s *MigrationServer) clearLoginFailures(ip string) {
	s.loginMu.Lock()
	delete(s.login, ip)
	s.loginMu.Unlock()
}
