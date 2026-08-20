package api

import (
	"net/http"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

//nolint:gochecknoglobals // Constant-like map of allowed settings
var adminAllowedSettings = map[string]bool{
	"max_retries": true, "max_spill_mb": true,
	"max_request_mb": true, "max_n": true, "aggregate_stream": true,
	"fake_stream_enabled":               true,
	"drop_max_tokens":                   true,
	"global_proxy_enabled":              true,
	"global_proxy_required":             true,
	"global_proxy_selection":            true,
	"allow_direct_without_global_proxy": true,
	"request_timeout":                   true,
	"race_timeout":                      true,
	"model_turn_guard_enabled":          true,
	"openai_parameter_policy":           true,
	"gemini_parameter_policy":           true,
	"tool_schema_policy":                true,
	"parallel_pool_enabled":             true, "parallel_pool_size": true,
	"parallel_pool_delay_dynamic":             true,
	"entry_proxy_probe_enabled":               true,
	"entry_proxy_probe_interval_seconds":      true,
	"entry_proxy_probe_cooldown_seconds":      true,
	"entry_proxy_probe_auto_disable_enabled":  true,
	"entry_proxy_probe_auto_disable_failures": true,
	"parallel_pool_delay_ms":                  true,
	"active_node_uri":                         true,
	"sticky_node_priority":                    true,
	"parallel_pool_retry_enabled":             true,
	"background_image":                        true,
	"font_size":                               true,
	"font_color_type":                         true,
	"font_color":                              true,
	"custom_bg_presets":                       true,
	"debug_mode":                              true,
	"auto_refresh_logs":                       true,
	"default_image_size":                      true,
	"default_response_modalities":             true,
}

func (adm *AdminHandler) adminGetSettings(w http.ResponseWriter, r *http.Request) {
	globalProxies, err := adm.listGlobalProxies(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("加载全局代理失败: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": map[string]any{
		"max_retries":              adm.cfg.MaxRetries(),
		"max_spill_mb":             adm.cfg.MaxSpillMB(),
		"max_request_mb":           adm.cfg.MaxRequestMB(),
		"max_n":                    adm.cfg.MaxN(),
		"aggregate_stream":         adm.cfg.AggregateStream(),
		"fake_stream_enabled":      adm.cfg.FakeStreamEnabled(),
		"drop_max_tokens":          adm.cfg.DropMaxTokens(),
		"request_timeout":          adm.cfg.RequestTimeout(),
		"race_timeout":             adm.cfg.RaceTimeout(),
		"model_turn_guard_enabled": adm.cfg.ModelTurnGuardEnabled(),
		"openai_parameter_policy":  adm.cfg.OpenAIParameterPolicy(),
		"gemini_parameter_policy":  adm.cfg.GeminiParameterPolicy(),
		"tool_schema_policy":       adm.cfg.ToolSchemaPolicy(),
		"parallel_pool_enabled":    adm.cfg.ParallelPoolEnabled(), "parallel_pool_size": adm.cfg.ParallelPoolSize(), "active_node_uri": adm.cfg.ActiveNodeURI(),
		"global_proxy_enabled":                    adm.cfg.GlobalProxyEnabled(),
		"global_proxy_required":                   adm.cfg.GlobalProxyRequired(),
		"global_proxy_selection":                  adm.cfg.GlobalProxySelection(),
		"allow_direct_without_global_proxy":       adm.cfg.AllowDirectWithoutGlobalProxy(),
		"proxy_url_candidates":                    globalProxies,
		"parallel_pool_delay_dynamic":             adm.cfg.ParallelPoolDelayDynamic(),
		"parallel_pool_delay_ms":                  adm.cfg.ParallelPoolDelayMs(),
		"entry_proxy_probe_enabled":               adm.cfg.EntryProxyProbeEnabled(),
		"entry_proxy_probe_interval_seconds":      adm.cfg.EntryProxyProbeIntervalSeconds(),
		"entry_proxy_probe_cooldown_seconds":      adm.cfg.EntryProxyProbeCooldownSeconds(),
		"entry_proxy_probe_auto_disable_enabled":  adm.cfg.EntryProxyProbeAutoDisableEnabled(),
		"entry_proxy_probe_auto_disable_failures": adm.cfg.EntryProxyProbeAutoDisableFailures(),
		"sticky_node_priority":                    adm.cfg.StickyNodePriority(),
		"parallel_pool_retry_enabled":             adm.cfg.ParallelPoolRetryEnabled(),
		"background_image":                        adm.cfg.BackgroundImage(),
		"font_size":                               adm.cfg.FontSize(),
		"font_color_type":                         adm.cfg.FontColorType(),
		"font_color":                              adm.cfg.FontColor(),
		"custom_bg_presets":                       adm.cfg.CustomBgPresets(),
		"debug_mode":                              adm.cfg.DebugMode(),
		"auto_refresh_logs":                       adm.cfg.AutoRefreshLogs(),
		"default_image_size":                      adm.cfg.DefaultImageSize(),
		"default_response_modalities":             adm.cfg.DefaultResponseModalities(),
	}})
}

func (adm *AdminHandler) adminPutSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Settings map[string]any `json:"settings"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	updates := map[string]any{}

	// 面板依赖校验：禁用并发池时强制禁用粘性池
	if ppEnabled, ok := body.Settings["parallel_pool_enabled"].(bool); ok && !ppEnabled {
		body.Settings["sticky_node_priority"] = false
	}

	for k, v := range body.Settings {
		if !adminAllowedSettings[k] {
			continue
		}
		switch k {
		case "max_retries", "max_spill_mb", "max_request_mb", "max_n", "parallel_pool_size", "parallel_pool_delay_ms", "request_timeout", "race_timeout",
			"entry_proxy_probe_interval_seconds", "entry_proxy_probe_cooldown_seconds", "entry_proxy_probe_auto_disable_failures":
			if f, ok := v.(float64); ok {
				val := int(f)
				switch k {
				case "request_timeout":
					if val <= 0 {
						val = 180
					} else if val > 1800 {
						val = 1800
					}
				case "max_request_mb":
					if val <= 0 {
						val = config.DefaultMaxRequestMB
					} else if val > config.MaxMaxRequestMB {
						val = config.MaxMaxRequestMB
					}
				case "race_timeout":
					if val < 0 {
						val = 0
					} else if val > 1800 {
						val = 1800
					}
				case "entry_proxy_probe_interval_seconds":
					if val <= 0 {
						val = config.DefaultEntryProxyProbeIntervalSeconds
					} else if val < config.MinEntryProxyProbeIntervalSeconds {
						val = config.MinEntryProxyProbeIntervalSeconds
					} else if val > config.MaxEntryProxyProbeSeconds {
						val = config.MaxEntryProxyProbeSeconds
					}
				case "entry_proxy_probe_cooldown_seconds":
					if val < 0 {
						val = 0
					} else if val > config.MaxEntryProxyProbeSeconds {
						val = config.MaxEntryProxyProbeSeconds
					}
				case "entry_proxy_probe_auto_disable_failures":
					if val <= 0 {
						val = config.DefaultEntryProxyAutoDisableFailures
					} else if val > config.MaxEntryProxyAutoDisableFailures {
						val = config.MaxEntryProxyAutoDisableFailures
					}
				}
				updates[k] = val
				continue
			}
		}
		updates[k] = v
	}
	if err := adm.writeSettings(updates); err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("写入配置失败 (failed to write config)"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminGetStats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, metricsBody())
}

func (adm *AdminHandler) adminResetStats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
