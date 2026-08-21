package entrypool

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/infra/db"
)

// setupEntryDBMgr 用临时文件初始化真实 SQLite（禁止 mock SQL）并构造绑定该库的实例。
func setupEntryDBMgr(t *testing.T) (*EntryManager, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "entry-test.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return NewEntryManager(database, nil, EntryHooks{}), database
}

// newMemEntryMgr 构造无库内存实例。
func newMemEntryMgr() *EntryManager { return NewEntryManager(nil, nil, EntryHooks{}) }

// seedUnsafe 直接预置内存态（模拟已装载状态，绕过 DB 装载）。
func (m *EntryManager) seedUnsafe(nodes []Node, health map[string]*NodeHealth) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entryList = nodes
	if health != nil {
		m.entryHealthMap = health
	}
	m.entryLoaded = true
}

// openBrokenDB 返回一个已关闭连接的库句柄（触发 Begin 失败路径）。
func openBrokenDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "broken.db"))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return d
}

func TestEntryNodesPersistenceRoundTrip(t *testing.T) {
	mgr, database := setupEntryDBMgr(t)

	seeds := []Node{
		{RawURI: "socks5://a@x.com:1080#e1", Type: "socks5", Name: "e1", Disabled: false},
		{RawURI: "http://b@y.com:8080#e2", Type: "http", Name: "e2", Disabled: true},
		{RawURI: "socks5://c@z.com:1081#e3", Type: "socks5", Name: "e3", Disabled: false},
	}
	mgr.MergeNodes(seeds)

	// 模拟重启：以同一数据库构造全新实例重新加载
	reborn := NewEntryManager(database, nil, EntryHooks{})
	got := reborn.LoadNodes()
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

func TestLoadEntryNodesReturnsIsolatedCopy(t *testing.T) {
	mgr := newMemEntryMgr()

	mgr.MergeNodes([]Node{{RawURI: "uri1", Name: "node1"}, {RawURI: "uri2", Name: "node2"}})
	got := mgr.LoadNodes()
	got[0].Disabled = true
	got[0].RawURI = "mutated"
	// 外部修改拷贝不得影响内部状态
	internal := mgr.LoadNodes()
	if len(internal) != 2 || internal[0].Disabled || internal[0].RawURI != "uri1" {
		t.Errorf("LoadEntryNodes 返回拷贝被外部修改污染内部状态: %+v", internal)
	}
}

func TestBatchUpdateEntryNodesDisabledPersistence(t *testing.T) {
	mgr, database := setupEntryDBMgr(t)

	mgr.MergeNodes([]Node{
		{RawURI: "uri1", Name: "node1"},
		{RawURI: "uri2", Name: "node2"},
		{RawURI: "uri3", Name: "node3"},
	})

	// 重复 URI 也应幂等（targets 去重）
	mgr.BatchUpdateNodesDisabled([]string{"uri1", "uri1", "uri3"}, true)

	// 内存一致性
	for _, n := range mgr.LoadNodes() {
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
	if err := database.QueryRow("SELECT COUNT(*) FROM entry_nodes WHERE disabled = 1").Scan(&count); err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if count != 2 {
		t.Errorf("DB disabled 数量=%d, want 2", count)
	}

	// 反向启用：CooldownUntil 应清零并持久化（提交成功后执行）
	mgr.mu.Lock()
	mgr.entryHealthMap["uri1"] = &NodeHealth{CooldownUntil: 999} //nolint:exhaustruct
	mgr.mu.Unlock()
	mgr.BatchUpdateNodesDisabled([]string{"uri1"}, false)

	var d1 int
	if err := database.QueryRow("SELECT disabled FROM entry_nodes WHERE raw_uri = 'uri1'").Scan(&d1); err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if d1 != 0 {
		t.Errorf("DB uri1 disabled=%d, want 0", d1)
	}
	var cd int64
	if err := database.QueryRow("SELECT cooldown_until FROM entry_node_health WHERE raw_uri = 'uri1'").Scan(&cd); err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if cd != 0 {
		t.Errorf("DB uri1 cooldown_until=%d, want 0", cd)
	}
	mgr.mu.RLock()
	got := mgr.entryList[0]
	h := mgr.entryHealthMap["uri1"]
	mgr.mu.RUnlock()
	if got.Disabled {
		t.Error("内存 uri1 应已启用")
	}
	if h.CooldownUntil != 0 {
		t.Errorf("内存 uri1 CooldownUntil=%d, want 0", h.CooldownUntil)
	}
}

func TestBatchUpdateEntryNodesDisabledDBFailure(t *testing.T) {
	// 关闭连接的库句柄 → Begin 失败，内存（含冷却）不得被修改
	mgr := NewEntryManager(openBrokenDB(t), nil, EntryHooks{})

	mgr.seedUnsafe(
		[]Node{{RawURI: "uri1", Name: "node1"}},              //nolint:exhaustruct
		map[string]*NodeHealth{"uri1": {CooldownUntil: 123}}, //nolint:exhaustruct
	)

	mgr.BatchUpdateNodesDisabled([]string{"uri1"}, false)

	mgr.mu.RLock()
	got := mgr.entryList[0]
	cd := mgr.entryHealthMap["uri1"].CooldownUntil
	mgr.mu.RUnlock()
	if got.Disabled {
		t.Error("DB 失败时内存 disabled 不得被修改")
	}
	if cd != 123 {
		t.Errorf("DB 失败时 CooldownUntil 不得被清零, got %d", cd)
	}
}
