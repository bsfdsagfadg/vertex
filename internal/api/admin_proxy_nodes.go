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
	"github.com/bsfdsagfadg/vertex/internal/domain"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
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

// No package-level test state variables — all testing operations are managed by TaskManager

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
	candidate, err := createEntryProxyCandidate(body.RawURI)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, adminErr(err.Error()))
		return
	}
	if adm.entryRepo != nil {
		if err := adm.entryRepo.Add(r.Context(), candidate); err != nil {
			writeJSON(w, http.StatusInternalServerError, adminErr(err.Error()))
			return
		}
	}
	_, _ = config.AddProxyCandidate(body.RawURI)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "candidate": candidate})
}

func (adm *AdminHandler) adminEnableProxyNode(w http.ResponseWriter, r *http.Request) {
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
	candidate, err := createEntryProxyCandidate(body.RawURI)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, adminErr(err.Error()))
		return
	}
	candidate.Disabled = false
	if adm.entryRepo != nil {
		_ = adm.entryRepo.Add(r.Context(), candidate)
	}
	_ = config.SetProxyCandidateEnabled(body.RawURI, true)
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
	candidate, err := createEntryProxyCandidate(body.RawURI)
	if err == nil {
		candidate.Disabled = true
		if adm.entryRepo != nil {
			_ = adm.entryRepo.Add(r.Context(), candidate)
		}
	}
	_ = config.SetProxyCandidateEnabled(body.RawURI, false)
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
	var all []domain.EntryProxyCandidate
	if adm.entryRepo != nil {
		dbList, err := adm.entryRepo.GetAll(r.Context())
		if err == nil {
			all = dbList
		}
	}
	if all == nil {
		all = domainCandidatesFromLegacy(config.ListProxyCandidates())
	}
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

func createEntryProxyCandidate(rawURI string) (domain.EntryProxyCandidate, error) {
	rawURI = strings.TrimSpace(rawURI)
	normalized, err := config.NormalizeProxyURI(rawURI)
	if err != nil {
		return domain.EntryProxyCandidate{}, err
	}
	name := nodes.GetNodeName(rawURI)
	nodeType := "unknown"
	if idx := strings.Index(rawURI, "://"); idx > 0 {
		nodeType = rawURI[:idx]
	}
	return domain.EntryProxyCandidate{
		RawURI:        rawURI,
		NormalizedURI: normalized,
		Name:          name,
		Type:          nodeType,
		Disabled:      false,
	}, nil
}
func domainCandidatesFromLegacy(legacyList []config.ProxyCandidate) []domain.EntryProxyCandidate {
	out := make([]domain.EntryProxyCandidate, len(legacyList))
	for i, c := range legacyList {
		normalized, _ := config.NormalizeProxyURI(c.RawURI)
		out[i] = domain.EntryProxyCandidate{
			RawURI:              c.RawURI,
			NormalizedURI:       normalized,
			Name:                c.Name,
			Type:                c.Type,
			Disabled:            c.Disabled,
			CooldownUntil:       c.CooldownUntil,
			LastTestOK:          c.LastTestOK,
			LastTestMs:          c.LastTestMs,
			LastTestAt:          c.LastTestAt,
			LastTestError:       c.LastTestError,
			ConsecutiveFailures: c.ConsecutiveFailures,
		}
	}
	return out
}

