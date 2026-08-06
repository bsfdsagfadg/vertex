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
	"github.com/bsfdsagfadg/vertex/internal/netx"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/recaptcha"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

const (
	batchTestConcurrency     = 50
	singleNodeTestTimeoutSec = 15
)

var (
	//nolint:gochecknoglobals // 当前唯一批量测速任务的取消函数和代次
	testAllCancel context.CancelFunc
	//nolint:gochecknoglobals // guards testAllCancel/testAllGeneration
	testAllMu sync.Mutex
	//nolint:gochecknoglobals // prevents an old task from clearing a newer cancel function
	testAllGeneration uint64
)

func (adm *AdminHandler) adminGetNodes(w http.ResponseWriter, _ *http.Request) {
	list := nodes.LoadNodes()
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
		"health":                nodes.LoadHealth(),
		"total":                 len(list),
		"enabled_count":         enabledCount,
		"disabled_count":        disabledCount,
		"sticky_pool_available": sp.AvailableCount(),
		"sticky_pool_in_use":    sp.StaleCount(),
		"sticky_node_priority":  adm.cfg.StickyNodePriority(),
	})
}

func (adm *AdminHandler) adminGetTestProgress(w http.ResponseWriter, _ *http.Request) {
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

	report := parseImportedNodesReport(text)
	nodes.MergeNodes(report.Imported)
	log.Printf("[Admin] [FetchSub] 导入完成: %d 个节点（unsupported: %d, failed: %d）",
		len(report.Imported), len(report.Unsupported), len(report.Failed))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "count": len(report.Imported),
		"unsupported":    report.Unsupported,
		"failed":         report.Failed,
		"protocol_stats": report.ProtocolStats,
	})
}

