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

// adminGetProxyNodes 返回前置代理池全量节点（含禁用）及启用/禁用计数。
func (adm *AdminHandler) adminGetProxyNodes(w http.ResponseWriter, _ *http.Request) {
	nodes := config.GetProxyCandidates()
	enabledCount, disabledCount := 0, 0
	for _, n := range nodes {
		if n.Disabled {
			disabledCount++
		} else {
			enabledCount++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes":          nodes,
		"total":          len(nodes),
		"enabled_count":  enabledCount,
		"disabled_count": disabledCount,
	})
}

// adminImportProxyNode 导入单个前置代理 URI 到轮询池；重复条目自动忽略。
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
	log.Printf("[Admin] [ImportProxyNode] 导入成功: %s (%s)", redactProxyURI(body.RawURI), candidate.Type)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// adminToggleProxyNodes 批量启用/禁用前置代理节点。
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
	if err := config.SetProxyCandidatesDisabled(body.URIs, body.Disabled); err != nil {
		writeJSON(w, http.StatusBadRequest, adminErr(err.Error()))
		return
	}
	action := "禁用"
	if !body.Disabled {
		action = "启用"
	}
	log.Printf("[Admin] [ToggleProxyNode] 已%s %d 个前置节点", action, len(body.URIs))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// adminBatchDeleteProxyNodes 批量删除前置代理节点。
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
	if err := config.BatchRemoveProxyCandidates(body.URIs); err != nil {
		writeJSON(w, http.StatusBadRequest, adminErr(err.Error()))
		return
	}
	log.Printf("[Admin] [BatchDeleteProxyNode] 已删除 %d 个前置节点", len(body.URIs))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// adminDeleteDisabledProxyNodes 清空所有已禁用的前置代理节点。
func (adm *AdminHandler) adminDeleteDisabledProxyNodes(w http.ResponseWriter, _ *http.Request) {
	n, err := config.RemoveDisabledProxyCandidates()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted_count": n})
}

// adminDedupProxyNodes 去重前置代理节点。
func (adm *AdminHandler) adminDedupProxyNodes(w http.ResponseWriter, _ *http.Request) {
	removed, err := config.DedupProxyCandidates()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "removed_count": removed})
}

// entryProxyProbeURL 候选拨测目标：业务域而非 gstatic 204（P9）。
// 消除"204 假健康"：候选测速目标与真实业务目标（google recaptcha）一致。
const entryProxyProbeURL = "https://www.google.com/recaptcha/enterprise.js"

// probeEntryProxyCandidate 直连候选自身探测业务域可达性（不挂入口），返回耗时与错误。
// 入口健康 = 能拨通出站代理形成链式代理：期望 HTTP 200，非 200 即失败。
func probeEntryProxyCandidate(ctx context.Context, netClient *transport.NetworkClient, rawURI string) (float64, error) {
	start := time.Now()
	session, err := netClient.CreateSessionWithoutEntryProxy(15, rawURI, "probe-entry")
	if err != nil {
		return 0, err
	}
	defer session.Close()
	status, _, err := session.DoAndRead(ctx, http.MethodGet, entryProxyProbeURL, nil, nil)
	if err == nil && status != http.StatusOK {
		err = fmt.Errorf("预期 HTTP 200，收到 %d", status)
	}
	return float64(time.Since(start).Milliseconds()), err
}

// adminTestProxyNode 测试指定前置代理对业务域的可达性并写入测试结果。
// 网络类失败会自动禁用该节点（见 config.UpdateProxyCandidateTest）；恢复后由周期拨测自愈解禁。
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

	elapsed, err := probeEntryProxyCandidate(ctx, adm.vc.Net(), body.RawURI)
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

// entryProbeInterval 后台周期拨测间隔（P9：默认每 5min 对每个启用候选探测业务域）。
const entryProbeInterval = 5 * time.Minute

// entryProbeTimeout 单候选周期拨测超时。
const entryProbeTimeout = 20 * time.Second

// StartEntryProxyProbeLoop 启动后台周期拨测（自愈）：
// 成功 → UpdateProxyCandidateTest(true,…)；若候选曾因网络错误被自动 Disabled，
// 再 SetProxyCandidatesDisabled(false) 解除（防短暂抖动被永久踢出）。
// 失败（网络类）→ UpdateProxyCandidateTest(false,…) 走既有自动禁用路径。
// 探测结果只写持久化状态，不记入任何运行期熔断（机制已废弃）。
func StartEntryProxyProbeLoop(netClient *transport.NetworkClient) {
	if netClient == nil {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[EntryProbe] 周期拨测循环 panic: %v", r)
			}
		}()
		for range time.Tick(entryProbeInterval) {
			probeAllEnabledProxyCandidates(netClient)
		}
	}()
}

func probeAllEnabledProxyCandidates(netClient *transport.NetworkClient) {
	for _, candidate := range config.GetProxyCandidates() {
		if candidate.RawURI == "" || candidate.Disabled {
			continue
		}
		go func(rawURI string) {
			ctx, cancel := context.WithTimeout(context.Background(), entryProbeTimeout)
			defer cancel()
			elapsed, err := probeEntryProxyCandidate(ctx, netClient, rawURI)
			if err != nil {
				log.Printf("[EntryProbe] 候选 %s 业务域拨测失败: %v", redactProxyURI(rawURI), err)
				_ = config.UpdateProxyCandidateTest(rawURI, false, elapsed, err.Error())
				return
			}
			// 成功：更新状态；若候选此前因网络错误被自动禁用，则解除（自愈）。
			healEnabledProxyCandidate(rawURI)
			if opErr := config.UpdateProxyCandidateTest(rawURI, true, elapsed, ""); opErr != nil {
				log.Printf("[EntryProbe] 更新候选 %s 状态失败: %v", redactProxyURI(rawURI), opErr)
			}
		}(candidate.RawURI)
	}
}

// healEnabledProxyCandidate 周期拨测成功后的自愈：候选曾因网络错误被自动 Disabled 时解除，
// 防短暂抖动被永久踢出（对齐"候选常驻"理念）。
func healEnabledProxyCandidate(rawURI string) {
	for _, existing := range config.GetProxyCandidates() {
		if existing.RawURI != rawURI || !existing.Disabled {
			continue
		}
		if opErr := config.SetProxyCandidatesDisabled([]string{rawURI}, false); opErr != nil {
			log.Printf("[EntryProbe] 自愈解禁候选 %s 失败: %v", redactProxyURI(rawURI), opErr)
		}
		return
	}
}
