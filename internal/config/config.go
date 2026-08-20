package config

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultAnonAPIKey                     = "AIzaSyCI-zsRP85UVOi0DjtiCwWBwQ1djDy741g"
	defaultCountTokensQuerySig            = "2/mENOSldfC+HZM+tGhVuJLrl8M6gEyK3HRjUKuA5AM58="
	maxTimeoutSeconds                     = 1800
	DefaultEntryProxyProbeIntervalSeconds = 300
	DefaultEntryProxyProbeCooldownSeconds = 60
	DefaultEntryProxyAutoDisableFailures  = 10
	MinEntryProxyProbeIntervalSeconds     = 60
	MaxEntryProxyProbeSeconds             = 86400
	MaxEntryProxyAutoDisableFailures      = 100
)

type AppConfig struct { //nolint:govet
	PortAPI                   int               `json:"port_api"`
	MaxRetries                int               `json:"max_retries"`
	AdminPassword             string            `json:"admin_password"`
	ProxyURL                  string            `json:"proxy_url"`
	GlobalProxyEnabled        bool              `json:"global_proxy_enabled"`
	GlobalProxyRequired       bool              `json:"global_proxy_required"`
	GlobalProxySelection      string            `json:"global_proxy_selection"`
	AllowDirectWithoutGlobal  bool              `json:"allow_direct_without_global_proxy"`
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
	OpenAIParameterPolicy     string            `json:"openai_parameter_policy"`
	GeminiParameterPolicy     string            `json:"gemini_parameter_policy"`
	ToolSchemaPolicy          string            `json:"tool_schema_policy"`

	// 并发池与节点锁定配置
	ActiveNodeURI                      string `json:"active_node_uri"`
	ParallelPoolEnabled                bool   `json:"parallel_pool_enabled"`
	StickyNodePriority                 bool   `json:"sticky_node_priority"`
	ParallelPoolRetryEnabled           bool   `json:"parallel_pool_retry_enabled"`
	ParallelPoolSize                   int    `json:"parallel_pool_size"`
	DebugPprof                         bool   `json:"debug_pprof"`
	ParallelNodeTopK                   int    `json:"parallel_node_top_k"`
	DebugMode                          bool   `json:"debug_mode"`
	ParallelPoolDelayDynamic           bool   `json:"parallel_pool_delay_dynamic"`
	ParallelPoolDelayMs                int    `json:"parallel_pool_delay_ms"`
	EntryProxyProbeEnabled             bool   `json:"entry_proxy_probe_enabled"`
	EntryProxyProbeIntervalSeconds     int    `json:"entry_proxy_probe_interval_seconds"`
	EntryProxyProbeCooldownSeconds     int    `json:"entry_proxy_probe_cooldown_seconds"`
	EntryProxyProbeAutoDisableEnabled  bool   `json:"entry_proxy_probe_auto_disable_enabled"`
	EntryProxyProbeAutoDisableFailures int    `json:"entry_proxy_probe_auto_disable_failures"`

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
		PortAPI:                            2156,
		MaxRetries:                         1, // 默认为 1 次
		VertexAPIKey:                       defaultAnonAPIKey,
		CountTokensQuerySignature:          defaultCountTokensQuerySig,
		MaxN:                               8,
		MaxSpillMB:                         2048,
		RequestTimeout:                     180,
		FakeStreamEnabled:                  true,
		RaceTimeout:                        0,
		StreamIdleTimeoutSeconds:           30,
		ModelTurnGuardEnabled:              true,
		OpenAIParameterPolicy:              "adaptive",
		GeminiParameterPolicy:              "passthrough",
		ToolSchemaPolicy:                   "adaptive",
		ParallelPoolEnabled:                true,
		GlobalProxyRequired:                true,
		GlobalProxySelection:               "health",
		AllowDirectWithoutGlobal:           false,
		StickyNodePriority:                 false,
		ParallelPoolSize:                   15, // 默认为 15 并发
		ParallelNodeTopK:                   80,
		ParallelPoolDelayDynamic:           false, // 建议默认关闭动态对冲，改为稳定的秒级接力
		ParallelPoolDelayMs:                2500,  // 固定对冲间隔设为 2500ms（2.5秒），单节点撞墙后触发接力
		EntryProxyProbeEnabled:             false, // 周期拨测默认关闭，避免后台自动产生入口代理流量
		EntryProxyProbeIntervalSeconds:     DefaultEntryProxyProbeIntervalSeconds,
		EntryProxyProbeCooldownSeconds:     DefaultEntryProxyProbeCooldownSeconds,
		EntryProxyProbeAutoDisableEnabled:  false,
		EntryProxyProbeAutoDisableFailures: DefaultEntryProxyAutoDisableFailures,
		BackgroundImage:                    "url('background.jpg')",
		FontSize:                           "14px",
		FontColorType:                      "adaptive",
		FontColor:                          "#f6f1e9",
		CustomBgPresets:                    []string{},
		DefaultImageSize:                   "1K",
		DefaultResponseModalities:          "图文",
	}
}

