package api

import (
	"net/http"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

//nolint:gochecknoglobals // Constant-like map of allowed settings
var adminAllowedSettings = map[string]bool{
	"max_retries": true, "max_spill_mb": true,
	"max_request_mb": true, "max_n": true, "aggregate_stream": true,
	"fake_stream_enabled": true,
	"drop_max_tokens":     true, "proxy_url": true,
	"request_timeout":          true,
	"race_timeout":             true,
	"model_turn_guard_enabled": true,
	"parallel_pool_enabled":    true, "parallel_pool_size": true,
	"telemetry_enabled":           true,
	"parallel_pool_delay_dynamic": true,
	"parallel_pool_delay_ms":      true,
	"active_node_uri":             true,
	"sticky_node_priority":        true,
	"parallel_pool_retry_enabled": true,
	"background_image":            true,
	"font_size":                   true,
	"font_color_type":             true,
	"font_color":                  true,
	"custom_bg_presets":           true,
	"debug_mode":                  true,
	"auto_refresh_logs":           true,
}

func (adm *AdminHandler) adminGetSettings(w http.ResponseWriter, _ *http.Request) {
	telEnabled := true
	if adm.cfg.TelemetryEnabled() != nil {
		telEnabled = *adm.cfg.TelemetryEnabled()
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": map[string]any{
		"max_retries":              adm.cfg.MaxRetries(),
		"max_spill_mb":             adm.cfg.MaxSpillMB(),
		"max_request_mb":           adm.cfg.MaxRequestMB(),
		"max_n":                    adm.cfg.MaxN(),
		"aggregate_stream":         adm.cfg.AggregateStream(),
		"fake_stream_enabled":      adm.cfg.FakeStreamEnabled(),
		"drop_max_tokens":          adm.cfg.DropMaxTokens(),
		"telemetry_enabled":        telEnabled,
		"request_timeout":          adm.cfg.RequestTimeout(),
		"race_timeout":             adm.cfg.RaceTimeout(),
		"model_turn_guard_enabled": adm.cfg.ModelTurnGuardEnabled(),
		"proxy_url":                adm.cfg.ProxyURL(), "parallel_pool_enabled": adm.cfg.ParallelPoolEnabled(), "parallel_pool_size": adm.cfg.ParallelPoolSize(), "active_node_uri": adm.cfg.ActiveNodeURI(),
		"parallel_pool_delay_dynamic": adm.cfg.ParallelPoolDelayDynamic(),
		"parallel_pool_delay_ms":      adm.cfg.ParallelPoolDelayMs(),
		"sticky_node_priority":        adm.cfg.StickyNodePriority(),
		"parallel_pool_retry_enabled": adm.cfg.ParallelPoolRetryEnabled(),
		"background_image":            adm.cfg.BackgroundImage(),
		"font_size":                   adm.cfg.FontSize(),
		"font_color_type":             adm.cfg.FontColorType(),
		"font_color":                  adm.cfg.FontColor(),
		"custom_bg_presets":           adm.cfg.CustomBgPresets(),
		"debug_mode":                  adm.cfg.DebugMode(),
		"auto_refresh_logs":           adm.cfg.AutoRefreshLogs(),
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
		case "max_retries", "max_spill_mb", "max_request_mb", "max_n", "parallel_pool_size", "parallel_pool_delay_ms", "request_timeout", "race_timeout":
			if f, ok := v.(float64); ok {
				val := int(f)
				switch k {
				case "request_timeout":
					if val <= 0 {
						val = 180
					} else if val > 1800 {
						val = 1800
					}
				case "race_timeout":
					if val < 0 {
						val = 0
					} else if val > 1800 {
						val = 1800
					}
				}
				updates[k] = val
				continue
			}
		}
		updates[k] = v
	}
	if err := config.WriteSettings(updates); err != nil {
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
