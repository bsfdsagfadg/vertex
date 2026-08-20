package api

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

const (
	adminCookieName          = "admin_token"
	adminSessionTTL          = 24 * time.Hour
	adminLoginWindowDuration = 5 * time.Minute
	adminLoginFailureLimit   = 5
)

type adminLoginWindow struct {
	started  time.Time
	failures int
}

func adminClientAddress(r *http.Request) string {
	if r == nil {
		return "unknown"
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	if raw := strings.TrimSpace(r.RemoteAddr); raw != "" {
		return raw
	}
	return "unknown"
}

func (adm *AdminHandler) adminLoginBlocked(address string) bool {
	adm.loginMu.Lock()
	defer adm.loginMu.Unlock()
	if adm.loginFailures == nil {
		adm.loginFailures = make(map[string]adminLoginWindow)
	}
	now := time.Now()
	window, ok := adm.loginFailures[address]
	if !ok || now.Sub(window.started) >= adminLoginWindowDuration {
		if ok {
			delete(adm.loginFailures, address)
		}
		return false
	}
	return window.failures >= adminLoginFailureLimit
}

func (adm *AdminHandler) recordAdminLoginFailure(address string) {
	adm.loginMu.Lock()
	defer adm.loginMu.Unlock()
	if adm.loginFailures == nil {
		adm.loginFailures = make(map[string]adminLoginWindow)
	}
	now := time.Now()
	window := adm.loginFailures[address]
	if window.started.IsZero() || now.Sub(window.started) >= adminLoginWindowDuration {
		window = adminLoginWindow{started: now}
	}
	window.failures++
	adm.loginFailures[address] = window
}

func (adm *AdminHandler) clearAdminLoginFailures(address string) {
	adm.loginMu.Lock()
	delete(adm.loginFailures, address)
	adm.loginMu.Unlock()
}

var (
	//nolint:gochecknoglobals // Admin sessions state
	adminSessionsMu sync.Mutex
	//nolint:gochecknoglobals // Admin sessions state
	adminSessions = map[string]time.Time{}
)

func issueAdminToken() string {
	b := make([]byte, 32)
	_, _ = cryptorand.Read(b)
	tok := hex.EncodeToString(b)
	adminSessionsMu.Lock()
	adminSessions[tok] = time.Now().Add(adminSessionTTL)
	adminSessionsMu.Unlock()
	return tok
}

func checkAdminToken(tok string) bool {
	if tok == "" {
		return false
	}
	adminSessionsMu.Lock()
	defer adminSessionsMu.Unlock()
	exp, ok := adminSessions[tok]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(adminSessions, tok)
		return false
	}
	return true
}

func dropAdminToken(tok string) {
	if tok == "" {
		return
	}
	adminSessionsMu.Lock()
	delete(adminSessions, tok)
	adminSessionsMu.Unlock()
}

func cleanupAdminSessions() int {
	now := time.Now()
	adminSessionsMu.Lock()
	defer adminSessionsMu.Unlock()
	n := 0
	for tok, exp := range adminSessions {
		if now.After(exp) {
			delete(adminSessions, tok)
			n++
		}
	}
	return n
}

func adminTokenFromRequest(r *http.Request) string {
	if c, err := r.Cookie(adminCookieName); err == nil && c.Value != "" {
		return c.Value
	}
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return ""
}

func requireAdmin(r *http.Request) bool {
	return checkAdminToken(adminTokenFromRequest(r))
}

func StartAdminSessionCleanup(interval time.Duration) {
	StartAdminSessionCleanupContext(context.Background(), interval)
}

func StartAdminSessionCleanupContext(ctx context.Context, interval time.Duration) <-chan struct{} {
	if interval <= 0 {
		interval = time.Hour
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if n := cleanupAdminSessions(); n > 0 {
					log.Printf("[Admin] 已清理 %d 个过期会话 token", n)
				}
			}
		}
	}()
	return done
}

func EnsureAdminPassword() { EnsureAdminPasswordWithProvider(config.GetProvider()) }

