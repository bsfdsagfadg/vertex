// 本文件实现模型注册表与别名映射的加载。
//
// models.json v2 由 Go 内置注册表补齐，兼容旧版字符串数组并自动备份迁移。
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const modelsFileVersion = 2

// fakePrefixes 是假流式模型前缀（中文 + ASCII）。
//
//nolint:gochecknoglobals // Read-only prefix list
var fakePrefixes = []string{"假流式-", "fake-"}

func FakePrefixes() []string { return append([]string(nil), fakePrefixes...) }

// ModelEntry 是 models.json v2 中的一条模型注册记录。
type ModelEntry struct {
	ID                 string `json:"id"`
	Enabled            bool   `json:"enabled"`
	FakeStreamEnabled  bool   `json:"fake_stream_enabled"`
	TrailingFixEnabled bool   `json:"trailing_fix_enabled"`
}

// defaultModelRegistry 是模型 ID 和缺省能力的唯一内置来源。
//
//nolint:gochecknoglobals // Read-only default registry
var defaultModelRegistry = []ModelEntry{
	{ID: "gemini-2.5-flash", Enabled: true, FakeStreamEnabled: true},
	{ID: "gemini-2.5-flash-lite", Enabled: true, FakeStreamEnabled: true},
	{ID: "gemini-2.5-flash-image", Enabled: true, FakeStreamEnabled: true},
	{ID: "gemini-2.5-pro", Enabled: true, FakeStreamEnabled: true},
	{ID: "gemini-3-flash-preview", Enabled: true, FakeStreamEnabled: true},
	{ID: "gemini-3-pro-image", Enabled: true, FakeStreamEnabled: true},
	{ID: "gemini-3.1-flash-lite", Enabled: true, FakeStreamEnabled: true},
	{ID: "gemini-3.1-flash-lite-image", Enabled: true, FakeStreamEnabled: true},
	{ID: "gemini-3.1-flash-image", Enabled: true, FakeStreamEnabled: true},
	{ID: "gemini-3.1-flash-tts-preview", Enabled: true, FakeStreamEnabled: true},
	{ID: "gemini-3.1-pro-preview", Enabled: true, FakeStreamEnabled: true},
	{ID: "gemini-3.5-flash", Enabled: true, FakeStreamEnabled: true},
	{ID: "gemini-3.5-flash-lite", Enabled: true, FakeStreamEnabled: true, TrailingFixEnabled: true},
	{ID: "gemini-3.6-flash", Enabled: true, FakeStreamEnabled: true, TrailingFixEnabled: true},
	{ID: "gemini-3.7-flash", Enabled: true, FakeStreamEnabled: true, TrailingFixEnabled: true},
	{ID: "imagen-3.0-capability", Enabled: true, FakeStreamEnabled: true},
	{ID: "imagen-4.0-generate-001", Enabled: true, FakeStreamEnabled: true},
	{ID: "imagen-4.0-ultra-generate-001", Enabled: true, FakeStreamEnabled: true},
	{ID: "imagen-4.0-fast-generate-001", Enabled: true, FakeStreamEnabled: true},
	{ID: "virtual-try-on-001", Enabled: true, FakeStreamEnabled: true},
	{ID: "lyria-002", Enabled: true, FakeStreamEnabled: true},
	{ID: "veo-2-generate-001", Enabled: true, FakeStreamEnabled: true},
	{ID: "veo-3-generate-001", Enabled: true, FakeStreamEnabled: true},
	{ID: "veo-3-fast-generate-001", Enabled: true, FakeStreamEnabled: true},
}

type modelsFile struct {
	Version    int                        `json:"version"`
	Models     []ModelEntry               `json:"models"`
	AliasMap   map[string]string          `json:"alias_map"`
	Extensions map[string]json.RawMessage `json:"-"`
}

type modelsEnvelope struct {
	Version  int               `json:"version"`
	Models   json.RawMessage   `json:"models"`
	AliasMap map[string]string `json:"alias_map"`
}

var (
	//nolint:gochecknoglobals // Global model cache
	modelsMu sync.Mutex
	//nolint:gochecknoglobals // Global model cache
	cachedModels *modelsFile
	//nolint:gochecknoglobals // Global model cache
	modelsCacheTime time.Time
	//nolint:gochecknoglobals // Serializes models.json writers independently from readers.
	modelsWriteMu sync.Mutex
)

