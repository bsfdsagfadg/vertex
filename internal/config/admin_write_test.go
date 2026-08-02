package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestWriteSettingsMergesAndPreservesUnknown 验证 WriteSettings：
// 合并已知字段、保留未提及字段（含 AppConfig 之外的额外字段）、写后缓存失效立即生效。
func TestWriteSettingsMergesAndPreservesUnknown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	t.Setenv("VPROXY_CONFIG", path)

	// 预置一份含额外字段 max_concurrent（admin 不认识、但不应丢）。
	initial := `{"port_api":2156,"max_retries":2,"max_concurrent":40}`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	InvalidateCache()

	if err := WriteSettings(map[string]any{"max_retries": 5, "aggregate_stream": true}); err != nil {
		t.Fatalf("WriteSettings: %v", err)
	}

	// 强类型读取：已知字段已更新。
	cfg := Load()
	if cfg.MaxRetries != 5 {
		t.Fatalf("max_retries 应为 5，got %d", cfg.MaxRetries)
	}
	if !cfg.AggregateStream {
		t.Fatalf("aggregate_stream 应为 true")
	}

	// 原始 map 读取：未提及字段 + 额外字段都应保留。
	raw := map[string]any{}
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["max_concurrent"] != float64(40) {
		t.Fatalf("额外字段 max_concurrent 应被保留为 40，got %v", raw["max_concurrent"])
	}
	if raw["port_api"] != float64(2156) {
		t.Fatalf("未提及字段 port_api 应保留为 2156，got %v", raw["port_api"])
	}

	InvalidateCache() // 清理，避免影响其它测试
}

func TestLoadInitializesMissingConfigFromExample(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	t.Setenv("VPROXY_CONFIG", path)
	t.Cleanup(InvalidateCache)
	example := `{"port_api":4321,"max_retries":3,"custom_template_field":"kept"}`
	if err := os.WriteFile(filepath.Join(dir, "config.example.json"), []byte(example), 0o644); err != nil {
		t.Fatal(err)
	}
	InvalidateCache()

	cfg := Load()
	if cfg.PortAPI != 4321 || cfg.MaxRetries != 3 {
		t.Fatalf("未从模板加载配置: port=%d retries=%d", cfg.PortAPI, cfg.MaxRetries)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("初始 config.json 未创建: %v", err)
	}
	raw := map[string]any{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["custom_template_field"] != "kept" {
		t.Fatalf("模板未知字段未保留: %#v", raw)
	}
}

// TestWriteModelsRoundTrip 验证 WriteModels：写盘 + 热重载，BaseModels/AliasMap 立即读到新值。
func TestWriteModelsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	t.Setenv("VPROXY_MODELS", path)
	InvalidateModelsCache()

	models := []string{"gemini-x", "gemini-y"}
	alias := map[string]string{"fast": "gemini-x"}
	if err := WriteModels(models, alias); err != nil {
		t.Fatalf("WriteModels: %v", err)
	}

	if got := BaseModels(); !contains(got, "gemini-x") || !contains(got, "gemini-y") {
		t.Fatalf("BaseModels 应含写入的模型，got %v", got)
	}
	if got := AliasMap(); got["fast"] != "gemini-x" {
		t.Fatalf("AliasMap 应含 fast→gemini-x，got %v", got)
	}
	if ResolveModelName("fast") != "gemini-x" {
		t.Fatalf("别名 fast 应解析为 gemini-x")
	}

	// nil aliasMap 应写空表、不报错。
	if err := WriteModels([]string{"only-one"}, nil); err != nil {
		t.Fatalf("WriteModels nil alias: %v", err)
	}
	if got := AliasMap(); len(got) != 0 {
		t.Fatalf("nil aliasMap 应写空表，got %v", got)
	}

	InvalidateModelsCache() // 清理
}
