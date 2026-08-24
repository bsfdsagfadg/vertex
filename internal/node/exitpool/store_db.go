package exitpool

import (
	"log"

	"github.com/bsfdsagfadg/vertex/internal/node/nodestore"
)

// ---- SQLite 持久化与节点 CRUD ----
//
// 全部 SQL 操作委托 internal/node/nodestore 通用内核（表名固定 "nodes" / "node_health"），
// 本文件仅保留内存态编排与回调编排；数据库句柄经 Manager 注入。

func (m *Manager) ensureLoaded() {
	if m.loaded {
		return
	}
	m.loaded = true

	if m.database == nil {
		return
	}

	// Load nodes（回调式扫描，装载各自结构体）
	_ = nodestore.LoadNodesFull(m.database, "nodes", func(rawURI, typ, name string, disabled bool) {
		m.nodeList = append(m.nodeList, Node{RawURI: rawURI, Type: typ, Name: name, Disabled: disabled}) //nolint:exhaustruct
	})
	m.rebuildNodeNameMapUnsafe()

	// Load health
	if hs, err := nodestore.LoadHealth(m.database, "node_health"); err == nil {
		for i := range hs {
			h := hs[i]
			m.healthMap[h.RawURI] = &NodeHealth{ //nolint:exhaustruct
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

	m.pruneHealthUnsafe()
}

// EnsureLoaded 强制完成节点池装载（幂等），供启动预热与测试使用。
func (m *Manager) EnsureLoaded() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLoaded()
}

func (m *Manager) LoadNodes() []Node {
	m.mu.RLock()
	if m.loaded {
		cp := make([]Node, len(m.nodeList))
		copy(cp, m.nodeList)
		m.mu.RUnlock()
		return cp
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLoaded()
	log.Printf("[Nodes] 获取所有节点 (数量: %d)", len(m.nodeList))
	cp := make([]Node, len(m.nodeList))
	copy(cp, m.nodeList)
	return cp
}

func (m *Manager) LoadHealth() map[string]*NodeHealth {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLoaded()
	// 浅拷贝后返回，避免外部并发遍历（如 json.Marshal）与写路径同时操作同一 map 导致运行时崩溃
	cp := make(map[string]*NodeHealth, len(m.healthMap))
	for k, v := range m.healthMap {
		cp[k] = v
	}
	return cp
}

func (m *Manager) saveNodesUnsafe() {
	rows := make([]nodestore.NodeRow, 0, len(m.nodeList))
	for _, n := range m.nodeList {
		rows = append(rows, nodestore.NodeRow{RawURI: n.RawURI, Type: n.Type, Name: n.Name, Disabled: n.Disabled})
	}
	if err := nodestore.SaveNodes(m.database, "nodes", rows); err != nil {
		log.Printf("[Nodes] 保存节点失败: %v", err)
	}
}

func (m *Manager) pruneHealthUnsafe() {
	nodeKeys := make(map[string]bool, len(m.nodeList))
	for _, n := range m.nodeList {
		nodeKeys[n.RawURI] = true
	}
	keys := make(map[string]bool, len(m.healthMap))
	for k := range m.healthMap {
		keys[k] = true
	}
	nodestore.PruneHealthKeys(keys, nodeKeys)
	for k := range m.healthMap {
		if !keys[k] {
			delete(m.healthMap, k)
		}
	}
}

func (m *Manager) MergeNodes(newNodes []Node) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLoaded()
	existing := make(map[string]bool)
	for _, n := range m.nodeList {
		existing[n.RawURI] = true
	}
	for _, n := range newNodes {
		if !existing[n.RawURI] {
			m.nodeList = append(m.nodeList, n)
			existing[n.RawURI] = true
		}
	}
	m.pruneHealthUnsafe()
	m.rebuildNodeNameMapUnsafe()
	m.saveNodesUnsafe()
}

// DeleteNode 删除指定 URI 的出口节点（委托 BatchDeleteNodes 统一编排）。
func (m *Manager) DeleteNode(uri string) {
	m.BatchDeleteNodes([]string{uri})
}

// filterNodesUnsafe 按 keep 谓词过滤节点池并清理被移除节点的健康度，返回被移除 URI 列表。
func (m *Manager) filterNodesUnsafe(keep func(Node) bool) []string {
	var kept []Node
	var removed []string
	for _, n := range m.nodeList {
		if keep(n) {
			kept = append(kept, n)
		} else {
			removed = append(removed, n.RawURI)
			delete(m.healthMap, n.RawURI)
		}
	}
	m.nodeList = kept
	m.rebuildNodeNameMapUnsafe()
	return removed
}

func (m *Manager) DedupNodes() int {
	m.mu.Lock()
	m.ensureLoaded()
	keepMap := make(map[string]bool)
	var kept []Node
	removed := 0
	var removedURIs []string
	for _, n := range m.nodeList {
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
		}
	}
	if removed == 0 {
		m.mu.Unlock()
		return 0
	}
	// 有数据库：定向删除先行，事务成功后才应用内存结果（失败则内存保持原状）。
	if m.database != nil {
		if err := nodestore.DeleteNodesBatch(m.database, "nodes", "node_health", removedURIs); err != nil {
			log.Printf("[Nodes] DedupNodes 删除失败: %v", err)
			m.mu.Unlock()
			return 0
		}
	}
	// 事务成功后统一清理健康度与节点列表，保证失败路径内存零变更。
	for _, u := range removedURIs {
		delete(m.healthMap, u)
	}
	m.nodeList = kept
	m.rebuildNodeNameMapUnsafe()
	hooks := m.hooks
	m.mu.Unlock() // 先解锁再通知销毁连接池

	if hooks.InvalidateParsed != nil {
		hooks.InvalidateParsed(removedURIs)
	}
	return removed
}

func (m *Manager) DeleteDisabled() int {
	m.mu.Lock()
	m.ensureLoaded()
	var removedURIs []string
	if m.database == nil {
		// 无库模式：纯内存过滤（保持现状语义）
		removedURIs = m.filterNodesUnsafe(func(n Node) bool { return !n.Disabled })
	} else {
		// 有数据库：定向删除先行，事务成功后才同步内存（失败则内存保持原状）。
		removed, uris, err := nodestore.DeleteDisabledNodes(m.database, "nodes", "node_health")
		if err != nil {
			log.Printf("[Nodes] DeleteDisabled 删除失败: %v", err)
			m.mu.Unlock()
			return 0
		}
		if removed > 0 {
			targets := make(map[string]bool, len(uris))
			for _, u := range uris {
				targets[u] = true
			}
			m.filterNodesUnsafe(func(n Node) bool { return !targets[n.RawURI] })
		}
		removedURIs = uris
	}
	hooks := m.hooks
	m.mu.Unlock()

	if hooks.InvalidateParsed != nil {
		hooks.InvalidateParsed(removedURIs)
	}
	return len(removedURIs)
}

func (m *Manager) BatchUpdateNodesDisabled(uris []string, disabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLoaded()
	targets := make(map[string]bool)
	for _, u := range uris {
		targets[u] = true
	}
	// 无数据库：直接按现状更新已加载的内存节点。
	if m.database == nil {
		for i, n := range m.nodeList {
			if targets[n.RawURI] {
				m.nodeList[i].Disabled = disabled
			}
		}
		return
	}
	// 有数据库：事务提交成功后才更新内存，失败则内存保持原状，
	// 避免「内存成功、数据库失败」的伪成功状态。
	if err := nodestore.UpdateDisabledBatch(m.database, "nodes", uris, disabled); err != nil {
		log.Printf("[Nodes] BatchUpdateNodesDisabled 更新失败: %v", err)
		return
	}
	for i, n := range m.nodeList {
		if targets[n.RawURI] {
			m.nodeList[i].Disabled = disabled
		}
	}
}

func (m *Manager) BatchDeleteNodes(uris []string) {
	if len(uris) == 0 {
		return
	}
	m.mu.Lock()
	m.ensureLoaded()
	var removedURIs []string
	targets := make(map[string]bool, len(uris))
	for _, u := range uris {
		targets[u] = true
	}
	if m.database == nil {
		// 无库模式：纯内存过滤（保持现状语义）
		removedURIs = m.filterNodesUnsafe(func(n Node) bool { return !targets[n.RawURI] })
	} else {
		// 有数据库：定向删除先行，事务成功后才同步内存（失败则内存保持原状）。
		if err := nodestore.DeleteNodesBatch(m.database, "nodes", "node_health", uris); err != nil {
			log.Printf("[Nodes] BatchDeleteNodes 删除失败: %v", err)
			m.mu.Unlock()
			return
		}
		removedURIs = m.filterNodesUnsafe(func(n Node) bool { return !targets[n.RawURI] })
	}
	hooks := m.hooks
	m.mu.Unlock() // 防止在批量删除时引发卡死死锁

	if hooks.InvalidateParsed != nil {
		hooks.InvalidateParsed(removedURIs)
	}
}
