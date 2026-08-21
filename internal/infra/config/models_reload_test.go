package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestInvalidateModelsCacheReloads 验证 SIGHUP 热重载机制：
// 改 models.json 后，不失效则仍读旧缓存；调 InvalidateModelsCache 后立即读到新内容。
func TestInvalidateModelsCacheReloads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	t.Setenv("VPROXY_MODELS", path)

	// 从干净缓存开始。
	InvalidateModelsCache()
	if err := os.WriteFile(path, []byte(`{"models":["gemini-alpha"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := BaseModels(); !contains(got, "gemini-alpha") {
		t.Fatalf("初次加载应含 gemini-alpha，got %v", got)
	}

	// 改文件但不失效缓存 → 应仍是旧内容（60s TTL 内）。
	if err := os.WriteFile(path, []byte(`{"models":["gemini-beta"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := BaseModels(); contains(got, "gemini-beta") {
		t.Fatalf("未失效缓存不应立即生效，got %v", got)
	}

	// 失效后 → 新内容立即生效（模拟 SIGHUP）。
	InvalidateModelsCache()
	if got := BaseModels(); !contains(got, "gemini-beta") {
		t.Fatalf("失效后应立即读到 gemini-beta，got %v", got)
	}

	// 清理：避免影响其它用默认路径的测试。
	InvalidateModelsCache()
}

func contains(ss []string, target string) bool {
	for _, s := range ss {
		if s == target {
			return true
		}
	}
	return false
}