func EnsureAdminPasswordWithProvider(provider config.ConfigProvider) {
	if provider == nil {
		provider = config.GetProvider()
	}
	if strings.TrimSpace(provider.AdminPassword()) != "" {
		return
	}
	b := make([]byte, 9)
	if _, err := cryptorand.Read(b); err != nil {
		log.Printf("[Admin] 生成管理员密码失败：%v", err)
		return
	}
	pw := base64.RawURLEncoding.EncodeToString(b)
	writer, ok := provider.(config.ConfigWriter)
	if !ok || writer == nil {
		log.Printf("[Admin] 配置提供器不支持写入管理员密码")
		return
	}
	if err := writer.WriteSettings(map[string]any{"admin_password": pw}); err != nil {
		log.Printf("[Admin] 写入管理员密码到 config.json 失败：%v", err)
		return
	}
	bar := strings.Repeat("=", 60)
	fmt.Fprintf(os.Stderr, "\n%s\n", bar)
	fmt.Fprintln(os.Stderr, "[Admin] 首次启动，已自动生成管理员密码：")
	fmt.Fprintf(os.Stderr, "[Admin]     密码: %s\n", pw)
	fmt.Fprintln(os.Stderr, "[Admin]     访问: http://<host>:<port>/admin")
	fmt.Fprintln(os.Stderr, "[Admin]     密码已写入 config/config.json，登录后可在面板修改")
	fmt.Fprintf(os.Stderr, "%s\n\n", bar)
	log.Printf("%s", bar)
	log.Printf("[Admin] 首次启动，已自动生成管理员密码：")
	log.Printf("[Admin]     密码: %s", pw)
	log.Printf("[Admin]     访问: http://<host>:<port>/admin")
	log.Printf("[Admin]     密码已写入 config/config.json，登录后可在面板修改")
	log.Printf("%s", bar)
}

func (adm *AdminHandler) adminLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		adm.adminMethodNotAllowed(w)
		return
	}
	address := adminClientAddress(r)
	if adm.adminLoginBlocked(address) {
		w.Header().Set("Retry-After", "60")
		writeJSON(w, http.StatusTooManyRequests, adminErr("登录失败次数过多，请稍后重试"))
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	expected := strings.TrimSpace(adm.cfg.AdminPassword())
	if expected == "" {
		writeJSON(w, http.StatusInternalServerError, adminErr("管理员密码未初始化 (admin password not set)"))
		return
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(body.Password)), []byte(expected)) != 1 {
		adm.recordAdminLoginFailure(address)
		log.Printf("[Security] 警告：后台登录失败，密码错误。来源 IP: %s", r.RemoteAddr)
		writeJSON(w, http.StatusUnauthorized, adminErr("密码错误 (invalid password)"))
		return
	}
	adm.clearAdminLoginFailures(address)
	log.Printf("[Admin] 管理后台登录成功。来源 IP: %s", r.RemoteAddr)
	cleanupAdminSessions()
	tok := issueAdminToken()
	http.SetCookie(w, &http.Cookie{
		Name:     adminCookieName,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		MaxAge:   int(adminSessionTTL / time.Second),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminLogout(w http.ResponseWriter, r *http.Request) {
	dropAdminToken(adminTokenFromRequest(r))
	http.SetCookie(w, &http.Cookie{
		Name:     adminCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminCheckAuth(w http.ResponseWriter, r *http.Request) {
	authenticated := requireAdmin(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated":     authenticated,
		"background_image":  adm.cfg.BackgroundImage(),
		"font_size":         adm.cfg.FontSize(),
		"font_color_type":   adm.cfg.FontColorType(),
		"font_color":        adm.cfg.FontColor(),
		"custom_bg_presets": adm.cfg.CustomBgPresets(),
	})
}

func (adm *AdminHandler) adminChangePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	expected := strings.TrimSpace(adm.cfg.AdminPassword())
	if expected == "" {
		writeJSON(w, http.StatusInternalServerError, adminErr("未设置管理员密码"))
		return
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(body.OldPassword)), []byte(expected)) != 1 {
		writeJSON(w, http.StatusBadRequest, adminErr("原密码错误"))
		return
	}
	newPw := strings.TrimSpace(body.NewPassword)
	if len(newPw) < 6 {
		writeJSON(w, http.StatusBadRequest, adminErr("新密码不能少于 6 个字符"))
		return
	}
	if err := adm.writeSettings(map[string]any{"admin_password": newPw}); err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("写入新密码失败"))
		return
	}
	adminSessionsMu.Lock()
	adminSessions = map[string]time.Time{}
	adminSessionsMu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     adminCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		MaxAge:   -1,
	})
	log.Printf("[Security] 后台管理员密码已修改，所有在线会话已重置。来源 IP: %s", r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
