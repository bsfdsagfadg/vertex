// Package nodestore 提供 exitpool / entrypool 共用的 SQLite 持久化内核。
//
// 仅承载「表名可变、列结构固定」的通用 SQL 与事务样板，全部为纯函数 + 数据视图，
// 禁止引入任何包级可变状态；两个节点池保留各自内存态、锁、回调编排与家族特有逻辑，
// 所有 SQL 操作委托本包执行。
package nodestore

import (
	"database/sql"
	"strings"
)

// NodeRow 是节点表行的最小持久化视图。
// Type / Name 由全量替换保存（SaveNodes）写入，缺一不可，否则重启后字段丢失。
type NodeRow struct {
	RawURI   string
	Type     string
	Name     string
	Disabled bool
}

// HealthRow 是健康度表行的持久化视图（9 列，与两个健康表 schema 对齐）。
type HealthRow struct {
	RawURI              string
	SuccessCount        int
	FailCount           int
	ConsecutiveFailures int
	LastTestMs          float64
	LastTestError       string
	LastSuccessAt       int64
	LastFailAt          int64
	CooldownUntil       int64
}

// ---- 通用 SQL 部分（db 为 nil 时静默跳过，对齐各节点池无库内存模式）----

// LoadNodesFull 回调式扫描节点表（raw_uri, type, name, disabled 四列），
// 调用方在 scan 回调内填充各自结构体，避免两个包重复 SELECT 样板。
// nodeTable 仅接受包内常量表名（"nodes" / "entry_nodes"），不接收外部输入。
func LoadNodesFull(db *sql.DB, nodeTable string, scan func(rawURI, typ, name string, disabled bool)) error {
	if db == nil {
		return nil
	}
	rows, err := db.Query("SELECT raw_uri, type, name, disabled FROM " + nodeTable)
	if err != nil {
		return err
	}
	defer func() {
		_ = rows.Close()
	}()
	for rows.Next() {
		var rawURI, typ, name string
		var disabled bool
		if err := rows.Scan(&rawURI, &typ, &name, &disabled); err != nil {
			return err
		}
		scan(rawURI, typ, name, disabled)
	}
	return rows.Err()
}

