package api

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/infra/config"
)

// resetAdminSessions 清空包级 session 表，避免用例间互相污染。
func resetAdminSessions() {
	adminSessionsMu.Lock()
	adminSessions = map[string]time.Time{}
	adminSessionsMu.Unlock()
}

// ---- session token：生成 / 校验 / 过期 / 登出 ----

func TestAdminSessionLifecycle(t *testing.T) {
	resetAdminSessions()

	tok := issueAdminToken()
	if len(tok) != 64 { // 32 字节 → 64 hex 字符
		t.Fatalf("token 应为 64 个 hex 字符（32 字节），实际 %d", len(tok))
	}
	if !checkAdminToken(tok) {
		t.Fatalf("刚签发的 token 应有效")
	}
	// 两次签发不应相同（crypto/rand）。
	if tok2 := issueAdminToken(); tok2 == tok {
		t.Fatalf("两次签发的 token 不应相同")
	}

	// 空 token / 未知 token 无效。
	if checkAdminToken("") || checkAdminToken("deadbeef") {
		t.Fatalf("空/未知 token 应无效")
	}

	// 登出后失效。
	dropAdminToken(tok)
	if checkAdminToken(tok) {
		t.Fatalf("dropAdminToken 后 token 应失效")
	}
}

func TestAdminSessionExpiry(t *testing.T) {
	resetAdminSessions()

	tok := "expired-token"
	adminSessionsMu.Lock()
	adminSessions[tok] = time.Now().Add(-time.Minute) // 已过期
	adminSessionsMu.Unlock()

	if checkAdminToken(tok) {
		t.Fatalf("过期 token 应无效")
	}
	// 校验过期 token 时应顺手删除它。
	adminSessionsMu.Lock()
	_, stillThere := adminSessions[tok]
	adminSessionsMu.Unlock()
	if stillThere {
		t.Fatalf("校验时应删除过期 token")
	}
}

func TestCleanupAdminSessions(t *testing.T) {
	resetAdminSessions()

	now := time.Now()
	adminSessionsMu.Lock()
	adminSessions["live1"] = now.Add(time.Hour)
	adminSessions["live2"] = now.Add(time.Hour)
	adminSessions["dead1"] = now.Add(-time.Hour)
	adminSessions["dead2"] = now.Add(-time.Second)
	adminSessionsMu.Unlock()

	if n := cleanupAdminSessions(); n != 2 {
		t.Fatalf("应清理 2 个过期 token，实际 %d", n)
	}
	adminSessionsMu.Lock()
	left := len(adminSessions)
	adminSessionsMu.Unlock()
	if left != 2 {
		t.Fatalf("清理后应剩 2 个有效 token，实际 %d", left)
	}
}

func TestStartAdminSessionCleanup(t *testing.T) {
	resetAdminSessions()
	now := time.Now()
	adminSessionsMu.Lock()
	adminSessions["dead"] = now.Add(-time.Hour)
	adminSessionsMu.Unlock()

	stop := StartAdminSessionCleanup(10 * time.Millisecond)
	time.Sleep(30 * time.Millisecond)
	stop()

	adminSessionsMu.Lock()
	left := len(adminSessions)
	adminSessionsMu.Unlock()
	if left != 0 {
		t.Fatalf("StartAdminSessionCleanup 应定期清理过期会话，实际剩余 %d", left)
	}
}

// ---- key 脱敏 ----

func TestMaskKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"sk-abcdef123456", "sk-····3456"},
		{"sk-12345", "sk-····2345"},
		{"sk-1", "sk-····"}, // 短于等于 4 位整段打码
		{"abcd", "sk-····"},
		{"", "sk-····"},
	}
	for _, c := range cases {
		if got := maskKey(c.in); got != c.want {
			t.Errorf("maskKey(%q)=%q，期望 %q", c.in, got, c.want)
		}
	}
}

// ---- generateAPIKey ----

func TestGenerateAPIKey(t *testing.T) {
	k1 := generateAPIKey()
	k2 := generateAPIKey()
	if !strings.HasPrefix(k1, "sk-") {
		t.Fatalf("生成的 key 应以 sk- 开头，实际 %q", k1)
	}
	if len(k1) != 3+48 { // "sk-" + 24 字节 hex(48 字符)
		t.Fatalf("生成的 key 长度应为 51，实际 %d", len(k1))
	}
	if k1 == k2 {
		t.Fatalf("两次生成的 key 不应相同")
	}
}

// ---- EnsureAdminPassword：空密码时生成并写回 config.json ----

func TestEnsureAdminPasswordGenerates(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	t.Setenv("VPROXY_CONFIG", cfgPath)
	t.Setenv("PROXY_URL", "") // 确保不被环境覆盖干扰
	config.InvalidateCache()

	// 初始无配置文件 → admin_password 为空 → 应生成。
	EnsureAdminPassword()

	cfg := config.Load()
	if strings.TrimSpace(cfg.AdminPassword) == "" {
		t.Fatalf("EnsureAdminPassword 后 admin_password 不应为空")
	}
	generated := cfg.AdminPassword

	// 第二次调用应保留已有密码（不覆盖）。
	config.InvalidateCache()
	EnsureAdminPassword()
	if got := config.Load().AdminPassword; got != generated {
		t.Fatalf("已有密码不应被覆盖：原 %q，现 %q", generated, got)
	}
}
