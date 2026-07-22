// 本文件实现模型清单与别名映射的加载。
//
// models.json 形如 {"models": [...], "alias_map": {"别名": "真名"}}。
// 与 config.json 同目录解析（VPROXY_MODELS 环境变量可覆盖路径），带 60 秒内存缓存。
package config

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// fakePrefixes 是假非流模型前缀。
// 模型名以此开头表示"先完整非流式生成、再以单包 SSE 返回"。
//
//nolint:gochecknoglobals // Read-only prefix list
var fakePrefixes = []string{"假非流-"}

// FakePrefixes 返回假非流前缀列表（供 api 层剥离前缀复用，避免常量散落）。
func FakePrefixes() []string { return fakePrefixes }

// defaultModels 是 models.json 缺失/损坏时的回退清单。
//
//nolint:gochecknoglobals // Read-only default list
var defaultModels = []string{
	"gemini-2.5-flash", "gemini-2.5-flash-lite",
	"gemini-2.5-pro", "gemini-3-flash-preview",
	"gemini-3.1-flash-lite", "gemini-3.1-pro-preview",
	"gemini-3.5-flash", "gemini-3.5-flash-lite",
	"gemini-3.6-flash", "gemini-2.5-flash-image",
	"gemini-3-pro-image", "gemini-3.1-flash-image",
	"gemini-3.1-flash-lite-image", "gemini-3.1-flash-tts-preview",
}

// modelsFile 是 models.json 内容结构。
type modelsFile struct { //nolint:govet
	Models   []string          `json:"models"`
	AliasMap map[string]string `json:"alias_map"`
}

var (
	//nolint:gochecknoglobals // Global model cache
	modelsMu sync.Mutex
	//nolint:gochecknoglobals // Global model cache
	cachedModels *modelsFile
	//nolint:gochecknoglobals // Global model cache
	modelsCacheTime time.Time
)

// InvalidateModelsCache 强制清除 models.json 缓存（SIGHUP 立即热重载用）。
func InvalidateModelsCache() {
	modelsMu.Lock()
	defer modelsMu.Unlock()
	cachedModels = nil
	modelsCacheTime = time.Time{}
}

// modelsPath 解析 models.json 路径（环境变量 > exe 同级 config/ > 工作目录 config/）。
func modelsPath() string {
	if p := os.Getenv("VPROXY_MODELS"); p != "" {
		return p
	}
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), "config", "models.json")
		if _, errStat := os.Stat(p); errStat == nil { //nolint:govet
			return p
		}
	}
	return filepath.Join("config", "models.json")
}

// loadModelsFile 读 models.json（带 60 秒缓存）；文件缺失/损坏退回默认清单 + 空别名表。
func loadModelsFile() *modelsFile {
	modelsMu.Lock()
	defer modelsMu.Unlock()

	if cachedModels != nil && time.Since(modelsCacheTime) < cacheTTL {
		return cachedModels
	}

	mf := &modelsFile{Models: defaultModels, AliasMap: map[string]string{}}
	if data, err := os.ReadFile(modelsPath()); err == nil {
		var parsed modelsFile
		if errUnm := json.Unmarshal(data, &parsed); errUnm != nil { //nolint:govet
			log.Printf("[Config] 解析 models.json 失败: %v", err)
		} else if len(parsed.Models) > 0 {
			mf.Models = parsed.Models
			if parsed.AliasMap != nil {
				mf.AliasMap = parsed.AliasMap
			}
			log.Printf("[Config] 成功加载模型配置文件 models.json (模型数: %d)", len(mf.Models))
		}
	} else if !os.IsNotExist(err) {
		log.Printf("[Config] 读取 models.json 失败: %v", err)
	}
	cachedModels = mf
	modelsCacheTime = time.Now()
	return mf
}

// BaseModels 返回基础模型清单（不含假流式变体）。
func BaseModels() []string {
	mf := loadModelsFile()
	out := make([]string, len(mf.Models))
	copy(out, mf.Models)
	return out
}

// AliasMap 返回别名 → 真名的映射副本（供 admin 后台展示/编辑）。
func AliasMap() map[string]string {
	mf := loadModelsFile()
	out := make(map[string]string, len(mf.AliasMap))
	for k, v := range mf.AliasMap {
		out[k] = v
	}
	return out
}

// ModelsWithFakeVariants 返回每个基础模型 + 其假非流前缀变体的完整清单
// （result 里依次塞入 m、假非流-m）。
// /v1/models、/v1beta/models、单模型 404 校验都用它，以保证假非流变体名自洽。
func ModelsWithFakeVariants() []string {
	base := loadModelsFile().Models
	result := make([]string, 0, len(base)*2)
	for _, m := range base {
		result = append(result, m, fakePrefixes[0]+m)
	}
	return result
}

// ResolveModelName 把模型名经 alias_map 重映射。
// 命中别名返回真名，否则原样透传。
func ResolveModelName(model string) string {
	if real, ok := loadModelsFile().AliasMap[model]; ok {
		return real
	}
	return model
}

// WriteModels 把模型清单与别名映射写回 models.json（原子写）并清空缓存，使下次读取即生效。
// 写盘 + 立即热重载。aliasMap 为 nil 时写空表。
func WriteModels(models []string, aliasMap map[string]string) error {
	if aliasMap == nil {
		aliasMap = map[string]string{}
	}
	if err := writeJSONFile(modelsPath(), modelsFile{Models: models, AliasMap: aliasMap}); err != nil {
		return err
	}
	InvalidateModelsCache()
	return nil
}

func (c AppConfig) BaseModels() []string                 { return BaseModels() }
func (c AppConfig) AliasMap() map[string]string          { return AliasMap() }
func (c AppConfig) ModelsWithFakeVariants() []string     { return ModelsWithFakeVariants() }
func (c AppConfig) FakePrefixes() []string               { return FakePrefixes() }
func (c AppConfig) ResolveModelName(model string) string { return ResolveModelName(model) }