func (adm *AdminHandler) adminTestAll(w http.ResponseWriter, _ *http.Request) {
	list := nodes.LoadNodes()
	enabledNodes := make([]nodes.Node, 0, len(list))
	for _, node := range list {
		if !node.Disabled {
			enabledNodes = append(enabledNodes, node)
		}
	}
	if !nodes.StartTestProgress(len(enabledNodes)) {
		writeJSON(w, http.StatusConflict, adminErr("已有批量测试正在进行中，请先等待其结束或终止"))
		return
	}

	dynamicTimeout := batchTestTimeout(len(enabledNodes))
	ctx, cancel := context.WithTimeout(context.Background(), dynamicTimeout)
	testAllMu.Lock()
	testAllGeneration++
	generation := testAllGeneration
	testAllCancel = cancel
	testAllMu.Unlock()

	log.Printf("[Admin] [TestAll] 开始触发全局并发测速（基于 recaptchaToken 耗时）")
	go func() {
		defer func() {
			cancel()
			testAllMu.Lock()
			if testAllGeneration == generation {
				testAllCancel = nil
			}
			testAllMu.Unlock()
		}()
		log.Printf("[Admin] [TestAll] 加载待测节点数: %d/%d, 并发上限: %d, 总超时: %v", len(enabledNodes), len(list), batchTestConcurrency, dynamicTimeout)

		var wg sync.WaitGroup
		sem := make(chan struct{}, batchTestConcurrency)

		for _, n := range enabledNodes {
			wg.Add(1)
			go func(node nodes.Node) {
				defer wg.Done()
				if nodes.CheckTestControl() {
					return
				}
				// capability 预检：进并发槽之前判明当前构建能否装载该节点；
				// 不支持的协议不发起网络、不占并发槽，直接记失败并禁用。
				if reason := transport.ValidateNodeURI(node.RawURI); reason != "" {
					log.Printf("[Admin] [TestAll] 节点 %s 能力预检不通过（不占并发槽）: %s", node.Name, reason)
					nodes.RecordTest(node.RawURI, false, 0, reason)
					nodes.BatchUpdateNodesDisabled([]string{node.RawURI}, true)
					nodes.UpdateTestProgress(node.Name, false)
					return
				}
				select {
				case sem <- struct{}{}:
				case <-ctx.Done():
					return
				}
				defer func() { <-sem }()
				if nodes.CheckTestControl() {
					return
				}

				start := time.Now()
				log.Printf("[Admin] [TestAll] 开始测试节点: %s (%s)", node.Name, node.Type)

				// 内层 goroutine + select 兜底：黑洞节点（连接永不返回）在
				// singleNodeTestTimeoutSec+2s 内强制记普通失败，防止占满 50 并发槽挂死整批。
				// 泄漏的内层 goroutine 每节点最多 1 个，其内部 nodeCtx 到点会自动取消，量级可接受。
				done := make(chan error, 1)
				go func() {
					nodeCtx, nodeCancel := context.WithTimeout(ctx, singleNodeTestTimeoutSec*time.Second)
					defer nodeCancel()
					sess, sessErr := adm.vc.Net().CreateSession(singleNodeTestTimeoutSec, node.RawURI, "admin-test-all")
					var innerErr error
					if sessErr == nil {
						defer sess.Close()
						innerErr = fetchRecaptchaTokenWithSess(nodeCtx, sess)
					} else {
						innerErr = sessErr
					}
					done <- innerErr
				}()
				var testErr error
				select {
				case testErr = <-done:
				case <-time.After(singleNodeTestTimeoutSec*time.Second + 2*time.Second):
					testErr = context.DeadlineExceeded
					log.Printf("[Admin] [TestAll] 节点 %s 黑洞兜底超时标记（%ds）", node.Name, singleNodeTestTimeoutSec+2)
				}

				duration := float64(time.Since(start).Milliseconds())
				testErr, abort := resolveBatchNodeTest(ctx, ctx, testErr)
				if abort || nodes.CheckTestControl() {
					return
				}
				if testErr != nil {
					log.Printf("[Admin] [TestAll] 节点 %s 测试失败: %v, 耗时: %.0fms", node.Name, testErr, duration)
				} else {
					log.Printf("[Admin] [TestAll] 节点 %s 测试成功, recaptcha 耗时: %.0fms", node.Name, duration)
				}
				success := testErr == nil
				nodes.RecordTest(node.RawURI, success, duration, errToStr(testErr))
				if !success {
					nodes.BatchUpdateNodesDisabled([]string{node.RawURI}, true)
				}
				nodes.UpdateTestProgress(node.Name, success)
			}(n)
		}
		wg.Wait()
		nodes.FinishTestProgress()
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
	nodes.PauseTestProgress()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminTestResume(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		adm.adminMethodNotAllowed(w)
		return
	}
	nodes.ResumeTestProgress()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminTestTerminate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		adm.adminMethodNotAllowed(w)
		return
	}
	nodes.TerminateTestProgress()
	testAllMu.Lock()
	if testAllCancel != nil {
		testAllCancel()
	}
	testAllMu.Unlock()
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

	// capability 预检：当前构建无法装载的协议直接失败，不发起网络。
	if reason := transport.ValidateNodeURI(body.RawURI); reason != "" {
		log.Printf("[Admin] [TestNode] 节点 %s 能力预检不通过: %s", nodes.GetNodeName(body.RawURI), reason)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": false, "elapsed_ms": 0, "error": reason, "disabled": false,
		})
		return
	}

	start := time.Now()
	// 内层 goroutine + select 兜底：黑洞节点在 body.TimeoutSeconds+2s 内强制记失败。
	done := make(chan error, 1)
	go func() {
		sess, sessErr := adm.vc.Net().CreateSession(15, body.RawURI, "admin-test-node")
		var innerErr error
		if sessErr == nil {
			defer sess.Close()
			innerErr = fetchRecaptchaTokenWithSess(ctx, sess)
		} else {
			innerErr = sessErr
		}
		done <- innerErr
	}()
	var testErr error
	select {
	case testErr = <-done:
	case <-time.After(timeout + 2*time.Second):
		testErr = context.DeadlineExceeded
		log.Printf("[Admin] [TestNode] 节点 %s 黑洞兜底超时标记", nodes.GetNodeName(body.RawURI))
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
		nodes.UpdateNodeTestResult(body.RawURI, ok, elapsed, errStr)
		disabled = !ok
		if !ok {
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
	ok := nodes.EnableNode(body.RawURI)
	log.Printf("[Admin] [EnableNode] 启用节点 %s: %v", nodes.GetNodeName(body.RawURI), ok)
	writeJSON(w, http.StatusOK, map[string]any{"ok": ok})
}

func fetchRecaptchaTokenWithSess(ctx context.Context, sess *transport.Session) error {
	_, err := recaptcha.FetchRecaptchaTokenWithSession(ctx, sess)
	return err
}

func (adm *AdminHandler) adminDedupNodes(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "removed_count": nodes.DedupNodes()})
}

func (adm *AdminHandler) adminDeleteDisabledNodes(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted_count": nodes.DeleteDisabled()})
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
	nodes.DeleteNode(body.RawURI)
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
	nodes.BatchDeleteNodes(body.URIs)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) fetchSubscriptionText(ctx context.Context, rawURL string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", errors.New("subscription url is empty")
	}

	// 代理优先 → 直连兜底（目标 4）：不论是否配置 proxy_url 都先走 CreateSession(30, proxyURI, …)；
	// proxyURI=="" 时 P1 改造后 CreateSession 自动挂候选入口（入口优先），非空时维持双跳语义
	// （候选入口 → proxy_url）。仅当网络客户端不可用时才跳过代理直接直连。
	var proxyErr error
	if adm.vc != nil && adm.vc.Net() != nil {
		proxyURI := subscriptionFallbackProxy(adm.cfg)
		var data []byte
		data, proxyErr = fetchSubscriptionDataViaProxy(ctx, adm.vc.Net(), rawURL, proxyURI)
		if proxyErr == nil {
			return strings.TrimSpace(string(data)), nil
		}
		log.Printf("[Admin] [FetchSub] proxy/入口 fetch failed, retry direct: %v", proxyErr)
	} else {
		log.Printf("[Admin] [FetchSub] 网络客户端不可用，跳过代理直接直连")
	}

	data, directErr := fetchSubscriptionDataDirect(ctx, rawURL)
	if directErr != nil {
		if proxyErr == nil {
			return "", fmt.Errorf("direct fallback failed: %w", directErr)
		}
		return "", fmt.Errorf("proxy fetch failed: %v; direct fallback failed: %w", proxyErr, directErr)
	}
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