// LoadHealth 扫描健康度表（9 列）为行视图；db 为 nil 时返回空切片。
func LoadHealth(db *sql.DB, healthTable string) ([]HealthRow, error) {
	if db == nil {
		return nil, nil
	}
	rows, err := db.Query(`SELECT raw_uri, success_count, fail_count, consecutive_failures,
		last_test_ms, last_test_error, last_success_at, last_fail_at, cooldown_until FROM ` + healthTable)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	var out []HealthRow
	for rows.Next() {
		var h HealthRow
		if err := rows.Scan(&h.RawURI, &h.SuccessCount, &h.FailCount, &h.ConsecutiveFailures,
			&h.LastTestMs, &h.LastTestError, &h.LastSuccessAt, &h.LastFailAt, &h.CooldownUntil); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// SaveNodes 全量替换保存：先 DELETE 全表再逐行插入，维持现状全量替换语义。
//
// 注意：SQLite 未启用 foreign_keys，DELETE 不级联清空健康表，陈旧健康行的内存
// 剪枝由 PruneHealthKeys 承担（调用方必须在 SaveNodes 后执行，不得依赖级联假设）。
func SaveNodes(db *sql.DB, nodeTable string, rows []NodeRow) error {
	if db == nil {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM " + nodeTable); err != nil {
		_ = tx.Rollback()
		return err
	}
	stmt, err := tx.Prepare("INSERT INTO " + nodeTable + " (raw_uri, type, name, disabled) VALUES (?, ?, ?, ?)")
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, r := range rows {
		if _, err := stmt.Exec(r.RawURI, r.Type, r.Name, r.Disabled); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			return err
		}
	}
	if err := stmt.Close(); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// SaveHealth 追加式替换保存：逐行 INSERT OR REPLACE，不得 DELETE（与现状一致）。
func SaveHealth(db *sql.DB, healthTable string, rows []HealthRow) error {
	if db == nil {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO ` + healthTable + `
		(raw_uri, success_count, fail_count, consecutive_failures, last_test_ms, last_test_error, last_success_at, last_fail_at, cooldown_until) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, h := range rows {
		if _, err := stmt.Exec(h.RawURI, h.SuccessCount, h.FailCount, h.ConsecutiveFailures,
			h.LastTestMs, h.LastTestError, h.LastSuccessAt, h.LastFailAt, h.CooldownUntil); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			return err
		}
	}
	if err := stmt.Close(); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// UpsertDisabled 单行更新 disabled 标志（UPDATE，无匹配行则零影响）。
func UpsertDisabled(db *sql.DB, nodeTable string, uri string, disabled bool) error {
	if db == nil {
		return nil
	}
	_, err := db.Exec("UPDATE "+nodeTable+" SET disabled = ? WHERE raw_uri = ?", disabled, uri)
	return err
}

// UpsertHealth 单行写入健康度（INSERT OR REPLACE）。
func UpsertHealth(db *sql.DB, healthTable string, h HealthRow) error {
	if db == nil {
		return nil
	}
	_, err := db.Exec(`INSERT OR REPLACE INTO `+healthTable+`
		(raw_uri, success_count, fail_count, consecutive_failures, last_test_ms, last_test_error, last_success_at, last_fail_at, cooldown_until) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		h.RawURI, h.SuccessCount, h.FailCount, h.ConsecutiveFailures,
		h.LastTestMs, h.LastTestError, h.LastSuccessAt, h.LastFailAt, h.CooldownUntil)
	return err
}

// UpdateDisabledBatch 事务内批量更新 disabled：Begin → Prepare → 逐条 Exec → Close →
// Commit，任一环节失败即 Rollback 并返回 error；重复 URI 天然幂等（UPDATE 语义）。
func UpdateDisabledBatch(db *sql.DB, nodeTable string, uris []string, disabled bool) error {
	if db == nil || len(uris) == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare("UPDATE " + nodeTable + " SET disabled = ? WHERE raw_uri = ?")
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, u := range uris {
		if _, err := stmt.Exec(disabled, u); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			return err
		}
	}
	if err := stmt.Close(); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// PruneHealthKeys 从 healthKeys 中删除所有不在 nodeKeys 里的键（内存剪枝）。
func PruneHealthKeys(healthKeys map[string]bool, nodeKeys map[string]bool) {
	for k := range healthKeys {
		if !nodeKeys[k] {
			delete(healthKeys, k)
		}
	}
}

// deleteNodesBatchTx 在同一事务内分批执行节点与健康表定向删除，
// 避免全表清空重写（SaveNodes 语义）在删除少量节点时的写放大。
func deleteNodesBatchTx(tx *sql.Tx, nodeTable, healthTable string, uris []string) error {
	stmtNode, err := tx.Prepare("DELETE FROM " + nodeTable + " WHERE raw_uri IN (" + placeholders(len(uris)) + ")")
	if err != nil {
		return err
	}
	defer func() { _ = stmtNode.Close() }()
	stmtHealth, err := tx.Prepare("DELETE FROM " + healthTable + " WHERE raw_uri IN (" + placeholders(len(uris)) + ")")
	if err != nil {
		return err
	}
	defer func() { _ = stmtHealth.Close() }()
	args := make([]any, len(uris))
	for i, u := range uris {
		args[i] = u
	}
	if _, err := stmtNode.Exec(args...); err != nil {
		return err
	}
	if _, err := stmtHealth.Exec(args...); err != nil {
		return err
	}
	return nil
}

// deleteBatchSize 单条 IN 子句占位符上限（SQLite 变量数限制安全值）。
const deleteBatchSize = 500

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?, ", n-1) + "?"
}

// DeleteDisabledNodes 定向删除全部 disabled=1 的节点行及其陈旧健康行（同一事务），
// 返回被删节点 URI 列表；db 为 nil 时返回 (0, nil, nil)，由调用方走纯内存分支。
// 健康表清理采用 NOT IN 语义对齐内存 pruneHealthUnsafe（foreign_keys 未启用，须显式清理）。
func DeleteDisabledNodes(db *sql.DB, nodeTable, healthTable string) (int, []string, error) {
	if db == nil {
		return 0, nil, nil
	}
	tx, err := db.Begin()
	if err != nil {
		return 0, nil, err
	}
	rows, err := tx.Query("SELECT raw_uri FROM " + nodeTable + " WHERE disabled = 1")
	if err != nil {
		_ = tx.Rollback()
		return 0, nil, err
	}
	var uris []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			_ = rows.Close()
			_ = tx.Rollback()
			return 0, nil, err
		}
		uris = append(uris, u)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		_ = tx.Rollback()
		return 0, nil, err
	}
	_ = rows.Close()
	if _, err := tx.Exec("DELETE FROM " + nodeTable + " WHERE disabled = 1"); err != nil {
		_ = tx.Rollback()
		return 0, nil, err
	}
	if _, err := tx.Exec("DELETE FROM " + healthTable + " WHERE raw_uri NOT IN (SELECT raw_uri FROM " + nodeTable + ")"); err != nil {
		_ = tx.Rollback()
		return 0, nil, err
	}
	if err := tx.Commit(); err != nil {
		return 0, nil, err
	}
	return len(uris), uris, nil
}

// DeleteNodesBatch 定向删除指定 URI 的节点行及其健康行（同一事务内分批执行）；
// 空 URI 列表与 db 为 nil 时静默跳过。仅删除不重写，替换全表清空语义仅适用于删除场景。
func DeleteNodesBatch(db *sql.DB, nodeTable, healthTable string, uris []string) error {
	if db == nil || len(uris) == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	for start := 0; start < len(uris); start += deleteBatchSize {
		end := start + deleteBatchSize
		if end > len(uris) {
			end = len(uris)
		}
		if err := deleteNodesBatchTx(tx, nodeTable, healthTable, uris[start:end]); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}
