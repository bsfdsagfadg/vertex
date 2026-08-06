package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/entrynodes"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

// entryNodeNameFromParsed 从 ParsedNode 推导展示名：优先解析名，否则 scheme://host:port。
func entryNodeNameFromParsed(n *transport.ParsedNode, raw string) string {
	if n.Name != "" {
		return n.Name
	}
	if n.Server != "" {
		return fmt.Sprintf("%s://%s:%d", n.Type, n.Server, n.Port)
	}
	if s := transport.RedactURI(raw); s != raw {
		return s
	}
	return raw
}

// adminGetProxyNodes 返回所有前置代理节点及其健康度列表。
func (adm *AdminHandler) adminGetProxyNodes(w http.ResponseWriter, _ *http.Request) {
	nodes := entrynodes.LoadEntryNodes()
	var enabledCount, disabledCount int
	for _, n := range nodes {
		if n.Disabled {
			disabledCount++
		} else {
			enabledCount++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes":          nodes,
		"health":         entrynodes.LoadEntryHealth(),
		"total":          len(nodes),
		"enabled_count":  enabledCount,
		"disabled_count": disabledCount,
	})
}

// adminImportProxyNode 导入单个前置节点 URI 到 entry_nodes 表。
// 仅接受支持/可解析的代理协议；重复条目自动忽略。
func (adm *AdminHandler) adminImportProxyNode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RawURI string `json:"raw_uri"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	raw := strings.TrimSpace(body.RawURI)
	if raw == "" {
		writeJSON(w, http.StatusBadRequest, adminErr("URI 为空"))
		return
	}

	pn, perr := transport.GetOrParse(raw)
	if perr != nil || pn == nil || !pn.Supported {
		reason := "parse failed"
		if perr != nil {
			reason = "parse failed: " + perr.Error()
		} else if pn != nil {
			reason = "unsupported: " + pn.UnsupportedReason
		}
		writeJSON(w, http.StatusBadRequest, adminErr(reason))
		return
	}

	entrynodes.MergeEntryNodes([]entrynodes.Node{{
		RawURI: raw,
		Type:   pn.Type,
		Name:   entryNodeNameFromParsed(pn, raw),
	}})

	log.Printf("[Admin] [ImportProxyNode] 导入成功: %s (%s)", transport.RedactURI(raw), pn.Type)
	if dialer := adm.dialer(); dialer != nil {
		if err := dialer.SyncEntryPool(); err != nil {
			log.Printf("[Admin] [ImportProxyNode] 导入后同步前置代理池失败: %v", err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// adminToggleProxyNodes 批量启用/禁用前置节点，并触发 SyncEntryPool 增量同步。
func (adm *AdminHandler) adminToggleProxyNodes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URIs     []string `json:"uris"`
		Disabled bool     `json:"disabled"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	if len(body.URIs) == 0 {
		writeJSON(w, http.StatusBadRequest, adminErr("uris 不能为空"))
		return
	}
	entrynodes.BatchUpdateEntryNodesDisabled(body.URIs, body.Disabled)
	if dialer := adm.dialer(); dialer != nil {
		if err := dialer.SyncEntryPool(); err != nil {
			log.Printf("[Admin] [ToggleProxyNode] 同步前置代理池失败: %v", err)
		}
	}
	action := "禁用"
	if !body.Disabled {
		action = "启用"
	}
	log.Printf("[Admin] [ToggleProxyNode] 已%s %d 个前置节点，并同步轮询池", action, len(body.URIs))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// adminBatchDeleteProxyNodes 批量删除前置节点，并触发 SyncEntryPool 关闭对应实例。
func (adm *AdminHandler) adminBatchDeleteProxyNodes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URIs []string `json:"uris"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	if len(body.URIs) == 0 {
		writeJSON(w, http.StatusBadRequest, adminErr("uris 不能为空"))
		return
	}
	entrynodes.BatchDeleteEntryNodes(body.URIs)
	if dialer := adm.dialer(); dialer != nil {
		if err := dialer.SyncEntryPool(); err != nil {
			log.Printf("[Admin] [BatchDeleteProxyNode] 删除后同步前置代理池失败: %v", err)
		}
	}
	log.Printf("[Admin] [BatchDeleteProxyNode] 已删除 %d 个前置节点", len(body.URIs))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// adminDeleteDisabledProxyNodes 清空已禁用的前置节点。
func (adm *AdminHandler) adminDeleteDisabledProxyNodes(w http.ResponseWriter, _ *http.Request) {
	n := entrynodes.DeleteDisabledEntryNodes()
	if dialer := adm.dialer(); dialer != nil {
		if err := dialer.SyncEntryPool(); err != nil {
			log.Printf("[Admin] [DeleteDisabledProxyNode] 同步轮询池失败: %v", err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted_count": n})
}

// adminDedupProxyNodes 去重前置节点。
func (adm *AdminHandler) adminDedupProxyNodes(w http.ResponseWriter, _ *http.Request) {
	removed := entrynodes.DedupEntryNodes()
	if dialer := adm.dialer(); dialer != nil {
		if err := dialer.SyncEntryPool(); err != nil {
			log.Printf("[Admin] [DedupProxyNode] 去重后同步轮询池失败: %v", err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "removed_count": removed})
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

// adminTestProxyNode 测试指定前置节点的 204 连通性并把结果写入 entry_node_health。
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

	pn, perr := transport.GetOrParse(body.RawURI)
	if perr != nil || pn == nil || !pn.Supported {
		reason := "parse failed"
		if perr != nil {
			reason = "parse failed: " + perr.Error()
		} else if pn != nil {
			reason = "unsupported: " + pn.UnsupportedReason
		}
		log.Printf("[Admin] [TestProxyNode] 跳过前置代理测试 %s: %s", transport.RedactURI(body.RawURI), reason)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": false, "elapsed_ms": 0, "error": reason,
		})
		return
	}

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

	entrynodes.RecordEntryTest(body.RawURI, ok, elapsed, errStr)

	// 测试结果（尤其是网络类失败自动禁用）必须即时同步到轮询池，
	// 否则被禁用的节点仍会留在 Round-Robin 池中继续承接流量。
	if dialer := adm.dialer(); dialer != nil {
		if err := dialer.SyncEntryPool(); err != nil {
			log.Printf("[Admin] [TestProxyNode] 测试后同步前置代理池失败: %v", err)
		}
	}

	log.Printf("[Admin] [TestProxyNode] 前置代理测试 %s: ok=%v elapsed=%.0fms error=%q", transport.RedactURI(body.RawURI), ok, elapsed, errStr)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": ok, "elapsed_ms": elapsed, "error": errStr,
	})
}