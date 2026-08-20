package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/recaptcha"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

const (
	batchTestConcurrency     = 50
	singleNodeTestTimeoutSec = 15
)

func (adm *AdminHandler) adminGetNodes(w http.ResponseWriter, r *http.Request) {
	list, health, err := adm.listRequestNodes(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("加载节点失败: "+err.Error()))
		return
	}
	var enabledCount, disabledCount int
	for _, n := range list {
		if n.Disabled {
			disabledCount++
		} else {
			enabledCount++
		}
	}
	var sp *nodes.StickyNodePool
	if adm.nodePool != nil {
		sp = adm.nodePool.StickyPool()
	} else {
		sp = nodes.GetStickyPool()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes":                 list,
		"health":                health,
		"total":                 len(list),
		"enabled_count":         enabledCount,
		"disabled_count":        disabledCount,
		"sticky_pool_available": sp.AvailableCount(),
		"sticky_pool_in_use":    sp.StaleCount(),
		"sticky_node_priority":  adm.cfg.StickyNodePriority(),
	})
}

func (adm *AdminHandler) adminGetTestProgress(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, adm.requestNodeTestProgress())
}

func (adm *AdminHandler) adminFetchSub(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	if err := validateSubscriptionURL(body.URL); err != nil {
		writeJSON(w, http.StatusBadRequest, adminErr(err.Error()))
		return
	}
	log.Printf("[Admin] [FetchSub] 开始拉取订阅 URL: %s", redactSubscriptionURL(body.URL))
	text, err := adm.fetchSubscriptionText(r.Context(), body.URL)
	if err != nil {
		log.Printf("[Admin] [FetchSub] 拉取失败: %v", err)
		writeJSON(w, http.StatusBadRequest, adminErr("拉取失败: "+err.Error()))
		return
	}

	newNodes := parseImportedNodes(text)
	if err := adm.importRequestNodes(r.Context(), newNodes, false); err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("保存节点失败: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": len(newNodes)})
}

func (adm *AdminHandler) adminTestAll(w http.ResponseWriter, r *http.Request) {
	list, _, err := adm.listRequestNodes(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("加载节点失败: "+err.Error()))
		return
	}
	enabledNodes := make([]nodes.Node, 0, len(list))
	for _, node := range list {
		if !node.Disabled {
			enabledNodes = append(enabledNodes, node)
		}
	}
	if !adm.startRequestNodeTest(len(enabledNodes)) {
		writeJSON(w, http.StatusConflict, adminErr("已有批量测试正在进行中，请先等待其结束或终止"))
		return
	}

	dynamicTimeout := batchTestTimeout(len(enabledNodes))
	ctx, cancel := context.WithTimeout(context.Background(), dynamicTimeout)
	adm.nodeTestMu.Lock()
	adm.nodeTestGeneration++
	generation := adm.nodeTestGeneration
	adm.nodeTestCancel = cancel
	adm.nodeTestMu.Unlock()

	log.Printf("[Admin] [TestAll] 开始触发全局并发测速（基于 recaptchaToken 耗时）")
	go func() {
		defer func() {
			cancel()
			adm.nodeTestMu.Lock()
			if adm.nodeTestGeneration == generation {
				adm.nodeTestCancel = nil
			}
			adm.nodeTestMu.Unlock()
		}()
		log.Printf("[Admin] [TestAll] 加载待测节点数: %d/%d, 并发上限: %d, 总超时: %v", len(enabledNodes), len(list), batchTestConcurrency, dynamicTimeout)

		var wg sync.WaitGroup
		sem := make(chan struct{}, batchTestConcurrency)

		for _, n := range enabledNodes {
			wg.Add(1)
			go func(node nodes.Node) {
				defer wg.Done()
				if adm.checkRequestNodeTestControl() {
					return
				}
				select {
				case sem <- struct{}{}:
				case <-ctx.Done():
					return
				}
				defer func() { <-sem }()
				if adm.checkRequestNodeTestControl() {
					return
				}

				start := time.Now()
				log.Printf("[Admin] [TestAll] 开始测试节点: %s (%s)", node.Name, node.Type)

				nodeCtx, nodeCancel := context.WithTimeout(ctx, singleNodeTestTimeoutSec*time.Second)
				defer nodeCancel()
				sess, err := adm.vc.Net().CreateSession(singleNodeTestTimeoutSec, node.RawURI, "admin-test-all")
				var testErr error
				if err == nil {
					defer sess.Close()
					testErr = fetchRecaptchaTokenWithSess(nodeCtx, sess)
				} else {
					testErr = err
				}

				duration := float64(time.Since(start).Milliseconds())
				testErr, abort := resolveBatchNodeTest(ctx, nodeCtx, testErr)
				if abort || adm.checkRequestNodeTestControl() {
					return
				}
				if testErr != nil {
					log.Printf("[Admin] [TestAll] 节点 %s 测试失败: %v, 耗时: %.0fms", node.Name, testErr, duration)
				} else {
					log.Printf("[Admin] [TestAll] 节点 %s 测试成功, recaptcha 耗时: %.0fms", node.Name, duration)
				}
				success := testErr == nil
				adm.recordRequestNodeTest(node.RawURI, success, duration, errToStr(testErr))
				if !success {
					_, _ = adm.setRequestNodesDisabled(context.Background(), []string{node.RawURI}, true)
				}
				adm.updateRequestNodeTest(node.Name, success)
			}(n)
		}
		wg.Wait()
		adm.finishRequestNodeTest()
		log.Printf("[Admin] [TestAll] 全局节点测试全部结束")
	}()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// resolveBatchNodeTest 区分整个批量任务取消与单节点超时：父任务取消时停止记账，
