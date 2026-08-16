package api

import (
	"net/http"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/importer"
)

//nolint:gochecknoglobals // Constant-like map of allowed settings
var adminAllowedSettings = map[string]bool{
	"max_retries": true, "max_spill_mb": true,
	"max_request_mb": true, "max_n": true, "aggregate_stream": true,
	"drop_max_tokens":       true,
	"parallel_pool_enabled": true, "parallel_pool_size": true,
	"telemetry_enabled":             true,
	"parallel_pool_delay_dynamic":   true,
	"active_node_uri":               true,
	"background_image":              true,
	"font_size":                     true,
	"font_color_type":               true,
	"font_color":                    true,
	"custom_bg_presets":             true,
	"debug_mode":                    true,
	"trailing_model_fix_enabled":    true,
	"trailing_fix_models":           true,
	"auto_refresh_logs":             true,
	"default_image_size":            true,
	"default_thinking_level":        true,
	"default_response_modalities":   true,
	"stream_idle_timeout_seconds":   true,
	"request_timeout_seconds":       true,
	"recaptcha_try_entry_or_direct": true,
}

func (adm *AdminHandler) adminGetSettings(w http.ResponseWriter, _ *http.Request) {
	telEnabled := true
	if adm.cfg.TelemetryEnabled() != nil {
		telEnabled = *adm.cfg.TelemetryEnabled()
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": map[string]any{
		"max_retries":           adm.cfg.MaxRetries(),
		"max_spill_mb":          adm.cfg.MaxSpillMB(),
		"max_request_mb":        adm.cfg.MaxRequestMB(),
		"max_n":                 adm.cfg.MaxN(),
		"aggregate_stream":      adm.cfg.AggregateStream(),
		"drop_max_tokens":       adm.cfg.DropMaxTokens(),
		"telemetry_enabled":     telEnabled,
		"parallel_pool_enabled": adm.cfg.ParallelPoolEnabled(), "parallel_pool_size": adm.cfg.ParallelPoolSize(), "active_node_uri": adm.cfg.ActiveNodeURI(),
		"parallel_pool_delay_dynamic":   adm.cfg.ParallelPoolDelayDynamic(),
		"background_image":              adm.cfg.BackgroundImage(),
		"font_size":                     adm.cfg.FontSize(),
		"font_color_type":               adm.cfg.FontColorType(),
		"font_color":                    adm.cfg.FontColor(),
		"custom_bg_presets":             adm.cfg.CustomBgPresets(),
		"debug_mode":                    adm.cfg.DebugMode(),
		"trailing_model_fix_enabled":    adm.cfg.TrailingModelFixEnabled(),
		"trailing_fix_models":           adm.cfg.TrailingFixModels(),
		"auto_refresh_logs":             adm.cfg.AutoRefreshLogs(),
		"default_image_size":            adm.cfg.DefaultImageSize(),
		"default_thinking_level":        adm.cfg.DefaultThinkingLevel(),
		"default_response_modalities":   adm.cfg.DefaultResponseModalities(),
		"stream_idle_timeout_seconds":   adm.cfg.StreamIdleTimeoutSeconds(),
		"request_timeout_seconds":       adm.cfg.RequestTimeoutSeconds(),
		"recaptcha_try_entry_or_direct": adm.cfg.RecaptchaTryEntryOrDirect(),
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

	for k, v := range body.Settings {
		if !adminAllowedSettings[k] {
			continue
		}
		switch k {
		case "max_retries", "max_spill_mb", "max_request_mb", "max_n", "parallel_pool_size", "stream_idle_timeout_seconds", "request_timeout_seconds":
			if f, ok := v.(float64); ok {
				updates[k] = int(f)
				continue
			}
		case "trailing_fix_models":
			if list, ok := v.([]any); ok {
				updates[k] = normalizeTrailingFixModelsInput(list)
			}
			continue
		}
		updates[k] = v
	}

	if err := config.WriteSettings(updates); err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("写入配置失败 (failed to write config)"))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// normalizeTrailingFixModelsInput 归一化管理后台提交的 trailing_fix_models：
// 逐项转字符串、TrimSpace、去空、去重，顺序保持稳定。
func normalizeTrailingFixModelsInput(list []any) []string {
	out := make([]string, 0, len(list))
	for _, item := range list {
		s := strings.TrimSpace(importer.ValueToString(item))
		if s == "" {
			continue
		}
		dup := false
		for _, e := range out {
			if e == s {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		out = append(out, s)
	}
	return out
}

func (adm *AdminHandler) adminGetStats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, metricsBody())
}

func (adm *AdminHandler) adminResetStats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
