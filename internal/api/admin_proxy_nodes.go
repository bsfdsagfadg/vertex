package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/transport"
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
	oldProxy := adm.cfg.ProxyURL()
	if err := config.WriteSettings(map[string]any{"proxy_url": body.RawURI}); err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("启用入口代理失败: "+err.Error()))
		return
	}
	if oldProxy != "" && oldProxy != body.RawURI {
		transport.RemoveProxy(oldProxy)
	}
	log.Printf("[Admin] [EnableProxyNode] 已启用入口代理: %s", redactProxyURI(body.RawURI))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminDisableProxyNode(w http.ResponseWriter, _ *http.Request) {
	oldProxy := adm.cfg.ProxyURL()
	if err := config.WriteSettings(map[string]any{"proxy_url": ""}); err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("停用入口代理失败: "+err.Error()))
		return
	}
	if oldProxy != "" {
		transport.RemoveProxy(oldProxy)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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

	start := time.Now()
	session, err := adm.vc.Net().CreateSessionWithoutEntryProxy(int(body.TimeoutSeconds), body.RawURI, "admin-test-entry-proxy")
	if err == nil {
		defer session.Close()
		var status int
		status, _, err = session.DoAndRead(ctx, http.MethodGet, "https://www.gstatic.com/generate_204", nil, nil)
		if err == nil && status != http.StatusNoContent {
			err = fmt.Errorf("预期 HTTP 204，收到 %d", status)
		}
	}
	elapsed := float64(time.Since(start).Milliseconds())
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
