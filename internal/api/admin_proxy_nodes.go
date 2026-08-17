package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

const (
	entryProxyProbeURL          = "https://www.google.com/recaptcha/enterprise.js"
	entryProxyProbeTimeout      = 20 * time.Second
	entryProxyProbePollInterval = time.Second
	entryProxyTestConcurrency   = 50
)

type entryProxyTestProgress struct {
	Running     bool   `json:"running"`
	Total       int    `json:"total"`
	Done        int    `json:"done"`
	OKCount     int    `json:"ok_count"`
	FailCount   int    `json:"fail_count"`
	CurrentNode string `json:"current_node"`
}

type entryProxyProbeSummary struct {
	Total        int
	Success      int
	Failed       int
	Cooling      int
	AutoDisabled int
}

type entryProxyProbeSchedule struct {
	interval time.Duration
	next     time.Time
}

func (s *entryProxyProbeSchedule) due(now time.Time, enabled bool, interval time.Duration) bool {
	if !enabled {
		s.interval = 0
		s.next = time.Time{}
		return false
	}
	if s.next.IsZero() || s.interval != interval {
		s.interval = interval
		s.next = now.Add(interval)
		return false
	}
	return !now.Before(s.next)
}

func (s *entryProxyProbeSchedule) completed(now time.Time) {
	s.next = now.Add(s.interval)
}

var (
	entryProxyTestMu         sync.Mutex             //nolint:gochecknoglobals
	entryProxyTestState      entryProxyTestProgress //nolint:gochecknoglobals
	entryProxyTestGeneration uint64                 //nolint:gochecknoglobals
)

func redactProxyURI(rawURI string) string {
	scheme, remainder, ok := strings.Cut(rawURI, "://")
	if !ok {
		return rawURI
	}
	if index := strings.Index(remainder, "@"); index >= 0 {
		return scheme + "://" + remainder[index+1:]
	}
	return rawURI
}

func (adm *AdminHandler) adminImportProxyNode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RawURI string `json:"raw_uri"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	if err := transport.ValidateProxyURI(body.RawURI); err != nil {
		writeJSON(w, http.StatusBadRequest, adminErr("代理构造失败: "+err.Error()))
		return
	}
	candidate, err := config.AddProxyCandidate(body.RawURI)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, adminErr(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "candidate": candidate})
}

func (adm *AdminHandler) adminEnableProxyNode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RawURI string `json:"raw_uri"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	if !config.HasProxyCandidate(body.RawURI) {
		writeJSON(w, http.StatusBadRequest, adminErr("该 URI 不在候选列表中"))
		return
	}
	if err := transport.ValidateProxyURI(body.RawURI); err != nil {
		writeJSON(w, http.StatusBadRequest, adminErr("代理构造失败: "+err.Error()))
		return
	}
	if err := config.SetProxyCandidateEnabled(body.RawURI, true); err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("启用入口代理失败: "+err.Error()))
		return
	}
	transport.RemoveProxy(body.RawURI)
	log.Printf("[Admin] [EnableProxyNode] 已启用入口代理: %s", redactProxyURI(body.RawURI))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminDisableProxyNode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RawURI string `json:"raw_uri"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.RawURI) == "" {
		body.RawURI = strings.TrimSpace(adm.cfg.ProxyURL())
	}
	if body.RawURI == "" {
		writeJSON(w, http.StatusBadRequest, adminErr("缺少入口代理 URI"))
		return
	}
	if err := config.SetProxyCandidateEnabled(body.RawURI, false); err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("停用入口代理失败: "+err.Error()))
		return
	}
	transport.RemoveProxy(body.RawURI)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminListProxyNodes(w http.ResponseWriter, r *http.Request) {
	page, pageSize := 1, 10
	if value := r.URL.Query().Get("page"); value != "" {
		if _, err := fmt.Sscanf(value, "%d", &page); err != nil || page < 1 {
			page = 1
		}
	}
	if value := r.URL.Query().Get("page_size"); value != "" {
		if _, err := fmt.Sscanf(value, "%d", &pageSize); err != nil || pageSize < 1 {
			pageSize = 10
		}
	}
	if pageSize > 100 {
		pageSize = 100
	}
	all := config.ListProxyCandidates()
	start := (page - 1) * pageSize
	if start > len(all) {
		start = len(all)
	}
	end := start + pageSize
	if end > len(all) {
		end = len(all)
	}
	items := all[start:end]
	writeJSON(w, http.StatusOK, map[string]any{
		"candidates": items, "page": page, "page_size": pageSize, "total": len(all),
		"total_pages": (len(all) + pageSize - 1) / pageSize,
	})
}

func (adm *AdminHandler) adminImportProxyNodesBatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URIs []string `json:"uris"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	added, existing, invalid := make([]config.ProxyCandidate, 0), make([]string, 0), make([]string, 0)
	for _, rawURI := range body.URIs {
		if err := transport.ValidateProxyURI(rawURI); err != nil {
			invalid = append(invalid, strings.TrimSpace(rawURI))
			continue
		}
		candidate, err := config.AddProxyCandidate(rawURI)
		if err == nil {
			added = append(added, candidate)
		} else if config.HasProxyCandidate(rawURI) {
			existing = append(existing, strings.TrimSpace(rawURI))
		} else {
			invalid = append(invalid, strings.TrimSpace(rawURI))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "added": added, "already_present": existing, "invalid": invalid})
}