// 单节点 deadline 则是该节点的一次普通失败，必须继续记录结果并推进进度。
func resolveBatchNodeTest(parentCtx, nodeCtx context.Context, testErr error) (resolved error, abort bool) {
	if err := parentCtx.Err(); err != nil {
		return err, true
	}
	if testErr == nil {
		if err := nodeCtx.Err(); err != nil {
			testErr = err
		}
	}
	return testErr, false
}

func batchTestTimeout(total int) time.Duration {
	rounds := (total + batchTestConcurrency - 1) / batchTestConcurrency
	timeout := time.Duration(rounds*2)*singleNodeTestTimeoutSec*time.Second + 2*time.Minute
	if timeout < 5*time.Minute {
		return 5 * time.Minute
	}
	return timeout
}

func (adm *AdminHandler) adminTestPause(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		adm.adminMethodNotAllowed(w)
		return
	}
	adm.pauseRequestNodeTest()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminTestResume(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		adm.adminMethodNotAllowed(w)
		return
	}
	adm.resumeRequestNodeTest()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminTestTerminate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		adm.adminMethodNotAllowed(w)
		return
	}
	adm.terminateRequestNodeTest()
	adm.nodeTestMu.Lock()
	if adm.nodeTestCancel != nil {
		adm.nodeTestCancel()
	}
	adm.nodeTestMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminTestNode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RawURI         string  `json:"raw_uri"`
		AutoDisable    bool    `json:"auto_disable"`
		TimeoutSeconds float64 `json:"timeout_seconds"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	if body.TimeoutSeconds <= 0 {
		body.TimeoutSeconds = 25
	}
	timeout := time.Duration(body.TimeoutSeconds * float64(time.Second))
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	start := time.Now()
	sess, err := adm.vc.Net().CreateSession(15, body.RawURI, "admin-test-node")
	var testErr error
	if err == nil {
		testErr = fetchRecaptchaTokenWithSess(ctx, sess)
		sess.Close()
	} else {
		testErr = err
	}
	elapsed := float64(time.Since(start).Milliseconds())

	errStr := ""
	ok := testErr == nil
	if testErr != nil {
		if ctx.Err() != nil || errors.Is(testErr, context.DeadlineExceeded) {
			errStr = "timeout"
		} else {
			errStr = testErr.Error()
		}
	}

	disabled := false
	if body.AutoDisable {
		adm.recordRequestNodeTest(body.RawURI, ok, elapsed, errStr)
		disabled = !ok
		if !ok {
			_, _ = adm.setRequestNodesDisabled(r.Context(), []string{body.RawURI}, true)
		}
	}

	log.Printf("[Admin] [TestNode] 节点测试 %s: ok=%v elapsed=%.0fms error=%q disabled=%v", adm.requestNodeName(body.RawURI), ok, elapsed, errStr, disabled)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         ok,
		"elapsed_ms": elapsed,
		"error":      errStr,
		"disabled":   disabled,
	})
}

func (adm *AdminHandler) adminEnableNode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RawURI string `json:"raw_uri"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	count, err := adm.setRequestNodesDisabled(r.Context(), []string{body.RawURI}, false)
	ok := err == nil && count > 0
	log.Printf("[Admin] [EnableNode] 启用节点 %s: %v", adm.requestNodeName(body.RawURI), ok)
	writeJSON(w, http.StatusOK, map[string]any{"ok": ok})
}