func (adm *AdminHandler) adminImportProxyNodesBatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URIs []string `json:"uris"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	added, existing, invalid := make([]domain.EntryProxyCandidate, 0), make([]string, 0), make([]string, 0)
	for _, rawURI := range body.URIs {
		if err := transport.ValidateProxyURI(rawURI); err != nil {
			invalid = append(invalid, strings.TrimSpace(rawURI))
			continue
		}
		candidate, err := createEntryProxyCandidate(rawURI)
		if err != nil {
			invalid = append(invalid, strings.TrimSpace(rawURI))
			continue
		}
		if adm.entryRepo != nil {
			if err := adm.entryRepo.Add(r.Context(), candidate); err != nil {
				invalid = append(invalid, strings.TrimSpace(rawURI))
				continue
			}
		}
		if _, err := config.AddProxyCandidate(rawURI); err == nil {
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
		candidate, err := createEntryProxyCandidate(rawURI)
		if err == nil {
			candidate.Disabled = !enabled
			if adm.entryRepo != nil {
				_ = adm.entryRepo.Add(r.Context(), candidate)
			}
		}
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
		rawURI = strings.TrimSpace(rawURI)
		normalized, err := config.NormalizeProxyURI(rawURI)
		if err == nil && adm.entryRepo != nil {
			_ = adm.entryRepo.Remove(r.Context(), normalized)
		}
		if _, err := config.RemoveProxyCandidate(rawURI); err != nil {
			invalid = append(invalid, rawURI)
			continue
		}
		transport.RemoveProxy(rawURI)
		deleted = append(deleted, rawURI)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": len(invalid) == 0, "deleted": deleted, "invalid": invalid})
}

func (adm *AdminHandler) adminDeleteDisabledProxyNodes(w http.ResponseWriter, r *http.Request) {
	var deleted []string
	if adm.entryRepo != nil {
		removed, err := adm.entryRepo.RemoveDisabled(r.Context())
		if err == nil {
			deleted = removed
		}
	}
	cfgDeleted, err := config.RemoveDisabledProxyCandidates()
	if err == nil && len(deleted) == 0 {
		deleted = cfgDeleted
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
	normalized, err := config.NormalizeProxyURI(body.RawURI)
	if err == nil && adm.entryRepo != nil {
		_ = adm.entryRepo.Remove(r.Context(), normalized)
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
	normalized, normErr := config.NormalizeProxyURI(body.RawURI)
	if normErr == nil && adm.entryRepo != nil {
		_, _ = adm.entryRepo.UpdateTestResult(r.Context(), normalized, err == nil, elapsed, errText, 0, false, false, 0)
	}
	if updateErr := config.UpdateProxyCandidateTest(body.RawURI, err == nil, elapsed, errText); updateErr != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr(updateErr.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": err == nil, "elapsed_ms": elapsed, "error": errText})
}

func (adm *AdminHandler) adminGetProxyTestProgress(w http.ResponseWriter, _ *http.Request) {
	if adm.taskManager != nil {
		if task, ok := adm.taskManager.GetActiveTaskByType("proxy_batch_test"); ok {
			writeJSON(w, http.StatusOK, map[string]any{
				"running":      task.State == TaskStateRunning || task.State == TaskStatePaused,
				"paused":       task.State == TaskStatePaused,
				"terminated":   task.State == TaskStateTerminated,
				"total":        task.Progress.Total,
				"done":         task.Progress.Done,
				"ok_count":     task.Progress.OkCount,
				"fail_count":   task.Progress.FailCount,
				"current_node": task.Progress.CurrentNode,
			})
			return
		}
	}
	writeJSON(w, http.StatusOK, entryProxyTestProgress{})
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

	var candidateList []domain.EntryProxyCandidate
	if adm.entryRepo != nil {
		dbList, err := adm.entryRepo.GetAll(r.Context())
		if err == nil {
			candidateList = dbList
		}
	}
	if candidateList == nil {
		candidateList = domainCandidatesFromLegacy(config.ListProxyCandidates())
	}

	known := make(map[string]domain.EntryProxyCandidate)
	for _, candidate := range candidateList {
		known[candidate.RawURI] = candidate
	}
	selected := make([]domain.EntryProxyCandidate, 0, len(body.URIs))
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

	if adm.taskManager == nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("任务管理器未就绪"))
		return
	}

	perItemTimeout := time.Duration(body.TimeoutSeconds * float64(time.Second))
	rounds := (len(selected) + entryProxyTestConcurrency - 1) / entryProxyTestConcurrency
	totalTimeout := time.Duration(rounds*2)*perItemTimeout + 2*time.Minute
	if totalTimeout < 5*time.Minute {
		totalTimeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), totalTimeout)

	_, err := adm.taskManager.StartTask(ctx, "proxy_batch_test", len(selected), func(tc *TaskControl) error {
		defer cancel()

		sem := make(chan struct{}, entryProxyTestConcurrency)
		var wg sync.WaitGroup
		for _, candidate := range selected {
			wg.Add(1)
			go func(c domain.EntryProxyCandidate) {
				defer wg.Done()
				if tc.CheckControl() {
					return
				}
				select {
				case sem <- struct{}{}:
				case <-tc.Context().Done():
					return
				}
				defer func() { <-sem }()
				if tc.CheckControl() {
					return
				}

				tc.SetCurrentNode(c.Name)

				probeCtx, probeCancel := context.WithTimeout(tc.Context(), perItemTimeout)
				elapsed, probeErr := probeEntryProxyCandidate(probeCtx, adm.vc.Net(), c.RawURI, int(perItemTimeout.Seconds()))
				probeCtxErr := probeCtx.Err()
				probeCancel()
				if tc.Context().Err() != nil {
					return
				}
				errText := ""
				if probeErr != nil {
					errText = probeErr.Error()
					if probeCtxErr != nil {
						errText = "timeout"
					}
				}
				normalized, normErr := config.NormalizeProxyURI(c.RawURI)
				if normErr == nil && adm.entryRepo != nil {
					_, _ = adm.entryRepo.UpdateTestResult(context.Background(), normalized, probeErr == nil, elapsed, errText, 0, false, false, 0)
				}
				_ = config.UpdateProxyCandidateTest(c.RawURI, probeErr == nil, elapsed, errText)

				success := probeErr == nil
				tc.UpdateProgress(c.Name, success)
			}(candidate)
		}
		wg.Wait()
		return nil
	})
	if err != nil {
		cancel()
		writeJSON(w, http.StatusConflict, adminErr("已有入口代理批量测试正在进行中"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "total": len(selected)})
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
