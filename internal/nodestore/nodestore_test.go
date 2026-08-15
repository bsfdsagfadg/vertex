package nodestore

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/db"
)

// setupDB 用临时文件初始化真实 SQLite（禁止 mock SQL），测试结束自动关闭。
func setupDB(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "nodestore-test.db")
	if err := db.InitDB(path); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(db.CloseDB)
}

func TestSaveNodesFullReplaceSemantics(t *testing.T) {
	setupDB(t)

	// 首次保存 3 行
	rows := []NodeRow{
		{RawURI: "uri1", Type: "vless", Name: "n1", Disabled: false},
		{RawURI: "uri2", Type: "vmess", Name: "n2", Disabled: true},
		{RawURI: "uri3", Type: "ss", Name: "n3", Disabled: false},
	}
	if err := SaveNodes(db.GlobalDB, "nodes", rows); err != nil {
		t.Fatalf("SaveNodes: %v", err)
	}

	// 全量替换为 2 行：stale 行（uri3）必须被清除
	if err := SaveNodes(db.GlobalDB, "nodes", rows[:2]); err != nil {
		t.Fatalf("SaveNodes replace: %v", err)
	}
	var count int
	if err := db.GlobalDB.QueryRow("SELECT COUNT(*) FROM nodes").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Fatalf("全量替换后行数=%d, want 2（stale 行应清除）", count)
	}
	// 字段保留
	var typ, name string
	var disabled bool
	if err := db.GlobalDB.QueryRow("SELECT type, name, disabled FROM nodes WHERE raw_uri = 'uri2'").Scan(&typ, &name, &disabled); err != nil {
		t.Fatalf("scan uri2: %v", err)
	}
	if typ != "vmess" || name != "n2" || !disabled {
		t.Errorf("uri2 字段丢失: type=%q name=%q disabled=%v", typ, name, disabled)
	}
}

func TestSaveHealthAppendOnly(t *testing.T) {
	setupDB(t)

	hs := []HealthRow{
		{RawURI: "uri1", SuccessCount: 3, FailCount: 1, CooldownUntil: 100},
		{RawURI: "uri2", FailCount: 5, LastTestError: "timeout"},
	}
	if err := SaveHealth(db.GlobalDB, "node_health", hs); err != nil {
		t.Fatalf("SaveHealth: %v", err)
	}
	// 追加式替换：只写 1 行时，另一行必须保留（不得 DELETE）
	if err := SaveHealth(db.GlobalDB, "node_health", hs[:1]); err != nil {
		t.Fatalf("SaveHealth append: %v", err)
	}
	var count int
	if err := db.GlobalDB.QueryRow("SELECT COUNT(*) FROM node_health").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Fatalf("追加式保存后行数=%d, want 2（不得删多余行）", count)
	}
	// 同 URI 再次写入为 REPLACE
	hs2 := append([]HealthRow(nil), hs[0])
	hs2[0].SuccessCount = 9
	if err := SaveHealth(db.GlobalDB, "node_health", hs2); err != nil {
		t.Fatalf("SaveHealth replace: %v", err)
	}
	var sc int
	if err := db.GlobalDB.QueryRow("SELECT success_count FROM node_health WHERE raw_uri = 'uri1'").Scan(&sc); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if sc != 9 {
		t.Errorf("同 URI 应 REPLACE, success_count=%d, want 9", sc)
	}
}

func TestLoadRoundTrip(t *testing.T) {
	setupDB(t)

	rows := []NodeRow{
		{RawURI: "uri1", Type: "trojan", Name: "t1", Disabled: true},
		{RawURI: "uri2", Type: "socks5", Name: "s1", Disabled: false},
	}
	if err := SaveNodes(db.GlobalDB, "nodes", rows); err != nil {
		t.Fatalf("SaveNodes: %v", err)
	}
	hs := []HealthRow{{RawURI: "uri1", SuccessCount: 7, LastTestMs: 12.5, CooldownUntil: 999}}
	if err := SaveHealth(db.GlobalDB, "node_health", hs); err != nil {
		t.Fatalf("SaveHealth: %v", err)
	}

	var gotNodes []NodeRow
	if err := LoadNodesFull(db.GlobalDB, "nodes", func(rawURI, typ, name string, disabled bool) {
		gotNodes = append(gotNodes, NodeRow{RawURI: rawURI, Type: typ, Name: name, Disabled: disabled})
	}); err != nil {
		t.Fatalf("LoadNodesFull: %v", err)
	}
	if len(gotNodes) != 2 {
		t.Fatalf("节点往返数量=%d, want 2", len(gotNodes))
	}
	byURI := map[string]NodeRow{}
	for _, r := range gotNodes {
		byURI[r.RawURI] = r
	}
	for _, want := range rows {
		got := byURI[want.RawURI]
		if got != want {
			t.Errorf("节点往返不一致: got %+v, want %+v", got, want)
		}
	}

	gotHealth, err := LoadHealth(db.GlobalDB, "node_health")
	if err != nil {
		t.Fatalf("LoadHealth: %v", err)
	}
	if len(gotHealth) != 1 || gotHealth[0].RawURI != "uri1" ||
		gotHealth[0].SuccessCount != 7 || gotHealth[0].LastTestMs != 12.5 || gotHealth[0].CooldownUntil != 999 {
		t.Errorf("健康度往返不一致: %+v", gotHealth)
	}
}