func (mf modelsFile) MarshalJSON() ([]byte, error) {
	versionJSON, err := json.Marshal(mf.Version)
	if err != nil {
		return nil, err
	}
	modelsJSON, err := json.Marshal(mf.Models)
	if err != nil {
		return nil, err
	}
	aliasesJSON, err := json.Marshal(mf.AliasMap)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.WriteString(`{"version":`)
	out.Write(versionJSON)
	out.WriteString(`,"models":`)
	out.Write(modelsJSON)
	out.WriteString(`,"alias_map":`)
	out.Write(aliasesJSON)
	keys := make([]string, 0, len(mf.Extensions))
	for key := range mf.Extensions {
		if key != "version" && key != "models" && key != "alias_map" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		keyJSON, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		if !json.Valid(mf.Extensions[key]) {
			return nil, fmt.Errorf("invalid raw extension %q", key)
		}
		out.WriteByte(',')
		out.Write(keyJSON)
		out.WriteByte(':')
		out.Write(mf.Extensions[key])
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

func cloneModelEntries(in []ModelEntry) []ModelEntry {
	out := make([]ModelEntry, len(in))
	copy(out, in)
	return out
}

func DefaultModelRegistry() []ModelEntry { return cloneModelEntries(defaultModelRegistry) }

func InvalidateModelsCache() {
	modelsMu.Lock()
	defer modelsMu.Unlock()
	cachedModels = nil
	modelsCacheTime = time.Time{}
}

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

func defaultEntryFor(id string) ModelEntry {
	for _, entry := range defaultModelRegistry {
		if entry.ID == id {
			return entry
		}
	}
	return ModelEntry{ID: id, Enabled: true, FakeStreamEnabled: true}
}

func DefaultModelEntry(id string) ModelEntry { return defaultEntryFor(strings.TrimSpace(id)) }

func decodeModelsFile(data []byte) (mf modelsFile, migrated bool, err error) {
	mf = modelsFile{Version: modelsFileVersion, AliasMap: map[string]string{}, Extensions: map[string]json.RawMessage{}}
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "[") {
		var ids []string
		if err := json.Unmarshal(data, &ids); err != nil {
			return mf, false, err
		}
		for _, id := range ids {
			mf.Models = append(mf.Models, defaultEntryFor(strings.TrimSpace(id)))
		}
		return mf, true, nil
	}

	var env modelsEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return mf, false, err
	}
	var rawEnvelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawEnvelope); err != nil {
		return mf, false, err
	}
	delete(rawEnvelope, "version")
	delete(rawEnvelope, "models")
	delete(rawEnvelope, "alias_map")
	for key, value := range rawEnvelope {
		mf.Extensions[key] = append(json.RawMessage(nil), value...)
	}
	if env.AliasMap != nil {
		mf.AliasMap = env.AliasMap
	}

	if env.Version >= modelsFileVersion {
		if err := json.Unmarshal(env.Models, &mf.Models); err != nil {
			return mf, false, err
		}
		return mf, false, nil
	}

	var ids []string
	if err := json.Unmarshal(env.Models, &ids); err != nil {
		return mf, false, fmt.Errorf("decode v1 models: %w", err)
	}
	for _, id := range ids {
		mf.Models = append(mf.Models, defaultEntryFor(strings.TrimSpace(id)))
	}
	return mf, true, nil
}

func normalizeModelsFile(mf modelsFile) (modelsFile, int) {
	out := modelsFile{
		Version: modelsFileVersion, Models: make([]ModelEntry, 0, len(mf.Models)),
		AliasMap: map[string]string{}, Extensions: map[string]json.RawMessage{},
	}
	for key, value := range mf.Extensions {
		out.Extensions[key] = append(json.RawMessage(nil), value...)
	}
	seen := make(map[string]bool, len(mf.Models))
	for _, entry := range mf.Models {
		entry.ID = strings.TrimSpace(entry.ID)
		if entry.ID == "" || seen[entry.ID] {
			continue
		}
		seen[entry.ID] = true
		out.Models = append(out.Models, entry)
	}

	for alias, target := range mf.AliasMap {
		alias, target = strings.TrimSpace(alias), strings.TrimSpace(target)
		if alias == "" || target == "" {
			continue
		}
		out.AliasMap[alias] = target
		if !seen[target] {
			seen[target] = true
			out.Models = append(out.Models, defaultEntryFor(target))
			log.Printf("[Config] 别名 %q 的目标模型 %q 不在列表中，已按兼容默认补充", alias, target)
		}
	}

	added := 0
	for _, entry := range defaultModelRegistry {
		if seen[entry.ID] {
			continue
		}
		seen[entry.ID] = true
		out.Models = append(out.Models, entry)
		added++
	}
	return out, added
}