func (adm *AdminHandler) adminSetProxyNodesEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	var body struct {
		URIs   []string `json:"uris"`
		RawURI string   `json:"raw_uri"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	if body.RawURI != "" {
		body.URIs = append(body.URIs, body.RawURI)
	}
	updated, invalid := make([]string, 0), make([]string, 0)
	for _, rawURI := range body.URIs {
		if err := config.SetProxyCandidateEnabled(rawURI, enabled); err != nil {
			invalid = append(invalid, strings.TrimSpace(rawURI))
			continue
		}
		updated = append(updated, strings.TrimSpace(rawURI))
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": len(invalid) == 0, "updated": updated, "invalid": invalid})
}

func (adm *AdminHandler) adminDeleteProxyNodesBatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URIs []string `json:"uris"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	deleted, invalid := make([]string, 0), make([]string, 0)
	for _, rawURI := range body.URIs {
		if _, err := config.RemoveProxyCandidate(rawURI); err != nil {
			invalid = append(invalid, strings.TrimSpace(rawURI))
			continue
		}
		transport.RemoveProxy(rawURI)
		deleted = append(deleted, strings.TrimSpace(rawURI))
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": len(invalid) == 0, "deleted": deleted, "invalid": invalid})
}

func (adm *AdminHandler) adminDeleteDisabledProxyNodes(w http.ResponseWriter, _ *http.Request) {
	deleted, err := config.RemoveDisabledProxyCandidates()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr(err.Error()))
		return
	}
	for _, rawURI := range deleted {
		transport.RemoveProxy(rawURI)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": deleted, "deleted_count": len(deleted)})
}

func (adm *AdminHandler) adminDeleteProxyNode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RawURI string `json:"raw_uri"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	wasActive, err := config.RemoveProxyCandidate(body.RawURI)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, adminErr(err.Error()))
		return
	}
	transport.RemoveProxy(body.RawURI)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "was_active": wasActive})
}

func (adm *AdminHandler) adminTestProxyNode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RawURI         string  `json:"raw_uri"`
		TimeoutSeconds float64 `json:"timeout_seconds"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	if !config.HasProxyCandidate(body.RawURI) {
		writeJSON(w, http.StatusBadRequest, adminErr("该 URI 不在候选列表中"))
		return
	}
	if body.TimeoutSeconds <= 0 || body.TimeoutSeconds > 60 {
		body.TimeoutSeconds = 25
	}
	timeout := time.Duration(body.TimeoutSeconds * float64(time.Second))
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	elapsed, err := probeEntryProxyCandidate(ctx, adm.vc.Net(), body.RawURI, int(body.TimeoutSeconds))
	errText := ""
	if err != nil {
		errText = err.Error()
		if ctx.Err() != nil {
			errText = "timeout"
		}
	}
	if updateErr := config.UpdateProxyCandidateTest(body.RawURI, err == nil, elapsed, errText); updateErr != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr(updateErr.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": err == nil, "elapsed_ms": elapsed, "error": errText})
}

func (adm *AdminHandler) adminGetProxyTestProgress(w http.ResponseWriter, _ *http.Request) {
	entryProxyTestMu.Lock()
	state := entryProxyTestState
	entryProxyTestMu.Unlock()
	writeJSON(w, http.StatusOK, state)
}

