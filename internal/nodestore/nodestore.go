// Package nodestore 提供 nodes / entrynodes 共用的 SQLite 持久化内核。
//
// 仅承载「表名可变、列结构固定」的通用 SQL 与事务样板，全部为纯函数 + 数据视图，
// 禁止引入任何包级可变状态；两个节点池保留各自内存态、锁、回调编排与家族特有逻辑，
// 所有 SQL 操作委托本包执行。
package nodestore

import (
	"database/sql"
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

// ---- 通用 SQL 部分（db 为 nil 时静默跳过，等价各包 db.GlobalDB == nil 现状）----

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