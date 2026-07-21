package api

import (
	"log"
	"net/http"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

//nolint:gochecknoglobals // Constant-like map of allowed settings
var adminAllowedSettings = map[string]bool{
	"max_retries": true, "max_spill_mb": true,
	"max_request_mb": true, "max_n": true, "aggregate_stream": true,
	"drop_max_tokens": true, "proxy_url": true,
	"parallel_pool_enabled": true, "parallel_pool_size": true,
	"telemetry_enabled":           true,
	"parallel_pool_delay_dynamic": true,
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
	"default_image_size":          true,
}

func (adm *AdminHandler) adminGetSettings(w http.ResponseWriter, _ *http.Request) {
	telEnabled := true
	if adm.cfg.TelemetryEnabled() != nil {
		telEnabled = *adm.cfg.TelemetryEnabled()
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": map[string]any{
		"max_retries":       adm.cfg.MaxRetries(),
		"max_spill_mb":      adm.cfg.MaxSpillMB(),
		"max_request_mb":    adm.cfg.MaxRequestMB(),
		"max_n":             adm.cfg.MaxN(),
		"aggregate_stream":  adm.cfg.AggregateStream(),
		"drop_max_tokens":   adm.cfg.DropMaxTokens(),
		"telemetry_enabled": telEnabled,
		"proxy_url":         adm.cfg.ProxyURL(), "proxy_url_candidates": adm.cfg.ProxyURLCandidates(), "parallel_pool_enabled": adm.cfg.ParallelPoolEnabled(), "parallel_pool_size": adm.cfg.ParallelPoolSize(), "active_node_uri": adm.cfg.ActiveNodeURI(),
		"parallel_pool_delay_dynamic": adm.cfg.ParallelPoolDelayDynamic(),
		"sticky_node_priority":        adm.cfg.StickyNodePriority(),
		"parallel_pool_retry_enabled": adm.cfg.ParallelPoolRetryEnabled(),
		"background_image":            adm.cfg.BackgroundImage(),
		"font_size":                   adm.cfg.FontSize(),
		"font_color_type":             adm.cfg.FontColorType(),
		"font_color":                  adm.cfg.FontColor(),
		"custom_bg_presets":           adm.cfg.CustomBgPresets(),
		"debug_mode":                  adm.cfg.DebugMode(),
		"auto_refresh_logs":           adm.cfg.AutoRefreshLogs(),
		"default_image_size":          adm.cfg.DefaultImageSize(),
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
		case "max_retries", "max_spill_mb", "max_request_mb", "max_n", "parallel_pool_size":
			if f, ok := v.(float64); ok {
				updates[k] = int(f)
				continue
			}
		}
		updates[k] = v
	}

	// proxy_url 变更：
	//   - 空字符串（禁用）：跳过 ValidateEntryProxy，直接写配置并 SyncEntryProxy("")。
	//   - 非空（启用/切换）：先 ValidateEntryProxy 验证候选，验证通过后写配置，最后
	//     AdoptEntryProxy 采纳。任一失败必须显式返回错误，禁止已写非空 proxy_url 但无活跃回环。
	//
	// 采用两阶段写入避免回滚范围过大：
	//   阶段一：仅持久化 proxy_url（与运行时生命周期耦合）
	//   阶段二：AdoptEntryProxy 成功后再写入其余设置
	if newProxy, ok := updates["proxy_url"].(string); ok && newProxy != adm.cfg.ProxyURL() {
		dialer := adm.dialer()
		if newProxy == "" {
			if err := config.WriteSettings(updates); err != nil {
				writeJSON(w, http.StatusInternalServerError, adminErr("写入配置失败 (failed to write config)"))
				return
			}
			if dialer != nil {
				if err := dialer.SyncEntryProxy(""); err != nil {
					log.Printf("[Admin] [PutSettings] 关闭前置代理失败: %v", err)
				}
			}
		} else {
			// 分离 proxy_url 与其他字段，分两阶段写入
			otherUpdates := make(map[string]any, len(updates))
			for k, v := range updates {
				if k != "proxy_url" {
					otherUpdates[k] = v
				}
			}
			if dialer != nil {
				candidate, addr, err := dialer.ValidateEntryProxy(newProxy)
				if err != nil {
					writeJSON(w, http.StatusInternalServerError, adminErr("前置代理验证失败: "+err.Error()))
					return
				}
				oldProxy := adm.cfg.ProxyURL()
				// 阶段一：仅持久化 proxy_url
				if err := config.WriteSettings(map[string]any{"proxy_url": newProxy}); err != nil {
					candidate.Close()
					writeJSON(w, http.StatusInternalServerError, adminErr("写入配置失败 (failed to write config)"))
					return
				}
				if err := dialer.AdoptEntryProxy(newProxy, candidate, addr); err != nil {
					_ = candidate.Close()
					_ = config.WriteSettings(map[string]any{"proxy_url": oldProxy})
					log.Printf("[Admin] [PutSettings] 采纳前置代理失败，已回滚 proxy_url: %v", err)
					writeJSON(w, http.StatusInternalServerError, adminErr("采纳前置代理失败: "+err.Error()))
					return
				}
				// 阶段二：写入其余设置
				if len(otherUpdates) > 0 {
					if err := config.WriteSettings(otherUpdates); err != nil {
						log.Printf("[Admin] [PutSettings] 代理已采纳但其余配置写入失败: %v", err)
					}
				}
			} else {
				if err := config.WriteSettings(updates); err != nil {
					writeJSON(w, http.StatusInternalServerError, adminErr("写入配置失败 (failed to write config)"))
					return
				}
			}
		}
	} else {
		if err := config.WriteSettings(updates); err != nil {
			writeJSON(w, http.StatusInternalServerError, adminErr("写入配置失败 (failed to write config)"))
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminGetStats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, metricsBody())
}

func (adm *AdminHandler) adminResetStats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
