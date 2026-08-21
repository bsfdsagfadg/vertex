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
	"github.com/bsfdsagfadg/vertex/internal/domain"
	"github.com/bsfdsagfadg/vertex/internal/netx"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/recaptcha"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

const (
	batchTestConcurrency     = 50
	singleNodeTestTimeoutSec = 15
)

func (adm *AdminHandler) adminGetNodes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var list []domain.Node
	if adm.nodeRepo != nil {
		dbList, err := adm.nodeRepo.GetAll(ctx)
		if err == nil {
			list = dbList
		}
	}
	if list == nil {
		list = domainNodesFromLegacy(nodes.LoadNodes())
	}

	var healthMap any
	if adm.healthRepo != nil {
		dbHealth, err := adm.healthRepo.GetAll(ctx)
		if err == nil {
			healthMap = dbHealth
		}
	}
	if healthMap == nil {
		healthMap = nodes.LoadHealth()
	}

	var enabledCount, disabledCount int
	for _, n := range list {
		if n.Disabled {
			disabledCount++
		} else {
			enabledCount++
		}
	}
	sp := nodes.GetStickyPool()
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes":                 list,
		"health":                healthMap,
		"total":                 len(list),
		"enabled_count":         enabledCount,
		"disabled_count":        disabledCount,
		"sticky_pool_available": sp.AvailableCount(),
		"sticky_pool_in_use":    sp.StaleCount(),
		"sticky_node_priority":  adm.cfg.StickyNodePriority(),
	})
}

