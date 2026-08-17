package entrynodes

import (
	"log"

	"github.com/bsfdsagfadg/vertex/internal/db"
	"github.com/bsfdsagfadg/vertex/internal/nodestore"
)

// ---- SQLite 持久化与前置节点 CRUD ----
//
// 全部 SQL 操作委托 internal/nodestore 通用内核（表名固定 "entry_nodes" /
// "entry_node_health"），本文件仅保留内存态编排与回调编排。

func ensureEntryLoaded() {
	if entryLoaded {
		return
	}
	entryLoaded = true

	if db.GlobalDB == nil {
		return
	}

	// Load nodes（回调式扫描，装载各自结构体）
	_ = nodestore.LoadNodesFull(db.GlobalDB, "entry_nodes", func(rawURI, typ, name string, disabled bool) {
		entryList = append(entryList, Node{RawURI: rawURI, Type: typ, Name: name, Disabled: disabled}) //nolint:exhaustruct
	})

	// Load health
	if hs, err := nodestore.LoadHealth(db.GlobalDB, "entry_node_health"); err == nil {
		for i := range hs {
			h := hs[i]
			entryHealthMap[h.RawURI] = &NodeHealth{ //nolint:exhaustruct
				SuccessCount:        h.SuccessCount,
				FailCount:           h.FailCount,
				ConsecutiveFailures: h.ConsecutiveFailures,
				LastTestMs:          h.LastTestMs,
				LastTestError:       h.LastTestError,
				LastSuccessAt:       h.LastSuccessAt,
				LastFailAt:          h.LastFailAt,
				CooldownUntil:       h.CooldownUntil,
			}
		}
	}

	pruneEntryHealthUnsafe()
}

// LoadEntryNodes 返回全量前置节点列表（含禁用）。
func LoadEntryNodes() []Node {
	mu.Lock()
	defer mu.Unlock()
	ensureEntryLoaded()
	return entryList
}

// LoadEntryHealth 返回前置节点健康度快照（浅拷贝，避免外部并发遍历崩溃）。
func LoadEntryHealth() map[string]*NodeHealth {
	mu.Lock()
	defer mu.Unlock()
	ensureEntryLoaded()
	cp := make(map[string]*NodeHealth, len(entryHealthMap))
	for k, v := range entryHealthMap {
		cp[k] = v
	}
	return cp
}

func saveEntryNodesUnsafe() {
	rows := make([]nodestore.NodeRow, 0, len(entryList))
	for _, n := range entryList {
		rows = append(rows, nodestore.NodeRow{RawURI: n.RawURI, Type: n.Type, Name: n.Name, Disabled: n.Disabled})
	}
	if err := nodestore.SaveNodes(db.GlobalDB, "entry_nodes", rows); err != nil {
		log.Printf("[EntryNodes] 保存节点失败: %v", err)
	}
}

