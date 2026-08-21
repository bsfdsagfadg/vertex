package entrypool

import (
	"log"

	"github.com/bsfdsagfadg/vertex/internal/node/nodestore"
)

// ---- SQLite 持久化与前置节点 CRUD ----
//
// 全部 SQL 操作委托 internal/node/nodestore 通用内核（表名固定 "entry_nodes" /
// "entry_node_health"），本文件仅保留内存态编排与回调编排；数据库句柄经 EntryManager 注入。

func (m *EntryManager) ensureEntryLoaded() {
	if m.entryLoaded {
		return
	}
	m.entryLoaded = true

	if m.database == nil {
		return
	}

	// Load nodes（回调式扫描，装载各自结构体）
	_ = nodestore.LoadNodesFull(m.database, "entry_nodes", func(rawURI, typ, name string, disabled bool) {
		m.entryList = append(m.entryList, Node{RawURI: rawURI, Type: typ, Name: name, Disabled: disabled}) //nolint:exhaustruct
	})

	// Load health
	if hs, err := nodestore.LoadHealth(m.database, "entry_node_health"); err == nil {
		for i := range hs {
			h := hs[i]
			m.entryHealthMap[h.RawURI] = &NodeHealth{ //nolint:exhaustruct
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

	m.pruneEntryHealthUnsafe()
}

// LoadNodes 返回全量前置节点列表（含禁用）。
// 返回浅拷贝切片，切断与内部全局切片底层的引用共享（对齐 LoadHealth 安全设计）。
func (m *EntryManager) LoadNodes() []Node {
	m.mu.RLock()
	if m.entryLoaded {
		cp := make([]Node, len(m.entryList))
		copy(cp, m.entryList)
		m.mu.RUnlock()
		return cp
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureEntryLoaded()
	cp := make([]Node, len(m.entryList))
	copy(cp, m.entryList)
	return cp
}

// LoadHealth 返回前置节点健康度快照（浅拷贝，避免外部并发遍历崩溃）。
func (m *EntryManager) LoadHealth() map[string]*NodeHealth {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureEntryLoaded()
	cp := make(map[string]*NodeHealth, len(m.entryHealthMap))
	for k, v := range m.entryHealthMap {
		cp[k] = v
	}
	return cp
}

func (m *EntryManager) saveEntryNodesUnsafe() {
	rows := make([]nodestore.NodeRow, 0, len(m.entryList))
	for _, n := range m.entryList {
		rows = append(rows, nodestore.NodeRow{RawURI: n.RawURI, Type: n.Type, Name: n.Name, Disabled: n.Disabled})
	}
	if err := nodestore.SaveNodes(m.database, "entry_nodes", rows); err != nil {
		log.Printf("[EntryNodes] 保存节点失败: %v", err)
	}
}

func (m *EntryManager) saveEntryHealthUnsafe() {
	rows := make([]nodestore.HealthRow, 0, len(m.entryHealthMap))
	for uri, h := range m.entryHealthMap {
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
	if err := nodestore.SaveHealth(m.database, "entry_node_health", rows); err != nil {
		log.Printf("[EntryNodes] 保存健康度失败: %v", err)
	}
}

func (m *EntryManager) pruneEntryHealthUnsafe() {
	nodeKeys := make(map[string]bool, len(m.entryList))
	for _, n := range m.entryList {
		nodeKeys[n.RawURI] = true
	}
	keys := make(map[string]bool, len(m.entryHealthMap))
	for k := range m.entryHealthMap {
		keys[k] = true
	}
	nodestore.PruneHealthKeys(keys, nodeKeys)
	for k := range m.entryHealthMap {
		if !keys[k] {
			delete(m.entryHealthMap, k)
		}
	}
}

// MergeNodes 追加导入新前置节点（按 raw_uri 去重）。
func (m *EntryManager) MergeNodes(newNodes []Node) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureEntryLoaded()
	existing := make(map[string]bool)
	for _, n := range m.entryList {
		existing[n.RawURI] = true
	}
	for _, n := range newNodes {
		if !existing[n.RawURI] {
			m.entryList = append(m.entryList, n)
			existing[n.RawURI] = true
		}
	}
	m.pruneEntryHealthUnsafe()
	m.saveEntryNodesUnsafe()
}

// DedupNodes 按身份键（IdentityResolver）去重，返回移除数量。
func (m *EntryManager) DedupNodes() int {
	m.mu.Lock()
	m.ensureEntryLoaded()
	keepMap := make(map[string]bool)
	var kept []Node
	removed := 0
	var removedURIs []string
	for _, n := range m.entryList {
		key := n.RawURI
		if m.identity != nil {
			if k, ok := m.identity.Identity(n.RawURI); ok {
				key = k
			}
		}
		if !keepMap[key] {
			keepMap[key] = true
			kept = append(kept, n)
		} else {
			removed++
			removedURIs = append(removedURIs, n.RawURI)
			delete(m.entryHealthMap, n.RawURI)
		}
	}
	m.entryList = kept
	m.saveEntryNodesUnsafe()
	m.saveEntryHealthUnsafe()
	hooks := m.hooks
	m.mu.Unlock()

	if hooks.InvalidateParsed != nil {
		for _, u := range removedURIs {
			hooks.InvalidateParsed(u)
		}
	}
	return removed
}

// DeleteDisabled 清空已禁用的前置节点，返回删除数量。
func (m *EntryManager) DeleteDisabled() int {
	m.mu.Lock()
	m.ensureEntryLoaded()
	var kept []Node
	removed := 0
	var removedURIs []string
	for _, n := range m.entryList {
		if !n.Disabled {
			kept = append(kept, n)
		} else {
			removed++
			removedURIs = append(removedURIs, n.RawURI)
			delete(m.entryHealthMap, n.RawURI)
		}
	}
	m.entryList = kept
	m.saveEntryNodesUnsafe()
	m.saveEntryHealthUnsafe()
	hooks := m.hooks
	m.mu.Unlock()

	if hooks.InvalidateParsed != nil {
		for _, u := range removedURIs {
			hooks.InvalidateParsed(u)
		}
	}
	return removed
}

// BatchUpdateNodesDisabled 批量启用/禁用前置节点。
func (m *EntryManager) BatchUpdateNodesDisabled(uris []string, disabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureEntryLoaded()
	targets := make(map[string]bool)
	for _, u := range uris {
		targets[u] = true
	}
	// 无数据库：直接按现状更新已加载的内存节点。
	if m.database == nil {
		m.applyEntryToggleUnsafe(targets, disabled)
		return
	}
	// 有数据库：事务提交成功后才应用内存状态（含冷却清零与持久化），
	// 失败则内存保持原状，避免伪成功状态。
	if err := nodestore.UpdateDisabledBatch(m.database, "entry_nodes", uris, disabled); err != nil {
		log.Printf("[EntryNodes] BatchUpdateEntryNodesDisabled 更新失败: %v", err)
		return
	}
	m.applyEntryToggleUnsafe(targets, disabled)
}

// applyEntryToggleUnsafe 在 DB 提交成功后应用内存状态：切换 disabled 标志，
// 启用时清零冷却并持久化健康度（DB 失败路径不得调用，保证内存与 DB 一致）。
func (m *EntryManager) applyEntryToggleUnsafe(targets map[string]bool, disabled bool) {
	for i, n := range m.entryList {
		if targets[n.RawURI] {
			m.entryList[i].Disabled = disabled
		}
	}
	if !disabled {
		for u := range targets {
			if h, exists := m.entryHealthMap[u]; exists {
				h.CooldownUntil = 0
			}
		}
		m.saveEntryHealthUnsafe()
	}
}

// BatchDeleteNodes 批量删除前置节点。
func (m *EntryManager) BatchDeleteNodes(uris []string) {
	m.mu.Lock()
	m.ensureEntryLoaded()
	targets := make(map[string]bool)
	for _, u := range uris {
		targets[u] = true
		delete(m.entryHealthMap, u)
	}
	var kept []Node
	for _, n := range m.entryList {
		if !targets[n.RawURI] {
			kept = append(kept, n)
		}
	}
	m.entryList = kept
	m.saveEntryNodesUnsafe()
	m.saveEntryHealthUnsafe()
	hooks := m.hooks
	m.mu.Unlock() // 先解锁再通知外部清理，避免卡死死锁

	if hooks.InvalidateParsed != nil {
		for _, u := range uris {
			hooks.InvalidateParsed(u)
		}
	}
}
