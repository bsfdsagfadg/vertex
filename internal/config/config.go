package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultAnonAPIKey          = "AIzaSyCI-zsRP85UVOi0DjtiCwWBwQ1djDy741g"
	defaultCountTokensQuerySig = "2/mENOSldfC+HZM+tGhVuJLrl8M6gEyK3HRjUKuA5AM58="
	maxTimeoutSeconds          = 1800
)

type AppConfig struct { //nolint:govet
	PortAPI                   int               `json:"port_api"`
	MaxRetries                int               `json:"max_retries"`
	AdminPassword             string            `json:"admin_password"`
	ProxyURL                  string            `json:"proxy_url"`
	AggregateStream           bool              `json:"aggregate_stream"`
	FakeStreamEnabled         bool              `json:"fake_stream_enabled"`
	DropMaxTokens             bool              `json:"drop_max_tokens"`
	SafetySettings            map[string]string `json:"safety_settings"`
	VertexAPIKey              string            `json:"vertex_api_key"`
	CountTokensQuerySignature string            `json:"count_tokens_query_signature"`
	MaxN                      int               `json:"max_n"`
	MaxSpillMB                int               `json:"max_spill_mb"`
	MaxRequestMB              int               `json:"max_request_mb"`
	RequestTimeout            int               `json:"request_timeout"`
	RaceTimeout               int               `json:"race_timeout"`
	StreamIdleTimeoutSeconds  int               `json:"stream_idle_timeout_seconds"` // 流式包间空闲超时（秒），防呆连接假死
	ModelTurnGuardEnabled     bool              `json:"model_turn_guard_enabled"`

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
	ParallelPoolDelayMs      int    `json:"parallel_pool_delay_ms"`

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

	DefaultImageSize          string `json:"default_image_size"`
	DefaultResponseModalities string `json:"default_response_modalities"`
}

func DefaultConfig() AppConfig {
	return AppConfig{ //nolint:exhaustruct
		PortAPI:                   2156,
		MaxRetries:                1, // 默认为 1 次
		VertexAPIKey:              defaultAnonAPIKey,
		CountTokensQuerySignature: defaultCountTokensQuerySig,
		MaxN:                      8,
		MaxSpillMB:                2048,
		RequestTimeout:            180,
		FakeStreamEnabled:         true,
		RaceTimeout:               0,
		StreamIdleTimeoutSeconds:  30,
		ModelTurnGuardEnabled:     true,
		ParallelPoolEnabled:       true,
		StickyNodePriority:        false,
		ParallelPoolSize:          15, // 默认为 15 并发
		ParallelNodeTopK:          80,
		ParallelPoolDelayDynamic:  false, // 建议默认关闭动态对冲，改为稳定的秒级接力
		ParallelPoolDelayMs:       2500,  // 固定对冲间隔设为 2500ms（2.5秒），单节点撞墙后触发接力
		BackgroundImage:           "url('background.jpg')",
		FontSize:                  "14px",
		FontColorType:             "adaptive",
		FontColor:                 "#f6f1e9",
		CustomBgPresets:           []string{},
		DefaultImageSize:          "1K",
		DefaultResponseModalities: "图文",
	}
}

var (
	//nolint:gochecknoglobals // Global configuration cache
	mu sync.Mutex
	// writeMu serializes config.json read-modify-write operations.
	writeMu sync.Mutex
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
	if err := writeSettings(updates); err != nil {
		return err
	}
	InvalidateCache()
	return nil
}

func writeSettings(updates map[string]any) error {
	writeMu.Lock()
	defer writeMu.Unlock()

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
			var needsSave bool
			// 自动补偿 RequestTimeout 默认值
			if cfg.RequestTimeout <= 0 {
				cfg.RequestTimeout = 180
				needsSave = true
			} else if cfg.RequestTimeout > maxTimeoutSeconds {
				log.Printf("[Config] 警告: 请求超时配置过高 (%d)，已限制为上限 %d", cfg.RequestTimeout, maxTimeoutSeconds)
				cfg.RequestTimeout = maxTimeoutSeconds
				needsSave = true
			}
			if cfg.RaceTimeout < 0 {
				cfg.RaceTimeout = 0
				needsSave = true
			} else if cfg.RaceTimeout > maxTimeoutSeconds {
				log.Printf("[Config] 警告: 竞速超时配置过高 (%d)，已限制为上限 %d", cfg.RaceTimeout, maxTimeoutSeconds)
				cfg.RaceTimeout = maxTimeoutSeconds
				needsSave = true
			}
			if cfg.StreamIdleTimeoutSeconds <= 0 {
				cfg.StreamIdleTimeoutSeconds = 30
				needsSave = true
			}
			if normalized := normalizeImageSizeTier(cfg.DefaultImageSize); normalized == "" {
				if cfg.DefaultImageSize != "" {
					log.Printf("[Config] default_image_size 非法 (%q)，回退 1K", cfg.DefaultImageSize)
				}
				cfg.DefaultImageSize = "1K"
				needsSave = true
			} else {
				cfg.DefaultImageSize = normalized
			}
			if cfg.DefaultResponseModalities != "图文" && cfg.DefaultResponseModalities != "仅图片" {
				if cfg.DefaultResponseModalities != "" {
					log.Printf("[Config] default_response_modalities 非法 (%q)，回退 图文", cfg.DefaultResponseModalities)
				}
				cfg.DefaultResponseModalities = "图文"
				needsSave = true
			}
			// 拦截在文件读取配置时过高的并发数限制为 20
			if cfg.ParallelPoolSize > 20 {
				log.Printf("[Config] 警告: 并发数配置过高 (%d)，已限制为上限 20", cfg.ParallelPoolSize)
				cfg.ParallelPoolSize = 20
				needsSave = true
			}
			if needsSave {
				// 在返回配置前完成回写，避免调用方读到已修正值但文件仍保留非法值。
				if errSave := writeSettings(map[string]any{
					"request_timeout":             cfg.RequestTimeout,
					"race_timeout":                cfg.RaceTimeout,
					"stream_idle_timeout_seconds": cfg.StreamIdleTimeoutSeconds,
					"parallel_pool_size":          cfg.ParallelPoolSize,
					"default_image_size":          cfg.DefaultImageSize,
					"default_response_modalities": cfg.DefaultResponseModalities,
				}); errSave != nil {
					log.Printf("[Config] 自动回写规范化配置失败: %v", errSave)
				}
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

func normalizeImageSizeTier(value string) string {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	switch normalized {
	case "512", "1K", "2K", "4K":
		return normalized
	default:
		return ""
	}
}