func domainNodesFromLegacy(legacyList []nodes.Node) []domain.Node {
	out := make([]domain.Node, len(legacyList))
	for i, n := range legacyList {
		out[i] = domain.Node{
			Type:     n.Type,
			Name:     n.Name,
			RawURI:   n.RawURI,
			Disabled: n.Disabled,
		}
	}
	return out
}
func (adm *AdminHandler) adminGetTestProgress(w http.ResponseWriter, _ *http.Request) {
	if adm.taskManager != nil {
		if task, ok := adm.taskManager.GetActiveTaskByType("node_test_all"); ok {
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
	writeJSON(w, http.StatusOK, nodes.GetTestProgress())
}

func (adm *AdminHandler) adminFetchSub(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	log.Printf("[Admin] [FetchSub] 开始拉取订阅 URL: %s", body.URL)
	text, err := adm.fetchSubscriptionText(r.Context(), body.URL)
	if err != nil {
		log.Printf("[Admin] [FetchSub] 拉取失败: %v", err)
		writeJSON(w, http.StatusBadRequest, adminErr("拉取失败: "+err.Error()))
		return
	}

	newNodes := parseImportedNodes(text)
	nodes.MergeNodes(newNodes)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": len(newNodes)})
}

func (adm *AdminHandler) adminTestAll(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var list []domain.Node
	if adm.nodeRepo != nil {
		dbList, err := adm.nodeRepo.GetAll(ctx)
		if err == nil {
			list = dbList
		}
	}
	if list == nil {
		list = domainNodesFromLegacy(nodes.LoadNodes())
	}

	enabledNodes := make([]domain.Node, 0, len(list))
	for _, node := range list {
		if !node.Disabled {
			enabledNodes = append(enabledNodes, node)
		}
	}

	if adm.taskManager == nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("任务管理器未就绪"))
		return
	}

	if !nodes.StartTestProgress(len(enabledNodes)) {
		writeJSON(w, http.StatusConflict, adminErr("已有批量测试正在进行中，请先等待其结束或终止"))
		return
	}

	dynamicTimeout := batchTestTimeout(len(enabledNodes))
	taskCtx, cancel := context.WithTimeout(context.Background(), dynamicTimeout)

	_, err := adm.taskManager.StartTask(taskCtx, "node_test_all", len(enabledNodes), func(tc *TaskControl) error {
		defer func() {
			cancel()
			nodes.FinishTestProgress()
		}()

		log.Printf("[Admin] [TestAll] 加载待测节点数: %d/%d, 并发上限: %d, 总超时: %v", len(enabledNodes), len(list), batchTestConcurrency, dynamicTimeout)

		var wg sync.WaitGroup
		sem := make(chan struct{}, batchTestConcurrency)

		for _, n := range enabledNodes {
			wg.Add(1)
			go func(node domain.Node) {
				defer wg.Done()
				if tc.CheckControl() || nodes.CheckTestControl() {
					return
				}
				select {
				case sem <- struct{}{}:
				case <-tc.Context().Done():
					return
				}
				defer func() { <-sem }()
				if tc.CheckControl() || nodes.CheckTestControl() {
					return
				}

				start := time.Now()
				log.Printf("[Admin] [TestAll] 开始测试节点: %s (%s)", node.Name, node.Type)

				nodeCtx, nodeCancel := context.WithTimeout(tc.Context(), singleNodeTestTimeoutSec*time.Second)
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
				testErr, abort := resolveBatchNodeTest(tc.Context(), nodeCtx, testErr)
				if abort || tc.CheckControl() || nodes.CheckTestControl() {
					return
				}
				if testErr != nil {
					log.Printf("[Admin] [TestAll] 节点 %s 测试失败: %v, 耗时: %.0fms", node.Name, testErr, duration)
				} else {
					log.Printf("[Admin] [TestAll] 节点 %s 测试成功, recaptcha 耗时: %.0fms", node.Name, duration)
				}
				success := testErr == nil
				errStr := ""
				if testErr != nil {
					errStr = testErr.Error()
				}
				if adm.healthRepo != nil {
					adm.healthRepo.RecordTest(node.RawURI, success, duration, errStr)
				}
				nodes.RecordTest(node.RawURI, success, duration, errStr)
				if !success {
					if adm.nodeRepo != nil {
						_ = adm.nodeRepo.BatchSetDisabled(context.Background(), []string{node.RawURI}, true)
					}
					nodes.BatchUpdateNodesDisabled([]string{node.RawURI}, true)
				}
				tc.UpdateProgress(node.Name, success)
				nodes.UpdateTestProgress(node.Name, success)
			}(n)
		}
		wg.Wait()
		log.Printf("[Admin] [TestAll] 全局节点测试全部结束")
		return nil
	})
	if err != nil {
		cancel()
		nodes.FinishTestProgress()
		writeJSON(w, http.StatusConflict, adminErr("已有批量测试正在进行中，请先等待其结束或终止"))
		return
	}
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
	if adm.taskManager != nil {
		adm.taskManager.PauseActiveByType("node_test_all")
	}
	nodes.PauseTestProgress()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminTestResume(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		adm.adminMethodNotAllowed(w)
		return
	}
	if adm.taskManager != nil {
		adm.taskManager.ResumeActiveByType("node_test_all")
	}
	nodes.ResumeTestProgress()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminTestTerminate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		adm.adminMethodNotAllowed(w)
		return
	}
	if adm.taskManager != nil {
		adm.taskManager.TerminateActiveByType("node_test_all")
	}
	nodes.TerminateTestProgress()
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
		if adm.healthRepo != nil {
			adm.healthRepo.RecordTest(body.RawURI, ok, elapsed, errStr)
		}
		nodes.UpdateNodeTestResult(body.RawURI, ok, elapsed, errStr)
		disabled = !ok
		if !ok {
			if adm.nodeRepo != nil {
				_ = adm.nodeRepo.BatchSetDisabled(r.Context(), []string{body.RawURI}, true)
			}
			nodes.BatchUpdateNodesDisabled([]string{body.RawURI}, true)
		}
	}

	log.Printf("[Admin] [TestNode] 节点测试 %s: ok=%v elapsed=%.0fms error=%q disabled=%v", nodes.GetNodeName(body.RawURI), ok, elapsed, errStr, disabled)
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
	if adm.nodeRepo != nil {
		_ = adm.nodeRepo.SetDisabled(r.Context(), body.RawURI, false)
	}
	ok := nodes.EnableNode(body.RawURI)
	log.Printf("[Admin] [EnableNode] 启用节点 %s: %v", nodes.GetNodeName(body.RawURI), ok)
	writeJSON(w, http.StatusOK, map[string]any{"ok": ok})
}

func fetchRecaptchaTokenWithSess(ctx context.Context, sess *transport.Session) error {
	_, err := recaptcha.FetchRecaptchaTokenWithSession(ctx, sess)
	return err
}