var (
	//nolint:gochecknoglobals // Global atomic configuration snapshot
	globalConfig atomic.Pointer[AppConfig]
	//nolint:gochecknoglobals // writeMu serializes config.json read-modify-write operations.
	writeMu sync.Mutex
	//nolint:gochecknoglobals // Tracks successful loads so unchanged TTL refreshes stay quiet.
	lastLoadedConfigHash [sha256.Size]byte
	//nolint:gochecknoglobals // Tracks whether lastLoadedConfigHash has been initialized.
	hasLoadedConfigHash bool
)

const cacheTTL = 60 * time.Second

func normalizeParameterPolicy(value, fallback string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "strict", "adaptive", "passthrough":
		return normalized
	default:
		return fallback
	}
}

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
	return writeSettingsUnsafe(updates)
}

func writeSettingsUnsafe(updates map[string]any) error {
	path := configPath()
	raw := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("parse existing config before update: %w", err)
		}
		if raw == nil {
			raw = map[string]any{}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read existing config before update: %w", err)
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
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", filepath.Base(path), err)
	}
	data = append(data, '\n')
	return writeBytesAtomic(path, data, true)
}

func writeBytesAtomic(path string, data []byte, createBackup bool) error {
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory for %s: %w", filepath.Base(path), err)
	}

	if createBackup {
		if current, err := os.ReadFile(path); err == nil {
			if err := writeBytesAtomic(path+".bak", current, false); err != nil {
				return fmt.Errorf("backup %s: %w", filepath.Base(path), err)
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("read %s for backup: %w", filepath.Base(path), err)
		}
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary %s: %w", filepath.Base(path), err)
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("set temporary permissions for %s: %w", filepath.Base(path), err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write temporary %s: %w", filepath.Base(path), err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync temporary %s: %w", filepath.Base(path), err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary %s: %w", filepath.Base(path), err)
	}
	if err := replaceFile(tmpPath, path); err != nil {
		return fmt.Errorf("replace %s: %w", filepath.Base(path), err)
	}
	committed = true
	if err := syncDirectory(dir); err != nil {
		return fmt.Errorf("sync directory for %s: %w", filepath.Base(path), err)

	}
	return nil
}

func cloneConfig(c *AppConfig) AppConfig {
	if c == nil {
		return DefaultConfig()
	}
	cp := *c
	if c.SafetySettings != nil {
		cp.SafetySettings = make(map[string]string, len(c.SafetySettings))
		for k, v := range c.SafetySettings {
			cp.SafetySettings[k] = v
		}
	}
	if c.CustomBgPresets != nil {
		cp.CustomBgPresets = make([]string, len(c.CustomBgPresets))
		copy(cp.CustomBgPresets, c.CustomBgPresets)
	}
	if c.AutoRefreshLogs != nil {
		v := *c.AutoRefreshLogs
		cp.AutoRefreshLogs = &v
	}
	return cp
}

func Load() AppConfig {
	if p := globalConfig.Load(); p != nil {
		return *p
	}
	return Reload()
}

// Reload forces a fresh configuration load from disk and atomically updates globalConfig.
func Reload() AppConfig {
	writeMu.Lock()
	defer writeMu.Unlock()
	cfg := DefaultConfig()
	initializeConfigFromExample()
	var saveUpdates map[string]any
	if data, err := os.ReadFile(configPath()); err == nil {
		if errUnm := json.Unmarshal(data, &cfg); errUnm != nil { //nolint:govet
			log.Printf("[Config] 解析 config.json 失败: %v", errUnm)
		} else {
			var needsSave bool
			switch strings.ToLower(strings.TrimSpace(cfg.GlobalProxySelection)) {
			case "health", "round_robin":
				cfg.GlobalProxySelection = strings.ToLower(strings.TrimSpace(cfg.GlobalProxySelection))
			default:
				cfg.GlobalProxySelection = "health"
				needsSave = true
			}
			if normalized := normalizeParameterPolicy(cfg.OpenAIParameterPolicy, "adaptive"); normalized != cfg.OpenAIParameterPolicy {
				cfg.OpenAIParameterPolicy = normalized
				needsSave = true
			}
			if normalized := normalizeParameterPolicy(cfg.GeminiParameterPolicy, "passthrough"); normalized != cfg.GeminiParameterPolicy {
				cfg.GeminiParameterPolicy = normalized
				needsSave = true
			}
			if normalized := normalizeParameterPolicy(cfg.ToolSchemaPolicy, "adaptive"); normalized != cfg.ToolSchemaPolicy {
				cfg.ToolSchemaPolicy = normalized
				needsSave = true
			}
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
			if cfg.EntryProxyProbeIntervalSeconds <= 0 {
				cfg.EntryProxyProbeIntervalSeconds = DefaultEntryProxyProbeIntervalSeconds
				needsSave = true
			} else if cfg.EntryProxyProbeIntervalSeconds < MinEntryProxyProbeIntervalSeconds {
				cfg.EntryProxyProbeIntervalSeconds = MinEntryProxyProbeIntervalSeconds
				needsSave = true
			} else if cfg.EntryProxyProbeIntervalSeconds > MaxEntryProxyProbeSeconds {
				cfg.EntryProxyProbeIntervalSeconds = MaxEntryProxyProbeSeconds
				needsSave = true
			}
			if cfg.EntryProxyProbeCooldownSeconds < 0 {
				cfg.EntryProxyProbeCooldownSeconds = 0
				needsSave = true
			} else if cfg.EntryProxyProbeCooldownSeconds > MaxEntryProxyProbeSeconds {
				cfg.EntryProxyProbeCooldownSeconds = MaxEntryProxyProbeSeconds
				needsSave = true
			}
			if cfg.EntryProxyProbeAutoDisableFailures <= 0 {
				cfg.EntryProxyProbeAutoDisableFailures = DefaultEntryProxyAutoDisableFailures
				needsSave = true
			} else if cfg.EntryProxyProbeAutoDisableFailures > MaxEntryProxyAutoDisableFailures {
				cfg.EntryProxyProbeAutoDisableFailures = MaxEntryProxyAutoDisableFailures
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
				saveUpdates = map[string]any{
					"global_proxy_selection":                  cfg.GlobalProxySelection,
					"openai_parameter_policy":                 cfg.OpenAIParameterPolicy,
					"gemini_parameter_policy":                 cfg.GeminiParameterPolicy,
					"tool_schema_policy":                      cfg.ToolSchemaPolicy,
					"request_timeout":                         cfg.RequestTimeout,
					"race_timeout":                            cfg.RaceTimeout,
					"stream_idle_timeout_seconds":             cfg.StreamIdleTimeoutSeconds,
					"parallel_pool_size":                      cfg.ParallelPoolSize,
					"entry_proxy_probe_interval_seconds":      cfg.EntryProxyProbeIntervalSeconds,
					"entry_proxy_probe_cooldown_seconds":      cfg.EntryProxyProbeCooldownSeconds,
					"entry_proxy_probe_auto_disable_failures": cfg.EntryProxyProbeAutoDisableFailures,
					"default_image_size":                      cfg.DefaultImageSize,
					"default_response_modalities":             cfg.DefaultResponseModalities,
				}
			}
			if shouldLogSuccessfulLoad(cfg) {
				log.Printf("[Config] 成功加载配置文件 config.json")
			}
		}
	} else if !os.IsNotExist(err) {
		log.Printf("[Config] 读取 config.json 失败: %v", err)
	}
	cloned := cloneConfig(&cfg)
	globalConfig.Store(&cloned)
	result := cloned

	if saveUpdates != nil {
		if errSave := writeSettingsUnsafe(saveUpdates); errSave != nil {
			log.Printf("[Config] 自动回写规范化配置失败: %v", errSave)
		}
	}
	return result
}

// shouldLogSuccessfulLoad suppresses the periodic success message when the
// cache TTL expires but the effective configuration has not changed.
// Load holds mu while calling this function.
func shouldLogSuccessfulLoad(cfg AppConfig) bool {
	data, err := json.Marshal(cfg)
	if err != nil {
		return true
	}
	hash := sha256.Sum256(data)
	if hasLoadedConfigHash && hash == lastLoadedConfigHash {
		return false
	}
	lastLoadedConfigHash = hash
	hasLoadedConfigHash = true
	return true
}

func initializeConfigFromExample() {
	path := configPath()
	if _, err := os.Stat(path); err == nil || !os.IsNotExist(err) {
		return
	}
	examplePath := filepath.Join(filepath.Dir(path), "config.example.json")
	data, err := os.ReadFile(examplePath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[Config] 读取初始配置模板失败: %v", err)
		}
		return
	}
	if err := writeBytesAtomic(path, data, false); err != nil {
		log.Printf("[Config] 从 config.example.json 创建初始配置失败: %v", err)
		return
	}
	log.Printf("[Config] 已从 config.example.json 创建初始配置")
}

func InvalidateCache() {
	globalConfig.Store(nil)
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
