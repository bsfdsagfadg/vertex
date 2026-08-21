package entrypool

import (
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/node/nodestore"
)

// ---- 前置节点健康度记录与可选筛选 ----

// entryFailCooldownSec 是前置节点测速失败后的冷却秒数。
// 冷却期内 SelectableNodes 会跳过该节点，实现透明降级。
const entryFailCooldownSec = 60

func (m *EntryManager) updateSingleEntryDisabledUnsafe(uri string, disabled bool) {
	_ = nodestore.UpsertDisabled(m.database, "entry_nodes", uri, disabled)
}

func (m *EntryManager) saveSingleEntryHealthUnsafe(uri string, h *NodeHealth) {
	if h == nil {
		return
	}
	_ = nodestore.UpsertHealth(m.database, "entry_node_health", nodestore.HealthRow{
		RawURI:              uri,
		SuccessCount:        h.SuccessCount,
		FailCount:           h.FailCount,
		ConsecutiveFailures: h.ConsecutiveFailures,
		LastTestMs:          h.LastTestMs,
		LastTestError:       h.LastTestError,
		LastSuccessAt:       h.LastSuccessAt,
		LastFailAt:          h.LastFailAt,
		CooldownUntil:       h.CooldownUntil,
	})
}

// RecordTest 记录一次前置代理 204 连通性测试结果并落库。
// 失败时进入短暂冷却；网络类错误（拨号/连接失败）会额外自动禁用该节点。
func (m *EntryManager) RecordTest(uri string, ok bool, ms float64, errStr string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureEntryLoaded()
	h, exists := m.entryHealthMap[uri]
	if !exists {
		h = &NodeHealth{} //nolint:exhaustruct
		m.entryHealthMap[uri] = h
	}
	h.LastTestMs = ms
	h.LastTestError = errStr
	if ok {
		h.SuccessCount++
		h.ConsecutiveFailures = 0
		h.LastSuccessAt = time.Now().Unix()
		h.CooldownUntil = 0
	} else {
		h.FailCount++
		h.ConsecutiveFailures++
		h.LastFailAt = time.Now().Unix()
		h.CooldownUntil = time.Now().Unix() + entryFailCooldownSec

		errLower := strings.ToLower(errStr)
		if strings.Contains(errLower, "dial") || strings.Contains(errLower, "refused") ||
			strings.Contains(errLower, "i/o timeout") || strings.Contains(errLower, "deadline exceeded") ||
			strings.Contains(errLower, "connection") {
			for i := range m.entryList {
				if m.entryList[i].RawURI == uri {
					m.entryList[i].Disabled = true
					m.updateSingleEntryDisabledUnsafe(uri, true)
					break
				}
			}
		}
	}
	m.saveSingleEntryHealthUnsafe(uri, h)
}

// SelectableNodes 返回可选前置节点：未禁用且不在冷却期。
func (m *EntryManager) SelectableNodes() []Node {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureEntryLoaded()
	now := time.Now().Unix()
	var out []Node
	for _, n := range m.entryList {
		if n.Disabled {
			continue
		}
		if h, exists := m.entryHealthMap[n.RawURI]; exists && h.CooldownUntil > now {
			continue
		}
		if m.identity != nil && !m.identity.Supported(n.RawURI) {
			continue
		}
		out = append(out, n)
	}
	return out
}