func TestUpsertDisabled(t *testing.T) {
	setupDB(t)

	if err := SaveNodes(db.GlobalDB, "nodes", []NodeRow{{RawURI: "uri1", Type: "ss", Name: "n1"}}); err != nil {
		t.Fatalf("SaveNodes: %v", err)
	}
	if err := UpsertDisabled(db.GlobalDB, "nodes", "uri1", true); err != nil {
		t.Fatalf("UpsertDisabled: %v", err)
	}
	var disabled bool
	if err := db.GlobalDB.QueryRow("SELECT disabled FROM nodes WHERE raw_uri = 'uri1'").Scan(&disabled); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !disabled {
		t.Error("UpsertDisabled 后 disabled 应为 true")
	}
	// 不存在的 URI 零影响，不报错
	if err := UpsertDisabled(db.GlobalDB, "nodes", "ghost", false); err != nil {
		t.Errorf("不存在 URI 不应报错: %v", err)
	}
}

func TestUpsertHealth(t *testing.T) {
	setupDB(t)

	h := HealthRow{RawURI: "uri1", SuccessCount: 2, FailCount: 3, LastTestError: "refused"}
	if err := UpsertHealth(db.GlobalDB, "node_health", h); err != nil {
		t.Fatalf("UpsertHealth: %v", err)
	}
	h.SuccessCount = 5
	if err := UpsertHealth(db.GlobalDB, "node_health", h); err != nil {
		t.Fatalf("UpsertHealth replace: %v", err)
	}
	var sc int
	if err := db.GlobalDB.QueryRow("SELECT success_count FROM node_health WHERE raw_uri = 'uri1'").Scan(&sc); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if sc != 5 {
		t.Errorf("UpsertHealth 应 REPLACE, success_count=%d, want 5", sc)
	}
}

func TestUpdateDisabledBatch(t *testing.T) {
	setupDB(t)

	if err := SaveNodes(db.GlobalDB, "nodes", []NodeRow{
		{RawURI: "uri1", Name: "n1"},
		{RawURI: "uri2", Name: "n2"},
		{RawURI: "uri3", Name: "n3"},
	}); err != nil {
		t.Fatalf("SaveNodes: %v", err)
	}
	// 重复 URI 幂等
	if err := UpdateDisabledBatch(db.GlobalDB, "nodes", []string{"uri1", "uri1", "uri3"}, true); err != nil {
		t.Fatalf("UpdateDisabledBatch: %v", err)
	}
	var count int
	if err := db.GlobalDB.QueryRow("SELECT COUNT(*) FROM nodes WHERE disabled = 1").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("批量禁用数量=%d, want 2", count)
	}
	if err := UpdateDisabledBatch(db.GlobalDB, "nodes", []string{"uri1"}, false); err != nil {
		t.Fatalf("UpdateDisabledBatch enable: %v", err)
	}
	var d int
	if err := db.GlobalDB.QueryRow("SELECT disabled FROM nodes WHERE raw_uri = 'uri1'").Scan(&d); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if d != 0 {
		t.Errorf("uri1 disabled=%d, want 0", d)
	}
}

func TestUpdateDisabledBatchDBFailure(t *testing.T) {
	d, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "broken.db"))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := UpdateDisabledBatch(d, "nodes", []string{"uri1"}, true); err == nil {
		t.Error("关闭的数据库应返回错误")
	}
	if err := SaveNodes(d, "nodes", []NodeRow{{RawURI: "uri1"}}); err == nil {
		t.Error("关闭的数据库 SaveNodes 应返回错误")
	}
	if err := SaveHealth(d, "node_health", []HealthRow{{RawURI: "uri1"}}); err == nil {
		t.Error("关闭的数据库 SaveHealth 应返回错误")
	}
}

func TestNilDB(t *testing.T) {
	// db 为 nil：全部静默跳过，不 panic、不报错
	if err := LoadNodesFull(nil, "nodes", func(_, _, _ string, _ bool) {}); err != nil {
		t.Errorf("LoadNodesFull(nil) = %v, want nil", err)
	}
	if hs, err := LoadHealth(nil, "node_health"); err != nil || hs != nil {
		t.Errorf("LoadHealth(nil) = %v, %v, want nil", hs, err)
	}
	if err := SaveNodes(nil, "nodes", nil); err != nil {
		t.Errorf("SaveNodes(nil) = %v, want nil", err)
	}
	if err := SaveHealth(nil, "node_health", nil); err != nil {
		t.Errorf("SaveHealth(nil) = %v, want nil", err)
	}
	if err := UpsertDisabled(nil, "nodes", "uri1", true); err != nil {
		t.Errorf("UpsertDisabled(nil) = %v, want nil", err)
	}
	if err := UpsertHealth(nil, "node_health", HealthRow{RawURI: "uri1"}); err != nil {
		t.Errorf("UpsertHealth(nil) = %v, want nil", err)
	}
	if err := UpdateDisabledBatch(nil, "nodes", []string{"uri1"}, true); err != nil {
		t.Errorf("UpdateDisabledBatch(nil) = %v, want nil", err)
	}
}

func TestPruneHealthKeys(t *testing.T) {
	health := map[string]bool{"a": true, "b": true, "c": true}
	node := map[string]bool{"a": true, "c": true}
	PruneHealthKeys(health, node)
	if len(health) != 2 || !health["a"] || !health["c"] || health["b"] {
		t.Errorf("PruneHealthKeys 结果错误: %v", health)
	}
	// 空节点集 → 全部清空
	health2 := map[string]bool{"x": true}
	PruneHealthKeys(health2, map[string]bool{})
	if len(health2) != 0 {
		t.Errorf("空节点集应清空全部健康键: %v", health2)
	}
}