package entrynodes

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/db"
)

// setupEntryTestDB 用临时文件初始化真实 SQLite 并挂载到 db.GlobalDB，测试结束自动关闭。
func setupEntryTestDB(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "entry-test.db")
	if err := db.InitDB(path); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(db.CloseDB)
}

func TestEntryNodesPersistenceRoundTrip(t *testing.T) {
	setupEntryTestDB(t)
	ResetEntryState()
	defer ResetEntryState()

	seeds := []Node{
		{RawURI: "socks5://a@x.com:1080#e1", Type: "socks5", Name: "e1", Disabled: false},
		{RawURI: "http://b@y.com:8080#e2", Type: "http", Name: "e2", Disabled: true},
		{RawURI: "socks5://c@z.com:1081#e3", Type: "socks5", Name: "e3", Disabled: false},
	}
	MergeEntryNodes(seeds)

	// 模拟重启：清空内存与加载标志，重新从数据库加载
	ResetEntryState()
	got := LoadEntryNodes()
	if len(got) != len(seeds) {
		t.Fatalf("roundtrip 数量=%d, want %d", len(got), len(seeds))
	}
	for i, want := range seeds {
		n := got[i]
		if n.RawURI != want.RawURI || n.Type != want.Type || n.Name != want.Name || n.Disabled != want.Disabled {
			t.Errorf("roundtrip[%d] 字段不一致: got %+v, want %+v", i, n, want)
		}
	}
}

func TestBatchUpdateEntryNodesDisabledPersistence(t *testing.T) {
	setupEntryTestDB(t)
	ResetEntryState()
	defer ResetEntryState()

	MergeEntryNodes([]Node{
		{RawURI: "uri1", Name: "node1"},
		{RawURI: "uri2", Name: "node2"},
		{RawURI: "uri3", Name: "node3"},
	})

	// 重复 URI 也应幂等（targets 去重）
	BatchUpdateEntryNodesDisabled([]string{"uri1", "uri1", "uri3"}, true)

	// 内存一致性
	for _, n := range LoadEntryNodes() {
		switch n.RawURI {
		case "uri1", "uri3":
			if !n.Disabled {
				t.Errorf("%s 应被禁用", n.RawURI)
			}
		case "uri2":
			if n.Disabled {
				t.Error("uri2 不应被禁用")
			}
		}
	}
	// 数据库一致性：直接查询
	var count int
	if err := db.GlobalDB.QueryRow("SELECT COUNT(*) FROM entry_nodes WHERE disabled = 1").Scan(&count); err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if count != 2 {
		t.Errorf("DB disabled 数量=%d, want 2", count)
	}

	// 反向启用：CooldownUntil 应清零并持久化（提交成功后执行）
	mu.Lock()
	entryHealthMap["uri1"] = &NodeHealth{CooldownUntil: 999} //nolint:exhaustruct
	mu.Unlock()
	BatchUpdateEntryNodesDisabled([]string{"uri1"}, false)

	var d1 int
	if err := db.GlobalDB.QueryRow("SELECT disabled FROM entry_nodes WHERE raw_uri = 'uri1'").Scan(&d1); err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if d1 != 0 {
		t.Errorf("DB uri1 disabled=%d, want 0", d1)
	}
	var cd int64
	if err := db.GlobalDB.QueryRow("SELECT cooldown_until FROM entry_node_health WHERE raw_uri = 'uri1'").Scan(&cd); err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if cd != 0 {
		t.Errorf("DB uri1 cooldown_until=%d, want 0", cd)
	}
	mu.RLock()
	got := entryList[0]
	h := entryHealthMap["uri1"]
	mu.RUnlock()
	if got.Disabled {
		t.Error("内存 uri1 应已启用")
	}
	if h.CooldownUntil != 0 {
		t.Errorf("内存 uri1 CooldownUntil=%d, want 0", h.CooldownUntil)
	}
}

func TestBatchUpdateEntryNodesDisabledDBFailure(t *testing.T) {
	ResetEntryState()
	defer ResetEntryState()

	// 关闭连接但 GlobalDB 仍非 nil → Begin 失败，内存（含冷却）不得被修改
	d, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "broken.db"))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	db.GlobalDB = d
	t.Cleanup(func() { db.GlobalDB = nil })

	mu.Lock()
	entryList = []Node{{RawURI: "uri1", Name: "node1"}} //nolint:exhaustruct
	entryHealthMap = map[string]*NodeHealth{"uri1": {CooldownUntil: 123}}
	entryLoaded = true
	mu.Unlock()

	BatchUpdateEntryNodesDisabled([]string{"uri1"}, false)

	mu.RLock()
	got := entryList[0]
	cd := entryHealthMap["uri1"].CooldownUntil
	mu.RUnlock()
	if got.Disabled {
		t.Error("DB 失败时内存 disabled 不得被修改")
	}
	if cd != 123 {
		t.Errorf("DB 失败时 CooldownUntil 不得被清零, got %d", cd)
	}
}