package nodes

import (
	"log"

	"github.com/bsfdsagfadg/vertex/internal/db"
	"github.com/bsfdsagfadg/vertex/internal/nodestore"
)

// ---- SQLite 持久化与节点 CRUD ----
//
// 全部 SQL 操作委托 internal/nodestore 通用内核（表名固定 "nodes" / "node_health"），
// 本文件仅保留内存态编排与回调编排。

func ensureLoaded() {
	if loaded {
		return
	}
	loaded = true

	if db.GlobalDB == nil {
		return
	}

	// Load nodes（回调式扫描，装载各自结构体）
	_ = nodestore.LoadNodesFull(db.GlobalDB, "nodes", func(rawURI, typ, name string, disabled bool) {
		nodeList = append(nodeList, Node{RawURI: rawURI, Type: typ, Name: name, Disabled: disabled}) //nolint:exhaustruct
	})
	rebuildNodeNameMapUnsafe()

	// Load health
	if hs, err := nodestore.LoadHealth(db.GlobalDB, "node_health"); err == nil {
		for i := range hs {
			h := hs[i]
			healthMap[h.RawURI] = &NodeHealth{ //nolint:exhaustruct
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

	pruneHealthUnsafe()
}

func LoadNodes() []Node {
	mu.Lock()
	defer mu.Unlock()
	ensureLoaded()
	log.Printf("[Nodes] 获取所有节点 (数量: %d)", len(nodeList))
	return nodeList
}

func LoadHealth() map[string]*NodeHealth {
	mu.Lock()
	defer mu.Unlock()
	ensureLoaded()
	// 浅拷贝后返回，避免外部并发遍历（如 json.Marshal）与写路径同时操作同一 map 导致运行时崩溃
	cp := make(map[string]*NodeHealth, len(healthMap))
	for k, v := range healthMap {
		cp[k] = v
	}
	return cp
}

func saveNodesUnsafe() {
	rows := make([]nodestore.NodeRow, 0, len(nodeList))
	for _, n := range nodeList {
		rows = append(rows, nodestore.NodeRow{RawURI: n.RawURI, Type: n.Type, Name: n.Name, Disabled: n.Disabled})
	}
	if err := nodestore.SaveNodes(db.GlobalDB, "nodes", rows); err != nil {
		log.Printf("[Nodes] 保存节点失败: %v", err)
	}
}

func saveHealthUnsafe() {
	rows := make([]nodestore.HealthRow, 0, len(healthMap))
	for uri, h := range healthMap {
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
	if err := nodestore.SaveHealth(db.GlobalDB, "node_health", rows); err != nil {
		log.Printf("[Nodes] 保存健康度失败: %v", err)
	}
}

func pruneHealthUnsafe() {
	nodeKeys := make(map[string]bool, len(nodeList))
	for _, n := range nodeList {
		nodeKeys[n.RawURI] = true
	}
	keys := make(map[string]bool, len(healthMap))
	for k := range healthMap {
		keys[k] = true
	}
	nodestore.PruneHealthKeys(keys, nodeKeys)
	for k := range healthMap {
		if !keys[k] {
			delete(healthMap, k)
		}
	}
}

func MergeNodes(newNodes []Node) {
	mu.Lock()
	defer mu.Unlock()
	ensureLoaded()
	existing := make(map[string]bool)
	for _, n := range nodeList {
		existing[n.RawURI] = true
	}
	for _, n := range newNodes {
		if !existing[n.RawURI] {
			nodeList = append(nodeList, n)
			existing[n.RawURI] = true
		}
	}
	pruneHealthUnsafe()
	rebuildNodeNameMapUnsafe()
	saveNodesUnsafe()
}

func DeleteNode(uri string) {
	mu.Lock()
	ensureLoaded()
	var kept []Node
	for _, n := range nodeList {
		if n.RawURI != uri {
			kept = append(kept, n)
		}
	}
	nodeList = kept
	delete(healthMap, uri)
	rebuildNodeNameMapUnsafe()
	saveNodesUnsafe()
	saveHealthUnsafe()
	cb := DeleteNodeCallback
	mu.Unlock() // 必须先解锁，避免底层的销毁回调查找节点名称时发生死锁
	if cb != nil {
		cb(uri)
	}
}

func DedupNodes() int {
	mu.Lock()
	ensureLoaded()
	keepMap := make(map[string]bool)
	var kept []Node
	removed := 0
	var removedURIs []string
	for _, n := range nodeList {
		key := n.RawURI
		if NodeIdentityFunc != nil {
			if k, ok := NodeIdentityFunc(n.RawURI); ok {
				key = k
			}
		}
		if !keepMap[key] {
			keepMap[key] = true
			kept = append(kept, n)
		} else {
			removed++
			removedURIs = append(removedURIs, n.RawURI)
			delete(healthMap, n.RawURI)
		}
	}
	nodeList = kept
	rebuildNodeNameMapUnsafe()
	saveNodesUnsafe()
	saveHealthUnsafe()
	cb := DeleteNodeCallback
	mu.Unlock() // 先解锁再通知销毁连接池

	if cb != nil {
		for _, u := range removedURIs {
			cb(u)
		}
	}
	return removed
}

func DeleteDisabled() int {
	mu.Lock()
	ensureLoaded()
	var kept []Node
	removed := 0
	var removedURIs []string
	for _, n := range nodeList {
		if !n.Disabled {
			kept = append(kept, n)
		} else {
			removed++
			removedURIs = append(removedURIs, n.RawURI)
			delete(healthMap, n.RawURI)
		}
	}
	nodeList = kept
	rebuildNodeNameMapUnsafe()
	saveNodesUnsafe()
	saveHealthUnsafe()
	cb := DeleteNodeCallback
	mu.Unlock()

	if cb != nil {
		for _, u := range removedURIs {
			cb(u)
		}
	}
	return removed
}

func BatchUpdateNodesDisabled(uris []string, disabled bool) {
	mu.Lock()
	defer mu.Unlock()
	ensureLoaded()
	targets := make(map[string]bool)
	for _, u := range uris {
		targets[u] = true
	}
	// 无数据库：直接按现状更新已加载的内存节点。
	if db.GlobalDB == nil {
		for i, n := range nodeList {
			if targets[n.RawURI] {
				nodeList[i].Disabled = disabled
			}
		}
		return
	}
	// 有数据库：事务提交成功后才更新内存，失败则内存保持原状，
	// 避免「内存成功、数据库失败」的伪成功状态。
	if err := nodestore.UpdateDisabledBatch(db.GlobalDB, "nodes", uris, disabled); err != nil {
		log.Printf("[Nodes] BatchUpdateNodesDisabled 更新失败: %v", err)
		return
	}
	for i, n := range nodeList {
		if targets[n.RawURI] {
			nodeList[i].Disabled = disabled
		}
	}
}

func BatchDeleteNodes(uris []string) {
	mu.Lock()
	ensureLoaded()
	targets := make(map[string]bool)
	for _, u := range uris {
		targets[u] = true
		delete(healthMap, u)
	}
	var kept []Node
	for _, n := range nodeList {
		if !targets[n.RawURI] {
			kept = append(kept, n)
		}
	}
	nodeList = kept
	rebuildNodeNameMapUnsafe()
	saveNodesUnsafe()
	saveHealthUnsafe()
	cb := DeleteNodeCallback
	mu.Unlock() // 防止在批量删除时引发卡死死锁

	if cb != nil {
		for _, u := range uris {
			cb(u)
		}
	}
}