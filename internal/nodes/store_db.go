package nodes

import (
	"log"

	"github.com/bsfdsagfadg/vertex/internal/db"
)

// ---- SQLite 持久化与节点 CRUD ----

func ensureLoaded() {
	if loaded {
		return
	}
	loaded = true

	if db.GlobalDB == nil {
		return
	}

	// Load nodes
	rows, err := db.GlobalDB.Query("SELECT raw_uri, type, name, disabled FROM nodes")
	if err == nil {
		defer func() {
			_ = rows.Close()
		}()
		nodes := []Node{}
		for rows.Next() {
			var n Node
			if err := rows.Scan(&n.RawURI, &n.Type, &n.Name, &n.Disabled); err == nil {
				nodes = append(nodes, n)
			}
		}
		nodeList = nodes
		rebuildNodeNameMapUnsafe()
	}

	// Load health
	hRows, err := db.GlobalDB.Query("SELECT raw_uri, success_count, fail_count, consecutive_failures, last_test_ms, last_test_error, last_success_at, last_fail_at, cooldown_until FROM node_health")
	if err == nil {
		defer func() {
			_ = hRows.Close()
		}()
		for hRows.Next() {
			var uri string
			h := &NodeHealth{} //nolint:exhaustruct
			if err := hRows.Scan(&uri, &h.SuccessCount, &h.FailCount, &h.ConsecutiveFailures, &h.LastTestMs, &h.LastTestError, &h.LastSuccessAt, &h.LastFailAt, &h.CooldownUntil); err == nil {
				healthMap[uri] = h
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
	if db.GlobalDB == nil {
		return
	}
	tx, err := db.GlobalDB.Begin()
	if err != nil {
		return
	}
	// 为了简单起见，可以先全量删除再插入，但最好的方式是逐个插入或在添加删除时调用单个 SQL
	// 这里保持原来 saveNodesUnsafe 的全量保存语义，执行全量同步
	_, _ = tx.Exec("DELETE FROM nodes")
	stmt, _ := tx.Prepare("INSERT INTO nodes (raw_uri, type, name, disabled) VALUES (?, ?, ?, ?)")
	for _, n := range nodeList {
		if stmt != nil {
			_, _ = stmt.Exec(n.RawURI, n.Type, n.Name, n.Disabled)
		}
	}
	if stmt != nil {
		_ = stmt.Close()
	}
	_ = tx.Commit()
}

func saveHealthUnsafe() {
	if db.GlobalDB == nil {
		return
	}
	tx, err := db.GlobalDB.Begin()
	if err != nil {
		return
	}
	stmt, _ := tx.Prepare(`INSERT OR REPLACE INTO node_health 
		(raw_uri, success_count, fail_count, consecutive_failures, last_test_ms, last_test_error, last_success_at, last_fail_at, cooldown_until) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if stmt == nil {
		_ = tx.Rollback()
		return
	}
	for uri, h := range healthMap {
		_, _ = stmt.Exec(uri, h.SuccessCount, h.FailCount, h.ConsecutiveFailures, h.LastTestMs, h.LastTestError, h.LastSuccessAt, h.LastFailAt, h.CooldownUntil)
	}
	_ = stmt.Close()
	_ = tx.Commit()
}

func pruneHealthUnsafe() {
	for uri := range healthMap {
		found := false
		for _, n := range nodeList {
			if n.RawURI == uri {
				found = true
				break
			}
		}
		if !found {
			delete(healthMap, uri)
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
	for i, n := range nodeList {
		if targets[n.RawURI] {
			nodeList[i].Disabled = disabled
		}
	}
	if db.GlobalDB != nil && len(uris) > 0 {
		tx, err := db.GlobalDB.Begin()
		if err == nil {
			stmt, _ := tx.Prepare("UPDATE nodes SET disabled = ? WHERE raw_uri = ?")
			if stmt != nil {
				for _, u := range uris {
					_, _ = stmt.Exec(disabled, u)
				}
				_ = stmt.Close()
			}
			_ = tx.Commit()
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