func saveEntryHealthUnsafe() {
	rows := make([]nodestore.HealthRow, 0, len(entryHealthMap))
	for uri, h := range entryHealthMap {
		rows = append(rows, nodestore.HealthRow{
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
	if err := nodestore.SaveHealth(db.GlobalDB, "entry_node_health", rows); err != nil {
		log.Printf("[EntryNodes] 保存健康度失败: %v", err)
	}
}

func pruneEntryHealthUnsafe() {
	nodeKeys := make(map[string]bool, len(entryList))
	for _, n := range entryList {
		nodeKeys[n.RawURI] = true
	}
	keys := make(map[string]bool, len(entryHealthMap))
	for k := range entryHealthMap {
		keys[k] = true
	}
	nodestore.PruneHealthKeys(keys, nodeKeys)
	for k := range entryHealthMap {
		if !keys[k] {
			delete(entryHealthMap, k)
		}
	}
}

// MergeEntryNodes 追加导入新前置节点（按 raw_uri 去重）。
func MergeEntryNodes(newNodes []Node) {
	mu.Lock()
	defer mu.Unlock()
	ensureEntryLoaded()
	existing := make(map[string]bool)
	for _, n := range entryList {
		existing[n.RawURI] = true
	}
	for _, n := range newNodes {
		if !existing[n.RawURI] {
			entryList = append(entryList, n)
			existing[n.RawURI] = true
		}
	}
	pruneEntryHealthUnsafe()
	saveEntryNodesUnsafe()
}

// DedupEntryNodes 按身份键（EntryIdentityFunc）去重，返回移除数量。
func DedupEntryNodes() int {
	mu.Lock()
	ensureEntryLoaded()
	keepMap := make(map[string]bool)
	var kept []Node
	removed := 0
	var removedURIs []string
	for _, n := range entryList {
		key := n.RawURI
		if EntryIdentityFunc != nil {
			if k, ok := EntryIdentityFunc(n.RawURI); ok {
				key = k
			}
		}
		if !keepMap[key] {
			keepMap[key] = true
			kept = append(kept, n)
		} else {
			removed++
			removedURIs = append(removedURIs, n.RawURI)
			delete(entryHealthMap, n.RawURI)
		}
	}
	entryList = kept
	saveEntryNodesUnsafe()
	saveEntryHealthUnsafe()
	cb := EntryDeleteCallback
	mu.Unlock()

	if cb != nil {
		for _, u := range removedURIs {
			cb(u)
		}
	}
	return removed
}

// DeleteDisabledEntryNodes 清空已禁用的前置节点，返回删除数量。
func DeleteDisabledEntryNodes() int {
	mu.Lock()
	ensureEntryLoaded()
	var kept []Node
	removed := 0
	var removedURIs []string
	for _, n := range entryList {
		if !n.Disabled {
			kept = append(kept, n)
		} else {
			removed++
			removedURIs = append(removedURIs, n.RawURI)
			delete(entryHealthMap, n.RawURI)
		}
	}
	entryList = kept
	saveEntryNodesUnsafe()
	saveEntryHealthUnsafe()
	cb := EntryDeleteCallback
	mu.Unlock()

	if cb != nil {
		for _, u := range removedURIs {
			cb(u)
		}
	}
	return removed
}

// BatchUpdateEntryNodesDisabled 批量启用/禁用前置节点。
func BatchUpdateEntryNodesDisabled(uris []string, disabled bool) {
	mu.Lock()
	defer mu.Unlock()
	ensureEntryLoaded()
	targets := make(map[string]bool)
	for _, u := range uris {
		targets[u] = true
	}
	// 无数据库：直接按现状更新已加载的内存节点。
	if db.GlobalDB == nil {
		applyEntryToggleUnsafe(targets, disabled)
		return
	}
	// 有数据库：事务提交成功后才应用内存状态（含冷却清零与持久化），
	// 失败则内存保持原状，避免伪成功状态。
	if err := nodestore.UpdateDisabledBatch(db.GlobalDB, "entry_nodes", uris, disabled); err != nil {
		log.Printf("[EntryNodes] BatchUpdateEntryNodesDisabled 更新失败: %v", err)
		return
	}
	applyEntryToggleUnsafe(targets, disabled)
}

// applyEntryToggleUnsafe 在 DB 提交成功后应用内存状态：切换 disabled 标志，
// 启用时清零冷却并持久化健康度（DB 失败路径不得调用，保证内存与 DB 一致）。
func applyEntryToggleUnsafe(targets map[string]bool, disabled bool) {
	for i, n := range entryList {
		if targets[n.RawURI] {
			entryList[i].Disabled = disabled
		}
	}
	if !disabled {
		for u := range targets {
			if h, exists := entryHealthMap[u]; exists {
				h.CooldownUntil = 0
			}
		}
		saveEntryHealthUnsafe()
	}
}

// BatchDeleteEntryNodes 批量删除前置节点。
func BatchDeleteEntryNodes(uris []string) {
	mu.Lock()
	ensureEntryLoaded()
	targets := make(map[string]bool)
	for _, u := range uris {
		targets[u] = true
		delete(entryHealthMap, u)
	}
	var kept []Node
	for _, n := range entryList {
		if !targets[n.RawURI] {
			kept = append(kept, n)
		}
	}
	entryList = kept
	saveEntryNodesUnsafe()
	saveEntryHealthUnsafe()
	cb := EntryDeleteCallback
	mu.Unlock() // 先解锁再通知外部清理，避免卡死死锁

	if cb != nil {
		for _, u := range uris {
			cb(u)
		}
	}
}