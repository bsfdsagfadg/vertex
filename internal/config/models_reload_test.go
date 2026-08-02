package config

import (
	"encoding/json"
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

func TestModelsV1AutoMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	t.Setenv("VPROXY_MODELS", path)
	original := []byte(`{"models":[" gemini-3.6-flash ","custom-model","custom-model"],"alias_map":{"fast":"gemini-3.6-flash"}}`)
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	InvalidateModelsCache()

	registry := ModelRegistry()
	assertModelEntry(t, registry, "gemini-3.6-flash", true, true, true)
	assertModelEntry(t, registry, "custom-model", true, true, false)

	backup, err := os.ReadFile(path + ".v1.bak")
	if err != nil {
		t.Fatalf("读取 v1 备份: %v", err)
	}
	if string(backup) != string(original) {
		t.Fatalf("v1 备份内容变化: %s", backup)
	}

	var migrated modelsFile
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &migrated); err != nil {
		t.Fatalf("解析迁移后的 v2: %v", err)
	}
	if migrated.Version != modelsFileVersion {
		t.Fatalf("version=%d, want %d", migrated.Version, modelsFileVersion)
	}
	if countModel(migrated.Models, "custom-model") != 1 {
		t.Fatalf("custom-model 未去重: %#v", migrated.Models)
	}

	InvalidateModelsCache()
	_ = ModelRegistry()
	if countModel(ModelRegistry(), "custom-model") != 1 {
		t.Fatal("v2 二次加载不幂等")
	}
	InvalidateModelsCache()
}

func TestModelsV2MergesMissingDefaultsWithoutOverwritingState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	t.Setenv("VPROXY_MODELS", path)
	data := []byte(`{"version":2,"models":[{"id":"custom-model","enabled":false,"fake_stream_enabled":false,"trailing_fix_enabled":true}],"alias_map":{}}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	InvalidateModelsCache()

	registry := ModelRegistry()
	assertModelEntry(t, registry, "custom-model", false, false, true)
	assertModelEntry(t, registry, "gemini-3.6-flash", true, true, true)
	assertModelEntry(t, registry, "gemini-2.5-flash", true, true, false)
	if contains(BaseModels(), "custom-model") {
		t.Fatal("禁用模型不应出现在 BaseModels")
	}
	InvalidateModelsCache()
}

func TestModelsWithFakeVariantsRespectsGlobalAndModelFlags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	t.Setenv("VPROXY_MODELS", path)
	if err := WriteModelRegistry([]ModelEntry{
		{ID: "fake-on", Enabled: true, FakeStreamEnabled: true},
		{ID: "fake-off", Enabled: true, FakeStreamEnabled: false},
		{ID: "disabled", Enabled: false, FakeStreamEnabled: true},
	}, nil); err != nil {
		t.Fatal(err)
	}

	withFake := modelsWithFakeVariants(true)
	if !contains(withFake, "fake-fake-on") || !contains(withFake, "假流式-fake-on") {
		t.Fatalf("fake-on 缺少假流式变体: %v", withFake)
	}
	if contains(withFake, "fake-fake-off") || contains(withFake, "disabled") {
		t.Fatalf("禁用状态未生效: %v", withFake)
	}
	withoutFake := modelsWithFakeVariants(false)
	if contains(withoutFake, "fake-fake-on") || !contains(withoutFake, "fake-on") {
		t.Fatalf("全局假流式开关未生效: %v", withoutFake)
	}
	InvalidateModelsCache()
}

func assertModelEntry(t *testing.T, entries []ModelEntry, id string, enabled, fake, trailing bool) {
	t.Helper()
	for _, entry := range entries {
		if entry.ID == id {
			if entry.Enabled != enabled || entry.FakeStreamEnabled != fake || entry.TrailingFixEnabled != trailing {
				t.Fatalf("模型 %s 状态=%+v, want enabled=%v fake=%v trailing=%v", id, entry, enabled, fake, trailing)
			}
			return
		}
	}
	t.Fatalf("缺少模型 %s", id)
}

func countModel(entries []ModelEntry, id string) int {
	count := 0
	for _, entry := range entries {
		if entry.ID == id {
			count++
		}
	}
	return count
}
