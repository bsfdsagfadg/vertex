package entrynodes

import (
	"github.com/bsfdsagfadg/vertex/internal/db"
)

// ---- SQLite 持久化与前置节点 CRUD ----

func ensureEntryLoaded() {
	if entryLoaded {
		return
	}
	entryLoaded = true

	if db.GlobalDB == nil {
		return
	}

	rows, err := db.GlobalDB.Query("SELECT raw_uri, type, name, disabled FROM entry_nodes")
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
		entryList = nodes
	}

	hRows, err := db.GlobalDB.Query("SELECT raw_uri, success_count, fail_count, consecutive_failures, last_test_ms, last_test_error, last_success_at, last_fail_at, cooldown_until FROM entry_node_health")
	if err == nil {
		defer func() {
			_ = hRows.Close()
		}()
		for hRows.Next() {
			var uri string
			h := &NodeHealth{} //nolint:exhaustruct
			if err := hRows.Scan(&uri, &h.SuccessCount, &h.FailCount, &h.ConsecutiveFailures, &h.LastTestMs, &h.LastTestError, &h.LastSuccessAt, &h.LastFailAt, &h.CooldownUntil); err == nil {
				entryHealthMap[uri] = h
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
	if db.GlobalDB == nil {
		return
	}
	tx, err := db.GlobalDB.Begin()
	if err != nil {
		return
	}
	_, err = tx.Exec("DELETE FROM entry_nodes")
	if err != nil {
		_ = tx.Rollback()
		return
	}
	stmt, err := tx.Prepare("INSERT INTO entry_nodes (raw_uri, type, name, disabled) VALUES (?, ?, ?, ?)")
	if err != nil || stmt == nil {
		_ = tx.Rollback()
		return
	}
	for _, n := range entryList {
		_, _ = stmt.Exec(n.RawURI, n.Type, n.Name, n.Disabled)
	}
	_ = stmt.Close()
	_ = tx.Commit()
}

func saveEntryHealthUnsafe() {
	if db.GlobalDB == nil {
		return
	}
	tx, err := db.GlobalDB.Begin()
	if err != nil {
		return
	}
	stmt, _ := tx.Prepare(`INSERT OR REPLACE INTO entry_node_health 
		(raw_uri, success_count, fail_count, consecutive_failures, last_test_ms, last_test_error, last_success_at, last_fail_at, cooldown_until) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if stmt == nil {
		_ = tx.Rollback()
		return
	}
	for uri, h := range entryHealthMap {
		_, _ = stmt.Exec(uri, h.SuccessCount, h.FailCount, h.ConsecutiveFailures, h.LastTestMs, h.LastTestError, h.LastSuccessAt, h.LastFailAt, h.CooldownUntil)
	}
	_ = stmt.Close()
	_ = tx.Commit()
}

func pruneEntryHealthUnsafe() {
	for uri := range entryHealthMap {
		found := false
		for _, n := range entryList {
			if n.RawURI == uri {
				found = true
				break
			}
		}
		if !found {
			delete(entryHealthMap, uri)
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

// DeleteEntryNode 删除单个前置节点及其健康度记录。
func DeleteEntryNode(uri string) {
	mu.Lock()
	ensureEntryLoaded()
	var kept []Node
	for _, n := range entryList {
		if n.RawURI != uri {
			kept = append(kept, n)
		}
	}
	entryList = kept
	delete(entryHealthMap, uri)
	saveEntryNodesUnsafe()
	saveEntryHealthUnsafe()
	cb := EntryDeleteCallback
	mu.Unlock() // 先解锁再通知外部清理（如关闭 sing-box 实例），避免死锁
	if cb != nil {
		cb(uri)
	}
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
		if !disabled {
			if h, exists := entryHealthMap[u]; exists {
				h.CooldownUntil = 0
			}
		}
	}
	for i, n := range entryList {
		if targets[n.RawURI] {
			entryList[i].Disabled = disabled
		}
	}
	if !disabled {
		saveEntryHealthUnsafe()
	}
	if db.GlobalDB != nil && len(uris) > 0 {
		tx, err := db.GlobalDB.Begin()
		if err == nil {
			stmt, _ := tx.Prepare("UPDATE entry_nodes SET disabled = ? WHERE raw_uri = ?")
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
