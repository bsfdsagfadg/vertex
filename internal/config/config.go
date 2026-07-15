package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	defaultAnonAPIKey          = "AIzaSyCI-zsRP85UVOi0DjtiCwWBwQ1djDy741g"
	defaultCountTokensQuerySig = "2/mENOSldfC+HZM+tGhVuJLrl8M6gEyK3HRjUKuA5AM58="
)

type AppConfig struct { //nolint:govet
	PortAPI                   int               `json:"port_api"`
	MaxRetries                int               `json:"max_retries"`
	AdminPassword             string            `json:"admin_password"`
	ProxyURL                  string            `json:"proxy_url"`
	AggregateStream           bool              `json:"aggregate_stream"`
	DropMaxTokens             bool              `json:"drop_max_tokens"`
	SafetySettings            map[string]string `json:"safety_settings"`
	VertexAPIKey              string            `json:"vertex_api_key"`
	CountTokensQuerySignature string            `json:"count_tokens_query_signature"`
	MaxN                      int               `json:"max_n"`
	MaxSpillMB                int               `json:"max_spill_mb"`
	MaxRequestMB              int               `json:"max_request_mb"`
	RequestTimeoutSeconds     int               `json:"request_timeout_seconds"`

	// 并发池与节点锁定配置
	ActiveNodeURI            string `json:"active_node_uri"`
	ParallelPoolEnabled      bool   `json:"parallel_pool_enabled"`
	StickyNodePriority       bool   `json:"sticky_node_priority"`
	ParallelPoolRetryEnabled bool   `json:"parallel_pool_retry_enabled"`
	ParallelPoolSize         int    `json:"parallel_pool_size"`
	DebugPprof               bool   `json:"debug_pprof"`
	ParallelNodeTopK         int    `json:"parallel_node_top_k"`
	DebugMode                bool   `json:"debug_mode"`
	ParallelPoolDelayDynamic bool   `json:"parallel_pool_delay_dynamic"`
	// 匿名遥测：仅发送实例 ID + 版本 + 平台，不含任何用户/网络/隐私数据。
	// 用于了解软件的版本分布和活跃数。指针类型区分"未设置"和"显式 false"，未设置时默认开启。
	TelemetryEnabled *bool `json:"telemetry_enabled,omitempty"`

	// 外观配置
	BackgroundImage string   `json:"background_image"`
	FontSize        string   `json:"font_size"`
	FontColorType   string   `json:"font_color_type"`
	FontColor       string   `json:"font_color"`
	CustomBgPresets []string `json:"custom_bg_presets"`
	AutoRefreshLogs *bool    `json:"auto_refresh_logs,omitempty"`
}

func DefaultConfig() AppConfig {
	return AppConfig{ //nolint:exhaustruct
		PortAPI:                   2156,
		MaxRetries:                1, // 默认为 1 次
		VertexAPIKey:              defaultAnonAPIKey,
		CountTokensQuerySignature: defaultCountTokensQuerySig,
		MaxN:                      8,
		MaxSpillMB:                2048,
		RequestTimeoutSeconds:     180,
		ParallelPoolEnabled:       true,
		StickyNodePriority:        false,
		ParallelPoolSize:          15, // 默认为 15 并发
		ParallelNodeTopK:          80,
		ParallelPoolDelayDynamic:  false, // 建议默认关闭动态对冲，改为稳定的秒级接力
		BackgroundImage:           "url('background.jpg')",
		FontSize:                  "14px",
		FontColorType:             "adaptive",
		FontColor:                 "#f6f1e9",
		CustomBgPresets:           []string{},
	}
}

var (
	//nolint:gochecknoglobals // Global configuration cache
	mu sync.Mutex
	//nolint:gochecknoglobals // Global configuration cache
	cached *AppConfig
	//nolint:gochecknoglobals // Global configuration cache
	cacheTime time.Time
)

const cacheTTL = 60 * time.Second

func configPath() string {
	if p := os.Getenv("VPROXY_CONFIG"); p != "" {
		return p
	}
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), "config", "config.json")
		if _, errStat := os.Stat(p); errStat == nil { //nolint:govet
			return p
		}
	}
	return filepath.Join("config", "config.json")
}

func ConfigPath() string { return configPath() }

func ConfigDir() string { return filepath.Dir(configPath()) }

func WriteSettings(updates map[string]any) error {
	path := configPath()
	raw := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &raw)
	}
	for k, v := range updates {
		raw[k] = v
	}

	// 拦截并在面板保存配置时限制并发上限为 20
	if val, ok := raw["parallel_pool_size"].(float64); ok && val > 20 {
		log.Printf("[Config] 面板设置并发数过高 (%v)，已强制保存为上限 20", val)
		raw["parallel_pool_size"] = 20
	} else if val, ok2 := raw["parallel_pool_size"].(int); ok2 && val > 20 { //nolint:govet
		log.Printf("[Config] 面板设置并发数过高 (%v)，已强制保存为上限 20", val)
		raw["parallel_pool_size"] = 20
	}

	if err := writeJSONFile(path, raw); err != nil {
		return err
	}
	InvalidateCache()
	return nil
}

func writeJSONFile(path string, v any) error {
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	data, _ := json.MarshalIndent(v, "", "  ")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("error: %w", err)

	}
	return os.Rename(tmp, path) //nolint:wrapcheck
}

func Load() AppConfig {
	mu.Lock()
	defer mu.Unlock()
	if cached != nil && time.Since(cacheTime) < cacheTTL {
		return *cached
	}
	cfg := DefaultConfig()
	if data, err := os.ReadFile(configPath()); err == nil {
		if errUnm := json.Unmarshal(data, &cfg); errUnm != nil { //nolint:govet
			log.Printf("[Config] 解析 config.json 失败: %v", err)
		} else {
			// 拦截在文件读取配置时过高的并发数限制为 20
			if cfg.ParallelPoolSize > 20 {
				log.Printf("[Config] 警告: 并发数配置过高 (%d)，已强制限制为上限 20", cfg.ParallelPoolSize)
				cfg.ParallelPoolSize = 20
			}
			log.Printf("[Config] 成功加载配置文件 config.json")
		}
	} else if !os.IsNotExist(err) {
		log.Printf("[Config] 读取 config.json 失败: %v", err)
	}
	cached = &cfg
	cacheTime = time.Now()
	return cfg
}

func InvalidateCache() {
	mu.Lock()
	defer mu.Unlock()
	cached = nil
}

func (c AppConfig) ConfigDir() string  { return ConfigDir() }
func (c AppConfig) ConfigPath() string { return ConfigPath() }

func (c *AppConfig) WriteSettings(updates map[string]any) error { return WriteSettings(updates) }
func (c *AppConfig) WriteModels(models []string, aliasMap map[string]string) error {
	return WriteModels(models, aliasMap)
}

func (c AppConfig) GetAutoRefreshLogs() bool {
	if c.AutoRefreshLogs == nil {
		return true
	}
	return *c.AutoRefreshLogs
}
