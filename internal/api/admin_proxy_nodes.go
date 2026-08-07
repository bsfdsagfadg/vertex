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
	entryProxyProbeURL      = "https://www.google.com/recaptcha/enterprise.js"
	entryProxyProbeInterval = 5 * time.Minute
	entryProxyProbeTimeout  = 20 * time.Second
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

func probeEntryProxyCandidate(ctx context.Context, netClient *transport.NetworkClient, rawURI string, timeoutSeconds int) (float64, error) {
	start := time.Now()
	session, err := netClient.CreateSessionWithoutEntryProxy(timeoutSeconds, rawURI, "probe-entry-proxy")
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
// Manual Disabled state is never changed; successful probes only clear transient cooldown.
func StartEntryProxyProbeLoop(netClient *transport.NetworkClient) func() {
	if netClient == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(entryProxyProbeInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				probeAllEnabledProxyCandidates(ctx, netClient)
			}
		}
	}()
	return cancel
}

func probeAllEnabledProxyCandidates(ctx context.Context, netClient *transport.NetworkClient) {
	var wg sync.WaitGroup
	for _, candidate := range config.ListProxyCandidates() {
		if candidate.Disabled || strings.TrimSpace(candidate.RawURI) == "" {
			continue
		}
		wg.Add(1)
		go func(rawURI string) {
			defer wg.Done()
			probeCtx, cancel := context.WithTimeout(ctx, entryProxyProbeTimeout)
			defer cancel()
			elapsed, err := probeEntryProxyCandidate(probeCtx, netClient, rawURI, int(entryProxyProbeTimeout.Seconds()))
			errText := ""
			if err != nil {
				errText = err.Error()
				log.Printf("[EntryProxy] 候选 %s 周期拨测失败: %v", redactProxyURI(rawURI), err)
			}
			if updateErr := config.UpdateProxyCandidateTest(rawURI, err == nil, elapsed, errText); updateErr != nil {
				log.Printf("[EntryProxy] 更新候选 %s 拨测状态失败: %v", redactProxyURI(rawURI), updateErr)
			}
		}(candidate.RawURI)
	}
	wg.Wait()
}