func (adm *AdminHandler) adminDedupNodes(w http.ResponseWriter, r *http.Request) {
	var count int
	if adm.nodeRepo != nil {
		preview, err := adm.nodeRepo.Dedup(r.Context())
		if err == nil {
			count = preview.DuplicateCount
		}
	}
	memCount := nodes.DedupNodes()
	if count == 0 {
		count = memCount
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "removed_count": count})
}

func (adm *AdminHandler) adminPreviewDedupNodes(w http.ResponseWriter, r *http.Request) {
	if adm.nodeRepo != nil {
		preview, err := adm.nodeRepo.Dedup(r.Context())
		if err == nil {
			writeJSON(w, http.StatusOK, preview)
			return
		}
	}
	writeJSON(w, http.StatusOK, nodes.PreviewDedupNodes())
}

func (adm *AdminHandler) adminDeleteDisabledNodes(w http.ResponseWriter, r *http.Request) {
	var removed []string
	if adm.nodeRepo != nil {
		var err error
		removed, err = adm.nodeRepo.DeleteDisabled(r.Context())
		if err != nil {
			log.Printf("[Admin] [DeleteDisabled] 删除失败: %v", err)
		}
	}
	memRemovedCount := nodes.DeleteDisabled()
	for _, uri := range removed {
		transport.RemoveProxy(uri)
	}
	count := len(removed)
	if count == 0 {
		count = memRemovedCount
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
		_ = config.WriteSettings(map[string]any{"active_node_uri": "", "parallel_pool_enabled": true})
	} else {
		_ = config.WriteSettings(map[string]any{"active_node_uri": body.RawURI, "parallel_pool_enabled": false})
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
		nodes.SortNodesByLatencyDesc()
	} else {
		nodes.SortNodesByLatency()
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
	if adm.nodeRepo != nil {
		_ = adm.nodeRepo.DeleteByURI(r.Context(), body.RawURI)
	}
	nodes.DeleteNode(body.RawURI)
	transport.RemoveProxy(body.RawURI)
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
	if adm.nodeRepo != nil {
		_ = adm.nodeRepo.BatchSetDisabled(r.Context(), body.URIs, true)
	}
	nodes.BatchUpdateNodesDisabled(body.URIs, true)
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
	if adm.nodeRepo != nil {
		_ = adm.nodeRepo.BatchSetDisabled(r.Context(), body.URIs, false)
	}
	nodes.BatchUpdateNodesDisabled(body.URIs, false)
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
	if adm.nodeRepo != nil {
		_ = adm.nodeRepo.BatchDelete(r.Context(), body.URIs)
	}
	nodes.BatchDeleteNodes(body.URIs)
	for _, uri := range body.URIs {
		transport.RemoveProxy(uri)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
func (adm *AdminHandler) fetchSubscriptionText(ctx context.Context, rawURL string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", errors.New("subscription url is empty")
	}

	data, err := fetchSubscriptionDataDirect(ctx, rawURL)
	if err == nil {
		return strings.TrimSpace(string(data)), nil
	}

	proxyURI := subscriptionFallbackProxy(adm.cfg)
	if proxyURI == "" || adm.vc == nil || adm.vc.Net() == nil {
		return "", err
	}

	log.Printf("[Admin] [FetchSub] direct fetch failed, retry via proxy: %v", err)
	data, proxyErr := fetchSubscriptionDataViaProxy(ctx, adm.vc.Net(), rawURL, proxyURI)
	if proxyErr != nil {
		return "", fmt.Errorf("direct fetch failed: %v; proxy retry failed: %w", err, proxyErr)
	}

	log.Printf("[Admin] [FetchSub] proxy retry succeeded")
	return strings.TrimSpace(string(data)), nil
}

func subscriptionFallbackProxy(cfg config.ConfigProvider) string {
	if cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.ProxyURL())
}

func fetchSubscriptionDataDirect(ctx context.Context, rawURL string) ([]byte, error) {
	client := netx.NewHTTPClient(30 * time.Second)
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

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)
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

	sess, err := netClient.CreateSession(30, proxyURI, "admin-fetch-sub")
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)
	}
	defer sess.Close()

	header := transport.Header{
		"user-agent": {subscriptionFetchUserAgent},
		"accept":     {"*/*"},
	}
	statusCode, data, err := sess.DoAndRead(ctx, http.MethodGet, rawURL, header, nil)
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("status code %d", statusCode)
	}
	return data, nil
}
