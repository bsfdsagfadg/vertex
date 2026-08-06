package entrynodes

import (
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/db"
)

// ---- 前置节点健康度记录与可选筛选 ----

// entryFailCooldownSec 是前置节点测速失败后的冷却秒数。
// 冷却期内 GetSelectableEntryNodes 会跳过该节点，实现透明降级。
const entryFailCooldownSec = 60

func updateSingleEntryDisabledUnsafe(uri string, disabled bool) {
	if db.GlobalDB == nil {
		return
	}
	_, _ = db.GlobalDB.Exec("UPDATE entry_nodes SET disabled = ? WHERE raw_uri = ?", disabled, uri)
}

func saveSingleEntryHealthUnsafe(uri string, h *NodeHealth) {
	if db.GlobalDB == nil || h == nil {
		return
	}
	_, _ = db.GlobalDB.Exec(`INSERT OR REPLACE INTO entry_node_health 
		(raw_uri, success_count, fail_count, consecutive_failures, last_test_ms, last_test_error, last_success_at, last_fail_at, cooldown_until) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uri, h.SuccessCount, h.FailCount, h.ConsecutiveFailures, h.LastTestMs, h.LastTestError, h.LastSuccessAt, h.LastFailAt, h.CooldownUntil)
}

// RecordEntryTest 记录一次前置代理 204 连通性测试结果并落库。
// 失败时进入短暂冷却；网络类错误（拨号/连接失败）会额外自动禁用该节点。
func RecordEntryTest(uri string, ok bool, ms float64, errStr string) {
	mu.Lock()
	defer mu.Unlock()
	ensureEntryLoaded()
	h, exists := entryHealthMap[uri]
	if !exists {
		h = &NodeHealth{} //nolint:exhaustruct
		entryHealthMap[uri] = h
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
			for i := range entryList {
				if entryList[i].RawURI == uri {
					entryList[i].Disabled = true
					updateSingleEntryDisabledUnsafe(uri, true)
					break
				}
			}
		}
	}
	saveSingleEntryHealthUnsafe(uri, h)
}

// GetSelectableEntryNodes 返回可选前置节点：未禁用且不在冷却期。
func GetSelectableEntryNodes() []Node {
	mu.Lock()
	defer mu.Unlock()
	ensureEntryLoaded()
	now := time.Now().Unix()
	var out []Node
	for _, n := range entryList {
		if n.Disabled {
			continue
		}
		if h, exists := entryHealthMap[n.RawURI]; exists && h.CooldownUntil > now {
			continue
		}
		if EntryIsSupportedFunc != nil && !EntryIsSupportedFunc(n.RawURI) {
			continue
		}
		out = append(out, n)
	}
	return out
}
