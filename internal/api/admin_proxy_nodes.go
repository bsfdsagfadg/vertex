package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

func redactURI(raw string) string {
	before, after, found := strings.Cut(raw, "://")
	if !found {
		return raw
	}
	atIdx := strings.Index(after, "@")
	if atIdx == -1 {
		return raw
	}
	return before + "://" + after[atIdx+1:]
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
		log.Printf("[Admin] [ImportProxyNode] 导入失败: %v", err)
		writeJSON(w, http.StatusBadRequest, adminErr(err.Error()))
		return
	}
	log.Printf("[Admin] [ImportProxyNode] 导入成功: %s (%s)", candidate.Name, candidate.Type)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "candidate": candidate})
}

func (adm *AdminHandler) adminEnableProxyNode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RawURI string `json:"raw_uri"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	cfg := config.Load()
	found := false
	for _, c := range cfg.ProxyURLCandidates {
		if c.RawURI == body.RawURI {
			found = true
			break
		}
	}
	if !found {
		writeJSON(w, http.StatusBadRequest, adminErr("该 URI 不在候选列表中，请先导入"))
		return
	}

	dialer := adm.dialer()
	if dialer != nil {
		candidate, addr, err := dialer.ValidateEntryProxy(body.RawURI)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, adminErr("前置代理验证失败: "+err.Error()))
			return
		}
		oldProxy := adm.cfg.ProxyURL()
		if err := config.WriteSettings(map[string]any{"proxy_url": body.RawURI}); err != nil {
			candidate.Close()
			writeJSON(w, http.StatusInternalServerError, adminErr("启用前置代理失败: "+err.Error()))
			return
		}
		if err := dialer.AdoptEntryProxy(body.RawURI, candidate, addr); err != nil {
			_ = candidate.Close()
			_ = config.WriteSettings(map[string]any{"proxy_url": oldProxy})
			log.Printf("[Admin] [EnableProxyNode] 采纳前置代理失败，已回滚: %v", err)
			writeJSON(w, http.StatusInternalServerError, adminErr("采纳前置代理失败: "+err.Error()))
			return
		}
	} else {
		if err := config.WriteSettings(map[string]any{"proxy_url": body.RawURI}); err != nil {
			writeJSON(w, http.StatusInternalServerError, adminErr("启用前置代理失败: "+err.Error()))
			return
		}
	}

	log.Printf("[Admin] [EnableProxyNode] 启用前置代理: %s", redactURI(body.RawURI))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminDisableProxyNode(w http.ResponseWriter, r *http.Request) {
	if err := config.WriteSettings(map[string]any{"proxy_url": ""}); err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("取消前置代理失败: "+err.Error()))
		return
	}
	if dialer := adm.dialer(); dialer != nil {
		if err := dialer.SyncEntryProxy(""); err != nil {
			log.Printf("[Admin] [DisableProxyNode] 关闭前置代理失败: %v", err)
		}
	}
	log.Printf("[Admin] [DisableProxyNode] 已取消前置代理")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminDeleteProxyNode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RawURI string `json:"raw_uri"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	if err := config.RemoveProxyCandidate(body.RawURI); err != nil {
		writeJSON(w, http.StatusBadRequest, adminErr(err.Error()))
		return
	}
	log.Printf("[Admin] [DeleteProxyNode] 已删除候选: %s", redactURI(body.RawURI))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "empty result") ||
		strings.Contains(s, "connection was forcibly closed") ||
		strings.Contains(s, "connection reset by peer") ||
		strings.Contains(s, "i/o timeout")
}

func (adm *AdminHandler) testNodeOnce(ctx context.Context, timeout int, uri string) (ok bool, statusCode int, err error) {
	dialCtx, cleanup, err := adm.vc.Net().Dialer().TestEntryProxy(uri)
	if err != nil {
		return false, 0, err
	}
	defer cleanup()

	tr := &http.Transport{
		DialContext: dialCtx,
	}
	client := &http.Client{
		Transport: tr,
		Timeout:   time.Duration(timeout) * time.Second,
	}
	defer tr.CloseIdleConnections()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.gstatic.com/generate_204", nil)
	if err != nil {
		return false, 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, 0, err
	}
	resp.Body.Close()
	return resp.StatusCode == 204, resp.StatusCode, nil
}

func (adm *AdminHandler) adminTestProxyNode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RawURI         string  `json:"raw_uri"`
		TimeoutSeconds float64 `json:"timeout_seconds"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	if body.TimeoutSeconds <= 0 {
		body.TimeoutSeconds = 25
	}
	timeout := time.Duration(body.TimeoutSeconds * float64(time.Second))
	ctx, cancel := context.WithTimeout(r.Context(), 2*timeout+2*time.Second)
	defer cancel()

	start := time.Now()
	var testErr error
	for attempt := 0; attempt < 2; attempt++ {
		if ctx.Err() != nil {
			testErr = ctx.Err()
			break
		}
		ok, sc, err := adm.testNodeOnce(ctx, int(body.TimeoutSeconds), body.RawURI)
		if ok && sc == 204 {
			testErr = nil
			break
		}
		if attempt == 0 && isRetryable(err) {
			time.Sleep(1 * time.Second)
			continue
		}
		if err != nil {
			testErr = err
		} else {
			testErr = fmt.Errorf("预期 204, 收到 %d", sc)
		}
		break
	}
	elapsed := float64(time.Since(start).Milliseconds())

	errStr := ""
	ok := testErr == nil
	if testErr != nil {
		if ctx.Err() != nil {
			errStr = "timeout"
		} else {
			errStr = testErr.Error()
		}
	}

	_ = config.UpdateProxyCandidateTest(body.RawURI, ok, elapsed, errStr)

	log.Printf("[Admin] [TestProxyNode] 前置代理测试 %s: ok=%v elapsed=%.0fms error=%q", redactURI(body.RawURI), ok, elapsed, errStr)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         ok,
		"elapsed_ms": elapsed,
		"error":      errStr,
	})
}