func (adm *AdminHandler) adminBatchTestProxyNodes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URIs           []string `json:"uris"`
		TimeoutSeconds float64  `json:"timeout_seconds"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	if body.TimeoutSeconds <= 0 || body.TimeoutSeconds > 60 {
		body.TimeoutSeconds = 25
	}

	known := make(map[string]config.ProxyCandidate)
	for _, candidate := range config.ListProxyCandidates() {
		known[candidate.RawURI] = candidate
	}
	selected := make([]config.ProxyCandidate, 0, len(body.URIs))
	seen := make(map[string]struct{}, len(body.URIs))
	for _, rawURI := range body.URIs {
		rawURI = strings.TrimSpace(rawURI)
		candidate, ok := known[rawURI]
		if !ok {
			continue
		}
		if _, duplicate := seen[rawURI]; duplicate {
			continue
		}
		seen[rawURI] = struct{}{}
		selected = append(selected, candidate)
	}
	if len(selected) == 0 {
		writeJSON(w, http.StatusBadRequest, adminErr("没有有效的入口代理可测试"))
		return
	}

	entryProxyTestMu.Lock()
	if entryProxyTestState.Running {
		entryProxyTestMu.Unlock()
		writeJSON(w, http.StatusConflict, adminErr("已有入口代理批量测试正在进行中"))
		return
	}
	entryProxyTestGeneration++
	generation := entryProxyTestGeneration
	entryProxyTestState = entryProxyTestProgress{Running: true, Total: len(selected)} //nolint:exhaustruct
	entryProxyTestMu.Unlock()

	perItemTimeout := time.Duration(body.TimeoutSeconds * float64(time.Second))
	rounds := (len(selected) + entryProxyTestConcurrency - 1) / entryProxyTestConcurrency
	totalTimeout := time.Duration(rounds*2)*perItemTimeout + 2*time.Minute
	if totalTimeout < 5*time.Minute {
		totalTimeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), totalTimeout)
	go adm.runProxyBatchTest(ctx, cancel, generation, selected, perItemTimeout)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "total": len(selected)})
}

func (adm *AdminHandler) runProxyBatchTest(
	ctx context.Context,
	cancel context.CancelFunc,
	generation uint64,
	candidates []config.ProxyCandidate,
	perItemTimeout time.Duration,
) {
	defer cancel()
	defer func() {
		entryProxyTestMu.Lock()
		if entryProxyTestGeneration == generation {
			entryProxyTestState.Running = false
			entryProxyTestState.CurrentNode = ""
		}
		entryProxyTestMu.Unlock()
	}()

	sem := make(chan struct{}, entryProxyTestConcurrency)
	var wg sync.WaitGroup
	for _, candidate := range candidates {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			entryProxyTestMu.Lock()
			if entryProxyTestGeneration == generation {
				entryProxyTestState.CurrentNode = candidate.Name
			}
			entryProxyTestMu.Unlock()

			probeCtx, probeCancel := context.WithTimeout(ctx, perItemTimeout)
			elapsed, err := probeEntryProxyCandidate(probeCtx, adm.vc.Net(), candidate.RawURI, int(perItemTimeout.Seconds()))
			probeCtxErr := probeCtx.Err()
			probeCancel()
			if ctx.Err() != nil {
				return
			}
			errText := ""
			if err != nil {
				errText = err.Error()
				if probeCtxErr != nil {
					errText = "timeout"
				}
			}
			if updateErr := config.UpdateProxyCandidateTest(candidate.RawURI, err == nil, elapsed, errText); updateErr != nil {
				err = updateErr
			}

			entryProxyTestMu.Lock()
			if entryProxyTestGeneration == generation {
				entryProxyTestState.Done++
				if err == nil {
					entryProxyTestState.OKCount++
				} else {
					entryProxyTestState.FailCount++
				}
			}
			entryProxyTestMu.Unlock()
		}()
	}
	wg.Wait()
}

func probeEntryProxyCandidate(ctx context.Context, netClient *transport.NetworkClient, rawURI string, timeoutSeconds int) (float64, error) {
	start := time.Now()
	reqID := transport.RequestIDFromContext(ctx)
	if reqID == "" {
		reqID = "entry-probe-loop"
	}
	session, err := netClient.CreateSessionWithoutEntryProxy(timeoutSeconds, rawURI, reqID)
	if err != nil {
		return float64(time.Since(start).Milliseconds()), err
	}
	defer session.Close()
	status, _, err := session.DoAndRead(ctx, http.MethodGet, entryProxyProbeURL, nil, nil)
	if err == nil && status != http.StatusOK {
		err = fmt.Errorf("预期 HTTP 200，收到 %d", status)
	}
	return float64(time.Since(start).Milliseconds()), err
}

// StartEntryProxyProbeLoop periodically probes enabled entries and returns a stop function.
// The interval and automatic-disable policy are read from config without requiring a restart.
func StartEntryProxyProbeLoop(netClient *transport.NetworkClient) func() {
	if netClient == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(entryProxyProbePollInterval)
		defer ticker.Stop()
		var schedule entryProxyProbeSchedule
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cfg := config.GetProvider()
				interval := time.Duration(cfg.EntryProxyProbeIntervalSeconds()) * time.Second
				if !schedule.due(time.Now(), cfg.EntryProxyProbeEnabled(), interval) {
					continue
				}
				probeAllEnabledProxyCandidates(ctx, netClient, cfg)
				schedule.completed(time.Now())
			}
		}
	}()
	return cancel
}

func probeAllEnabledProxyCandidates(ctx context.Context, netClient *transport.NetworkClient, cfg config.ConfigProvider) entryProxyProbeSummary {
	return runEntryProxyProbeRound(ctx, cfg, func(probeCtx context.Context, rawURI string) (float64, error) {
		return probeEntryProxyCandidate(probeCtx, netClient, rawURI, int(entryProxyProbeTimeout.Seconds()))
	})
}

func runEntryProxyProbeRound(
	ctx context.Context,
	cfg config.ConfigProvider,
	probe func(context.Context, string) (float64, error),
) entryProxyProbeSummary {
	candidates := make([]config.ProxyCandidate, 0)
	for _, candidate := range config.ListProxyCandidates() {
		if candidate.Disabled || strings.TrimSpace(candidate.RawURI) == "" {
			continue
		}
		candidates = append(candidates, candidate)
	}

	log.Printf("[EntryProxy] 自动拨测开始: %d 个节点测试", len(candidates))
	debugMode := cfg.DebugMode()
	cooldownSeconds := cfg.EntryProxyProbeCooldownSeconds()
	autoDisable := cfg.EntryProxyProbeAutoDisableEnabled()
	failureLimit := cfg.EntryProxyProbeAutoDisableFailures()
	var resultMu sync.Mutex
	summary := entryProxyProbeSummary{Total: len(candidates)}

	var wg sync.WaitGroup
	for _, candidate := range candidates {
		wg.Add(1)
		go func(rawURI string) {
			defer wg.Done()
			probeCtx, cancel := context.WithTimeout(ctx, entryProxyProbeTimeout)
			defer cancel()
			elapsed, err := probe(probeCtx, rawURI)
			errText := ""
			if err != nil {
				errText = err.Error()
			}
			autoDisabled, updateErr := config.UpdateProxyCandidateProbeResult(
				rawURI, err == nil, elapsed, errText, cooldownSeconds, autoDisable, failureLimit,
			)
			if updateErr != nil {
				log.Printf("[EntryProxy] 更新候选 %s 拨测状态失败: %v", redactProxyURI(rawURI), updateErr)
			}
			if err != nil {
				if autoDisabled {
					log.Printf(
						"[EntryProxy] 候选 %s 周期拨测失败: %v（连续失败 %d 次，已自动禁用）",
						redactProxyURI(rawURI), err, failureLimit,
					)
				} else {
					log.Printf("[EntryProxy] 候选 %s 周期拨测失败: %v", redactProxyURI(rawURI), err)
				}
			} else if debugMode {
				log.Printf("[EntryProxy] 候选 %s 周期拨测成功: %.0fms", redactProxyURI(rawURI), elapsed)
			}

			resultMu.Lock()
			if err == nil {
				summary.Success++
			} else {
				summary.Failed++
				if autoDisabled {
					summary.AutoDisabled++
				} else if updateErr == nil && cooldownSeconds > 0 {
					summary.Cooling++
				}
			}
			resultMu.Unlock()
		}(candidate.RawURI)
	}
	wg.Wait()
	log.Printf(
		"[EntryProxy] 自动拨测结束: %d 个节点自动测试完毕，%d 个成功，%d 个失败，%d 个冷却，%d 个自动禁用",
		summary.Total, summary.Success, summary.Failed, summary.Cooling, summary.AutoDisabled,
	)
	return summary
}
