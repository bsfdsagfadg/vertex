package api

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

// ---- extractAPIKey：Bearer 头 > x-goog-api-key 头 > query ?key= ----

func newReq(t *testing.T, headers map[string]string, query string) *http.Request {
	t.Helper()
	rawURL := "http://example.com/v1/chat/completions"
	if query != "" {
		rawURL += "?" + query
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	r := &http.Request{Header: http.Header{}, URL: u} //nolint:exhaustruct
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestExtractAPIKey(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		query   string
		want    string
	}{
		{ //nolint:exhaustruct
			name:    "bearer header",
			headers: map[string]string{"Authorization": "Bearer sk-abc123"},
			want:    "sk-abc123",
		},
		{ //nolint:exhaustruct
			name:    "bearer case-insensitive scheme",
			headers: map[string]string{"Authorization": "bearer sk-low"},
			want:    "sk-low",
		},
		{ //nolint:exhaustruct
			name:    "bearer trims whitespace",
			headers: map[string]string{"Authorization": "Bearer   sk-pad  "},
			want:    "sk-pad",
		},
		{
			name:    "bearer beats x-goog-api-key",
			headers: map[string]string{"Authorization": "Bearer sk-bearer", "x-goog-api-key": "sk-goog"},
			want:    "sk-bearer",
		},
		{
			name:    "x-goog-api-key when no bearer",
			headers: map[string]string{"x-goog-api-key": "sk-goog"},
			want:    "sk-goog",
		},
		{
			name:    "x-goog-api-key beats query",
			headers: map[string]string{"x-goog-api-key": "sk-goog"},
			query:   "key=sk-query",
			want:    "sk-goog",
		},
		{ //nolint:exhaustruct
			name:  "query key fallback",
			query: "key=sk-query",
			want:  "sk-query",
		},
		{
			name:    "non-bearer authorization ignored, falls to query",
			headers: map[string]string{"Authorization": "Basic xyz"},
			query:   "key=sk-query",
			want:    "sk-query",
		},
		{ //nolint:exhaustruct
			name: "nothing returns empty",
			want: "",
		},
		{
			name:    "empty authorization falls through",
			headers: map[string]string{"Authorization": ""},
			query:   "key=sk-q",
			want:    "sk-q",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newReq(t, c.headers, c.query)
			if got := extractAPIKey(r); got != c.want {
				t.Errorf("extractAPIKey=%q，期望 %q", got, c.want)
			}
		})
	}
}

// ---- LoadKeys + ValidateKey：用 VPROXY_API_KEYS 指向临时文件 ----

func TestLoadKeysAndValidate(t *testing.T) {
	dir := t.TempDir()
	keysFile := filepath.Join(dir, "api_keys.txt")
	content := "" +
		"# comment line should be skipped\n" +
		"\n" + // 空行
		"alice:sk-alice-key:Alice desc\n" +
		"bob:sk-bob-key\n" + // 两段也合法
		"baddesc:notskprefix:should be skipped\n" + // key 不以 sk- 开头 → 跳过
		"onlyonefield\n" + // 单段 → 跳过
		"  carol  :  sk-carol-key  :trimmed\n" // 含空白，应被 trim
	if err := os.WriteFile(keysFile, []byte(content), 0o600); err != nil {
		t.Fatalf("write keys file: %v", err)
	}

	t.Setenv("VPROXY_API_KEYS", keysFile)

	m := NewAPIKeyManager()
	if !m.LoadKeys() {
		t.Fatalf("LoadKeys 应返回 true")
	}

	if got := m.Count(); got != 3 {
		t.Errorf("应加载 3 个有效密钥（alice/bob/carol），实际 %d", got)
	}

	// 有效密钥校验
	for _, k := range []string{"sk-alice-key", "sk-bob-key", "sk-carol-key"} {
		if !m.ValidateKey(k) {
			t.Errorf("ValidateKey(%q) 应为 true", k)
		}
	}
	// trim 后 key 校验：ValidateKey 内部也 trim
	if !m.ValidateKey("  sk-alice-key  ") {
		t.Errorf("ValidateKey 应 trim 输入后命中")
	}
	// 无效/被跳过的
	for _, k := range []string{"notskprefix", "sk-unknown", "", "onlyonefield"} {
		if m.ValidateKey(k) {
			t.Errorf("ValidateKey(%q) 应为 false", k)
		}
	}
}

func TestLoadKeysMissingFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VPROXY_API_KEYS", filepath.Join(dir, "does-not-exist.txt"))
	m := NewAPIKeyManager()
	if m.LoadKeys() {
		t.Errorf("文件不存在时 LoadKeys 应返回 false")
	}
	if m.Count() != 0 {
		t.Errorf("文件不存在时密钥数应为 0")
	}
}

func TestValidateKeyEmpty(t *testing.T) {
	m := NewAPIKeyManager()
	if m.ValidateKey("") {
		t.Errorf("空 key 应为 false")
	}
}

// ---- admin 后台的密钥读写：List / Add / Delete（持久化到文件 + 重载内存）----

func TestAPIKeyManagerAddListDelete(t *testing.T) {
	dir := t.TempDir()
	keysFile := filepath.Join(dir, "api_keys.txt")
	t.Setenv("VPROXY_API_KEYS", keysFile)

	m := NewAPIKeyManager()

	// 文件不存在时 List 应为空、不报错。
	if entries, err := m.List(); err != nil || len(entries) != 0 {
		t.Fatalf("空文件 List 应为空切片无错，got %v err=%v", entries, err)
	}

	// 新增两个密钥。
	if err := m.Add("alice", "sk-alice", "Alice"); err != nil {
		t.Fatalf("Add alice: %v", err)
	}
	if err := m.Add("bob", "sk-bob", ""); err != nil {
		t.Fatalf("Add bob: %v", err)
	}

	entries, err := m.List()
	if err != nil || len(entries) != 2 {
		t.Fatalf("应有 2 个密钥，got %d err=%v", len(entries), err)
	}
	// Add 后应已重载内存 → ValidateKey 命中。
	if !m.ValidateKey("sk-alice") || !m.ValidateKey("sk-bob") {
		t.Fatalf("Add 后内存应已重载，密钥应可校验")
	}

	// 同名覆盖：alice 换新 key，旧 key 失效。
	if errAdd := m.Add("alice", "sk-alice2", "Alice2"); errAdd != nil {
		t.Fatalf("Add 覆盖 alice: %v", errAdd)
	}
	if m.ValidateKey("sk-alice") {
		t.Fatalf("覆盖后旧 key 应失效")
	}
	if !m.ValidateKey("sk-alice2") {
		t.Fatalf("覆盖后新 key 应有效")
	}
	if entries2, _ := m.List(); len(entries2) != 2 {
		t.Fatalf("覆盖不应新增条目，应仍为 2，got %d", len(entries2))
	}

	// 删除 bob。
	ok, err := m.Delete("bob")
	if err != nil || !ok {
		t.Fatalf("Delete bob 应成功，got ok=%v err=%v", ok, err)
	}
	if m.ValidateKey("sk-bob") {
		t.Fatalf("删除后 bob 的 key 应失效")
	}

	// 删除不存在的 → false、无错。
	if ok2, errDel := m.Delete("ghost"); errDel != nil || ok2 {
		t.Fatalf("删除不存在的应返回 false 无错，got ok=%v err=%v", ok2, errDel)
	}

	// 描述应被持久化保留。
	entries, _ = m.List()
	var found bool
	for _, e := range entries {
		if e.Name == "alice" {
			found = true
			if e.Description != "Alice2" {
				t.Errorf("alice 描述应为 Alice2，got %q", e.Description)
			}
		}
	}
	if !found {
		t.Fatalf("alice 应仍在列表中")
	}
}
