package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestTrailingFixModels_DefaultsAndNormalization 验证尾部兼容模型清单：
// 默认值预填、WriteSettings 写入后归一化（Trim/去空/去重）、切片防御性拷贝。
func TestTrailingFixModels_DefaultsAndNormalization(t *testing.T) {
	t.Run("默认配置预填 3 个默认模型", func(t *testing.T) {
		cfg := StaticProvider(DefaultConfig())
		got := cfg.TrailingFixModels()
		want := []string{"gemini-3.5-flash-lite", "gemini-3.6-flash", "gemini-3.7-flash"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("TrailingFixModels()=%v, want %v", got, want)
		}
	})

	t.Run("WriteSettings 写入带空格/重复/空项后归一化去重并 trim", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")
		t.Setenv("VPROXY_CONFIG", path)
		InvalidateCache()

		if err := WriteSettings(map[string]any{
			"trailing_fix_models": []string{" gemini-3.6-flash ", "gemini-3.6-flash", ""},
		}); err != nil {
			t.Fatalf("WriteSettings: %v", err)
		}

		got := StaticProvider(Load()).TrailingFixModels()
		want := []string{"gemini-3.6-flash"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("TrailingFixModels()=%v, want %v", got, want)
		}

		InvalidateCache() // 清理，避免影响其它测试
	})

	t.Run("显式空数组被尊重不回填默认值", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")
		t.Setenv("VPROXY_CONFIG", path)
		if err := os.WriteFile(path, []byte(`{"trailing_fix_models":[]}`), 0o644); err != nil {
			t.Fatal(err)
		}
		InvalidateCache()

		got := StaticProvider(Load()).TrailingFixModels()
		if len(got) != 0 {
			t.Fatalf("显式 [] 应得到空清单，got %v", got)
		}

		InvalidateCache() // 清理
	})

	t.Run("返回切片为防御性拷贝", func(t *testing.T) {
		cfg := StaticProvider(DefaultConfig())
		first := cfg.TrailingFixModels()
		first[0] = "mutated"
		second := cfg.TrailingFixModels()
		if second[0] == "mutated" {
			t.Fatalf("修改返回切片不应影响内部缓存，second[0]=%q", second[0])
		}
	})
}