func backupV1Models(path string, data []byte) error {
	backupPath := path + ".v1.bak"
	f, err := os.OpenFile(backupPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.Sync()
}

func loadModelsFile() *modelsFile {
	modelsMu.Lock()
	defer modelsMu.Unlock()
	if cachedModels != nil && time.Since(modelsCacheTime) < cacheTTL {
		return cachedModels
	}

	path := modelsPath()
	mf := modelsFile{
		Version: modelsFileVersion, Models: DefaultModelRegistry(),
		AliasMap: map[string]string{}, Extensions: map[string]json.RawMessage{},
	}
	if data, err := os.ReadFile(path); err == nil {
		parsed, migrated, decodeErr := decodeModelsFile(data)
		if decodeErr != nil {
			log.Printf("[Config] 解析 models.json 失败，使用内置模型注册表: %v", decodeErr)
		} else {
			normalized, added := normalizeModelsFile(parsed)
			mf = normalized
			if migrated {
				if backupErr := backupV1Models(path, data); backupErr != nil {
					log.Printf("[Config] 备份 v1 models.json 失败，将继续使用内存迁移结果: %v", backupErr)
				} else if writeErr := writeJSONFile(path, mf); writeErr != nil {
					log.Printf("[Config] 自动迁移 models.json v2 失败，将继续使用内存迁移结果: %v", writeErr)
				} else {
					log.Printf("[Config] 已自动迁移 models.json v1 → v2")
				}
			} else if added > 0 {
				if writeErr := writeJSONFile(path, mf); writeErr != nil {
					log.Printf("[Config] 自动补充 %d 个内置模型失败: %v", added, writeErr)
				} else {
					log.Printf("[Config] 已按内置默认补充 %d 个缺失模型", added)
				}
			}
		}
	} else if !os.IsNotExist(err) {
		log.Printf("[Config] 读取 models.json 失败，使用内置模型注册表: %v", err)
	}

	cachedModels = &mf
	modelsCacheTime = time.Now()
	return cachedModels
}

// ModelRegistry 返回全部模型（包含前端禁用项）。
func ModelRegistry() []ModelEntry { return cloneModelEntries(loadModelsFile().Models) }

// BaseModels 只返回启用的基础模型。
func BaseModels() []string {
	registry := loadModelsFile().Models
	out := make([]string, 0, len(registry))
	for _, entry := range registry {
		if entry.Enabled {
			out = append(out, entry.ID)
		}
	}
	return out
}

func AliasMap() map[string]string {
	mf := loadModelsFile()
	out := make(map[string]string, len(mf.AliasMap))
	for k, v := range mf.AliasMap {
		out[k] = v
	}
	return out
}

func LookupModel(model string) (ModelEntry, bool) {
	for _, entry := range loadModelsFile().Models {
		if entry.ID == model {
			return entry, true
		}
	}
	return ModelEntry{}, false
}

func modelsWithFakeVariants(globalEnabled bool) []string {
	registry := loadModelsFile().Models
	result := make([]string, 0, len(registry)*3)
	for _, entry := range registry {
		if !entry.Enabled {
			continue
		}
		result = append(result, entry.ID)
		if globalEnabled && entry.FakeStreamEnabled {
			result = append(result, fakePrefixes[0]+entry.ID, fakePrefixes[1]+entry.ID)
		}
	}
	return result
}

func ResolveModelName(model string) string {
	if real, ok := loadModelsFile().AliasMap[model]; ok {
		return real
	}
	return model
}

func WriteModelRegistry(models []ModelEntry, aliasMap map[string]string) error {
	modelsWriteMu.Lock()
	defer modelsWriteMu.Unlock()
	extensions := map[string]json.RawMessage{}
	if data, err := os.ReadFile(modelsPath()); err == nil {
		parsed, _, decodeErr := decodeModelsFile(data)
		if decodeErr != nil {
			return fmt.Errorf("parse existing models before update: %w", decodeErr)
		}
		extensions = parsed.Extensions
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read existing models before update: %w", err)
	}
	normalized, _ := normalizeModelsFile(modelsFile{
		Version: modelsFileVersion, Models: models, AliasMap: aliasMap, Extensions: extensions,
	})
	if err := writeJSONFile(modelsPath(), normalized); err != nil {
		return err
	}
	InvalidateModelsCache()
	return nil
}

// WriteModels 保留旧管理 API/测试兼容，字符串模型默认启用全部能力。
func WriteModels(models []string, aliasMap map[string]string) error {
	entries := make([]ModelEntry, 0, len(models))
	for _, model := range models {
		entries = append(entries, defaultEntryFor(strings.TrimSpace(model)))
	}
	return WriteModelRegistry(entries, aliasMap)
}

func (c AppConfig) BaseModels() []string        { return BaseModels() }
func (c AppConfig) ModelRegistry() []ModelEntry { return ModelRegistry() }
func (c AppConfig) AliasMap() map[string]string { return AliasMap() }
func (c AppConfig) ModelsWithFakeVariants() []string {
	return modelsWithFakeVariants(c.FakeStreamEnabled)
}
func (c AppConfig) FakePrefixes() []string                      { return FakePrefixes() }
func (c AppConfig) ResolveModelName(model string) string        { return ResolveModelName(model) }
func (c AppConfig) LookupModel(model string) (ModelEntry, bool) { return LookupModel(model) }