func fetchRecaptchaTokenWithSess(ctx context.Context, sess *transport.Session) error {
	_, err := recaptcha.FetchRecaptchaTokenWithSession(ctx, sess)
	return err
}

func (adm *AdminHandler) adminDedupNodes(w http.ResponseWriter, r *http.Request) {
	if adm.nodePool == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "removed_count": nodes.DedupNodes()})
		return
	}
	removed, err := adm.nodePool.Dedup(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("节点去重失败: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "removed_count": removed})
}

func (adm *AdminHandler) adminPreviewDedupNodes(w http.ResponseWriter, r *http.Request) {
	if adm.nodePool == nil {
		writeJSON(w, http.StatusOK, nodes.PreviewDedupNodes())
		return
	}
	preview, err := adm.nodePool.PreviewDedup(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("预览去重失败: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (adm *AdminHandler) adminDeleteDisabledNodes(w http.ResponseWriter, r *http.Request) {
	if adm.nodePool == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted_count": nodes.DeleteDisabled()})
		return
	}
	count, err := adm.nodePool.DeleteDisabled(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("删除禁用节点失败: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted_count": count})
}

func (adm *AdminHandler) adminUseNode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RawURI string `json:"raw_uri"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	if body.RawURI == "" {
		if err := adm.writeSettings(map[string]any{"active_node_uri": "", "parallel_pool_enabled": true}); err != nil {
			writeJSON(w, http.StatusInternalServerError, adminErr("写入节点模式失败: "+err.Error()))
			return
		}
	} else {
		if err := adm.writeSettings(map[string]any{"active_node_uri": body.RawURI, "parallel_pool_enabled": false}); err != nil {
			writeJSON(w, http.StatusInternalServerError, adminErr("写入节点模式失败: "+err.Error()))
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminSortNodesByLatency(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Desc bool `json:"desc"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	if body.Desc {
		if adm.nodePool != nil {
			adm.nodePool.SetSort(true)
		} else {
			nodes.SortNodesByLatencyDesc()
		}
	} else {
		if adm.nodePool != nil {
			adm.nodePool.SetSort(false)
		} else {
			nodes.SortNodesByLatency()
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminDeleteNode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RawURI string `json:"raw_uri"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	if _, err := adm.deleteRequestNodes(r.Context(), []string{body.RawURI}); err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("删除节点失败: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminBatchDisableNodes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URIs []string `json:"uris"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	log.Printf("[Admin] [BatchDisable] 批量禁用 %d 个节点", len(body.URIs))
	if _, err := adm.setRequestNodesDisabled(r.Context(), body.URIs, true); err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("禁用节点失败: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminBatchEnableNodes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URIs []string `json:"uris"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	log.Printf("[Admin] [BatchEnable] 批量启用 %d 个节点", len(body.URIs))
	if _, err := adm.setRequestNodesDisabled(r.Context(), body.URIs, false); err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("启用节点失败: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminBatchDeleteNodes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URIs []string `json:"uris"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	log.Printf("[Admin] [BatchDelete] 批量删除 %d 个节点", len(body.URIs))
	if _, err := adm.deleteRequestNodes(r.Context(), body.URIs); err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("删除节点失败: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) fetchSubscriptionText(ctx context.Context, rawURL string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", errors.New("subscription url is empty")
	}
	if err := validateSubscriptionURL(rawURL); err != nil {
		return "", err
	}
	// Validate DNS destinations even when the request is sent through a
	// configured proxy. The proxy must not become an SSRF bypass for private
	// or metadata hostnames.
	if err := validateSubscriptionURLResolved(ctx, rawURL); err != nil {
		return "", err
	}

	proxyURI, direct, err := adm.planSubscriptionRoute(ctx, adm.cfg)
	if err != nil {
		return "", err
	}
	var data []byte
	if direct {
		data, err = fetchSubscriptionDataDirect(ctx, rawURL)
	} else {
		if adm.vc == nil || adm.vc.Net() == nil {
			return "", errors.New("network client unavailable for global proxy subscription route")
		}
		data, err = fetchSubscriptionDataViaProxy(ctx, adm.vc.Net(), rawURL, proxyURI)
		if err == nil {
			_ = adm.markGlobalProxySuccess(proxyURI)
		}
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func (adm *AdminHandler) planSubscriptionRoute(ctx context.Context, cfg config.ConfigProvider) (proxyURI string, direct bool, err error) {
	if cfg == nil || !cfg.GlobalProxyEnabled() {
		return "", true, nil
	}
	proxyURI, err = adm.selectGlobalProxy(ctx, cfg)
	if err != nil {
		return "", false, err
	}
	if proxyURI = strings.TrimSpace(proxyURI); proxyURI != "" {
		return proxyURI, false, nil
	}
	if cfg.AllowDirectWithoutGlobalProxy() {
		return "", true, nil
	}
	return "", false, errors.New("no_global_proxy_route")
}

// planSubscriptionRoute makes direct access an explicit policy decision. When
// GlobalProxy is enabled, subscription traffic never probes the destination
// directly before trying the selected first hop.
func planSubscriptionRoute(cfg config.ConfigProvider) (proxyURI string, direct bool, err error) {
	if cfg == nil {
		return "", true, nil
	}
	if !cfg.GlobalProxyEnabled() {
		return "", true, nil
	}
	if proxyURI = strings.TrimSpace(config.SelectEntryProxy(cfg)); proxyURI != "" {
		return proxyURI, false, nil
	}
	if cfg.AllowDirectWithoutGlobalProxy() {
		return "", true, nil
	}
	return "", false, errors.New("no_global_proxy_route")
}

func fetchSubscriptionDataDirect(ctx context.Context, rawURL string) ([]byte, error) {
	if err := validateSubscriptionURLResolved(ctx, rawURL); err != nil {
		return nil, err
	}
	client := newSubscriptionHTTPClient(30 * time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)
	}
	req.Header.Set("User-Agent", subscriptionFetchUserAgent)
	req.Header.Set("Accept", "*/*")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("nil response received")
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSubscriptionResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)
	}
	if len(data) > maxSubscriptionResponseBytes {
		return nil, fmt.Errorf("subscription response exceeds %d bytes", maxSubscriptionResponseBytes)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status code %d", resp.StatusCode)
	}
	return data, nil
}

func fetchSubscriptionDataViaProxy(ctx context.Context, netClient *transport.NetworkClient, rawURL string, proxyURI string) ([]byte, error) {
	if netClient == nil {
		return nil, errors.New("network client unavailable")
	}
	if err := validateSubscriptionURLResolved(ctx, rawURL); err != nil {
		return nil, err
	}

	sess, err := netClient.CreateSessionWithoutRedirects(30, proxyURI, "admin-fetch-sub")
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)
	}
	defer sess.Close()

	header := transport.Header{
		"user-agent": {subscriptionFetchUserAgent},
		"accept":     {"*/*"},
	}
	statusCode, data, err := sess.DoAndReadLimit(ctx, http.MethodGet, rawURL, header, nil, maxSubscriptionResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("status code %d", statusCode)
	}
	return data, nil
}
