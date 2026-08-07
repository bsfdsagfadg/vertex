package nodes

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/db"
)

func resetState() {
	mu.Lock()
	defer mu.Unlock()
	nodeList = nil
	healthMap = make(map[string]*NodeHealth)
	nodeSources = make(map[string]map[NodeSource]struct{})
	globalStickyPool = NewStickyNodePool()
	loaded = false
	// 彻底清除物理磁盘缓存，防止测试间的数据污染
	_ = os.Remove(filepath.Join(config.ConfigDir(), "nodes.json"))
	_ = os.Remove(filepath.Join(config.ConfigDir(), "node_health.json"))
}

func TestBatchTestProgressRejectsDuplicateAndKeepsTermination(t *testing.T) {
	FinishTestProgress()
	if !StartTestProgress(10) {
		t.Fatal("首次批量测试应成功占用状态")
	}
	if StartTestProgress(20) {
		t.Fatal("运行中应拒绝重复批量测试")
	}
	if !IsTestRunning() {
		t.Fatal("批量测试应处于运行状态")
	}
	TerminateTestProgress()
	FinishTestProgress()
	progress := GetTestProgress()
	if progress.Running || !progress.Terminated || progress.CurrentNode != "已终止" {
		t.Fatalf("终止状态未保留: %+v", progress)
	}
}

func TestNodesLifecycle(t *testing.T) {
	// Setup a temporary directory for config
	_ = t.TempDir()

	// Temporarily override the behavior of fileDir if possible,
	// but since it's hardcoded to os.Executable() or "config",
	// we will create "config" in the current directory, or just mock what we can.
	// Since fileDir is fixed and we don't want to pollute actual config,
	// let's create a symlink or temporarily mock os.Executable if needed.
	// For simplicity, we just test the in-memory aspects mostly, and let it write to ./config
	// Note: In a real test environment, we should make fileDir overridable.
	// Update: fileDir() 已经被移除并重构为了 config.ConfigDir()，现在测试环境可以通过 VPROXY_CONFIG 环境变量轻松覆盖配置路径，从而避免污染真实配置。

	// We'll just test the logic that doesn't strictly depend on file system or clean up

	resetState()

	n1 := Node{RawURI: "uri1", Name: "node1"} //nolint:exhaustruct
	n2 := Node{RawURI: "uri2", Name: "node2"} //nolint:exhaustruct

	MergeNodes([]Node{n1, n2})

	nodes := LoadNodes()
	if len(nodes) != 2 {
		t.Fatalf("Expected 2 nodes, got %d", len(nodes))
	}

	// Test Dedup
	MergeNodes([]Node{n1}) // Add duplicate
	if len(LoadNodes()) != 2 {
		t.Fatalf("Expected 2 nodes after merging duplicate, got %d", len(LoadNodes()))
	}

	removed := DedupNodes()
	if removed != 0 {
		t.Errorf("Expected 0 removed during dedup, got %d", removed)
	}

	// Test RecordTest
	RecordTest("uri1", true, 10.5, "")
	health := LoadHealth()
	hUri1 := health["uri1"]
	if hUri1 == nil || hUri1.SuccessCount != 1 {
		t.Errorf("Expected success count 1, got %v", hUri1)
	}

	RecordTest("uri1", false, 0, "timeout")
	hUri1 = health["uri1"]
	if hUri1 == nil || hUri1.FailCount != 1 {
		t.Errorf("Expected fail count 1, got %v", hUri1)
	}

	// Test BatchUpdateNodesDisabled
	BatchUpdateNodesDisabled([]string{"uri1"}, true)
	for _, n := range LoadNodes() {
		if n.RawURI == "uri1" && !n.Disabled {
			t.Errorf("Expected uri1 to be disabled")
		}
	}

	// Test SelectForParallel (uri1 is disabled, should only return uri2 if available)
	selected := SelectForParallel(2, 80, false, false)
	if len(selected) != 1 || selected[0].RawURI != "uri2" {
		t.Errorf("Expected only uri2 to be selected, got %v", selected)
	}

	// Test DeleteDisabled
	removed = DeleteDisabled()
	if removed != 1 {
		t.Errorf("Expected 1 node removed, got %d", removed)
	}
	if len(LoadNodes()) != 1 {
		t.Errorf("Expected 1 node remaining, got %d", len(LoadNodes()))
	}

	// Test DeleteNode
	DeleteNode("uri2")
	if len(LoadNodes()) != 0 {
		t.Errorf("Expected 0 nodes, got %d", len(LoadNodes()))
	}

	// Cleanup state
	resetState()
	_ = os.RemoveAll(filepath.Join(config.ConfigDir(), "nodes.json"))
	_ = os.RemoveAll(filepath.Join(config.ConfigDir(), "node_health.json"))
}

func TestParseNodeIdentity(t *testing.T) {
	tests := []struct { //nolint:govet
		name     string
		uri      string
		wantOK   bool
		wantS    string
		wantUI   string
		wantHost string
		wantPort int
	}{
		{"vmess", "vmess://eyJhZGQiOiIxMjcuMC4wLjEiLCJwb3J0Ijo4ODg4LCJpZCI6InV1aWQtdmFsdWUiLCJwcyI6InRlc3QifQ==", true, "vmess", "uuid-value", "127.0.0.1", 8888},
		{"ss", "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@127.0.0.1:8888", true, "ss", "aes-256-gcm:password", "127.0.0.1", 8888},
		{"vless", "vless://uuid@example.com:443", true, "vless", "uuid", "example.com", 443},
		{"trojan", "trojan://password@example.com:8443", true, "trojan", "password", "example.com", 8443},
		{"invalid", "not-a-uri://", false, "", "", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, ui, host, port, ok := parseNodeIdentity(tt.uri)
			if ok != tt.wantOK {
				t.Errorf("parseNodeIdentity() ok = %v, want %v", ok, tt.wantOK)
			}
			if s != tt.wantS {
				t.Errorf("parseNodeIdentity() scheme = %q, want %q", s, tt.wantS)
			}
			if ui != tt.wantUI {
				t.Errorf("parseNodeIdentity() userinfo = %q, want %q", ui, tt.wantUI)
			}
			if host != tt.wantHost {
				t.Errorf("parseNodeIdentity() host = %q, want %q", host, tt.wantHost)
			}
			if port != tt.wantPort {
				t.Errorf("parseNodeIdentity() port = %d, want %d", port, tt.wantPort)
			}
		})
	}
}

func TestUpdateNodeTestResult(t *testing.T) {
	resetState()
	defer resetState()

	// Setup: one enabled node
	n1 := Node{RawURI: "uri1", Name: "node1"} //nolint:exhaustruct
	MergeNodes([]Node{n1})

	// Test: fail the node
	UpdateNodeTestResult("uri1", false, 100, "timeout")
	health := LoadHealth()
	h1 := health["uri1"]
	if h1 == nil || h1.ConsecutiveFailures != 1 {
		t.Errorf("Expected 1 consecutive failure")
	}
	nodes := LoadNodes()
	if len(nodes) != 1 || nodes[0].Disabled {
		t.Errorf("Expected node1 to NOT be disabled after failed test (cooldown replaces disable)")
	}
	if h1 == nil || h1.CooldownUntil == 0 {
		t.Errorf("Expected cooldown to be set after failed test")
	}

	// Test: succeed the node
	UpdateNodeTestResult("uri1", true, 50, "")
	health = LoadHealth()
	h2 := health["uri1"]
	if h2 == nil || h2.SuccessCount != 1 {
		t.Errorf("Expected 1 success")
	}
	if h2 == nil || h2.CooldownUntil != 0 {
		t.Errorf("Expected cooldown to be cleared after success")
	}
	nodes = LoadNodes()
	if len(nodes) == 0 || nodes[0].Disabled {
		t.Errorf("Expected node1 to be enabled after success")
	}
}

func TestRecordTestTransportFailureDoesNotDisableNodeInDB(t *testing.T) {
	db.CloseDB()
	resetState()
	if err := db.InitDB(filepath.Join(t.TempDir(), "data.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		resetState()
		db.CloseDB()
	})

	MergeNodes([]Node{{RawURI: "uri1", Name: "node1"}})
	RecordTest("uri1", false, 0, "dial tcp: i/o timeout")

	nodes := LoadNodes()
	if len(nodes) != 1 || nodes[0].Disabled {
		t.Fatalf("临时网络错误不应禁用内存节点: %+v", nodes)
	}
	var disabled bool
	if err := db.GlobalDB.QueryRow("SELECT disabled FROM nodes WHERE raw_uri = ?", "uri1").Scan(&disabled); err != nil {
		t.Fatal(err)
	}
	if disabled {
		t.Fatal("临时网络错误不应将数据库节点标记为 disabled")
	}

	// 模拟进程重启后从数据库重新加载。
	resetState()
	nodes = LoadNodes()
	if len(nodes) != 1 || nodes[0].Disabled {
		t.Fatalf("重载后节点应保持启用: %+v", nodes)
	}
}

func TestMergeNodesPrunesHealthMap(t *testing.T) {
	resetState()
	defer resetState()

	n1 := Node{RawURI: "uri1", Name: "node1"} //nolint:exhaustruct
	n2 := Node{RawURI: "uri2", Name: "node2"} //nolint:exhaustruct

	MergeNodes([]Node{n1, n2})

	RecordTest("uri1", true, 10, "")
	RecordTest("uri2", false, 0, "timeout")
	health := LoadHealth()
	if len(health) != 2 {
		t.Fatalf("Expected 2 health entries, got %d", len(health))
	}

	DeleteNode("uri2")

	mu.Lock()
	healthMap["orphan-uri"] = &NodeHealth{SuccessCount: 99} //nolint:exhaustruct
	mu.Unlock()

	MergeNodes([]Node{n1})
	health = LoadHealth()
	if len(health) != 1 {
		t.Fatalf("Expected 1 health entry after MergeNodes prunes orphan, got %d", len(health))
	}
	if health["orphan-uri"] != nil {
		t.Errorf("Expected orphan-uri health entry to be pruned")
	}
	if health["uri1"] == nil {
		t.Errorf("Expected uri1 health entry to survive")
	}

	RecordTest("uri1", false, 0, "timeout")
	health = LoadHealth()
	if health["uri1"] == nil || health["uri1"].FailCount != 1 {
		t.Errorf("Expected RecordTest to still work after pruning, got %v", health["uri1"])
	}
}

func TestEnableNode(t *testing.T) {
	resetState()
	defer resetState()

	n1 := Node{RawURI: "uri1", Name: "node1", Disabled: true} //nolint:exhaustruct
	MergeNodes([]Node{n1})

	// Also set cooldown
	RecordTest("uri1", false, 0, "timeout")

	ok := EnableNode("uri1")
	if !ok {
		t.Errorf("Expected EnableNode to return true")
	}
	nodes := LoadNodes()
	if len(nodes) != 1 || nodes[0].Disabled {
		t.Errorf("Expected node1 to be enabled")
	}
	health := LoadHealth()
	if health["uri1"] != nil && health["uri1"].CooldownUntil != 0 {
		t.Errorf("Expected cooldown to be cleared")
	}

	// Test enabling non-existent node
	ok = EnableNode("nonexistent")
	if ok {
		t.Errorf("Expected EnableNode to return false for nonexistent node")
	}
}

func TestDedupNodesSemantic(t *testing.T) {
	resetState()
	defer resetState()

	// Two nodes with same identity but different raw URIs (different names/fragments)
	n1 := Node{RawURI: "vless://uuid@example.com:443?security=tls#name1", Name: "node1"}
	n2 := Node{RawURI: "vless://uuid@example.com:443?security=tls#name2", Name: "node2"}
	MergeNodes([]Node{n1, n2})

	removed := DedupNodes()
	if removed != 1 {
		t.Errorf("Expected 1 removed during semantic dedup, got %d", removed)
	}
	result := LoadNodes()
	if len(result) != 1 {
		t.Errorf("Expected 1 node after dedup, got %d", len(result))
	}
}

func TestSubscriptionAdoptsLegacySource(t *testing.T) {
	resetState()
	defer resetState()

	node := Node{RawURI: "vless://id@example.com:443?security=tls#name", Name: "name"}
	if err := UpsertNodesWithSource([]Node{node}, SourceLegacy, ""); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceSubscriptionNodes("sub-a", []Node{node}, false); err != nil {
		t.Fatal(err)
	}
	sources := GetNodeSources(node.RawURI)
	if len(sources) != 1 || sources[0] != (NodeSource{Type: SourceSubscription, ID: "sub-a"}) {
		t.Fatalf("legacy 节点应由订阅接管: %+v", sources)
	}
}

func TestManualSourceRemainsIndependentFromSubscriptions(t *testing.T) {
	resetState()
	defer resetState()

	node := Node{RawURI: "vless://id@example.com:443?security=tls#name", Name: "name"}
	if err := ImportManualNodes([]Node{node}, false); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceSubscriptionNodes("sub-a", []Node{node}, false); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceSubscriptionNodes("sub-b", []Node{node}, false); err != nil {
		t.Fatal(err)
	}
	if err := RemoveSubscriptionSource("sub-a", true); err != nil {
		t.Fatal(err)
	}
	sources := GetNodeSources(node.RawURI)
	want := []NodeSource{{Type: SourceManual}, {Type: SourceSubscription, ID: "sub-b"}}
	if len(sources) != len(want) || sources[0] != want[0] || sources[1] != want[1] {
		t.Fatalf("manual 和其他订阅来源必须保留: got %+v want %+v", sources, want)
	}
}

func TestSubscriptionRefreshCanAdoptManualMetadata(t *testing.T) {
	resetState()
	defer resetState()

	rawURI := "vless://id@example.com:443?security=tls#name"
	if err := ReplaceSubscriptionNodes("sub-a", []Node{{RawURI: rawURI, Name: "old", Type: "vless", Disabled: true}}, false); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceSubscriptionNodes("sub-a", []Node{{RawURI: rawURI, Name: "new", Type: "vless"}}, false); err != nil {
		t.Fatal(err)
	}
	got := LoadNodes()
	if len(got) != 1 || got[0].Name != "new" || got[0].Disabled {
		t.Fatalf("subscription refresh must update owned metadata: %+v", got)
	}

	manual := Node{RawURI: rawURI, Name: "manual", Type: "vless", Disabled: true}
	if err := ImportManualNodes([]Node{manual}, false); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceSubscriptionNodes("sub-a", []Node{{RawURI: rawURI, Name: "subscription", Type: "vless"}}, false); err != nil {
		t.Fatal(err)
	}
	got = LoadNodes()
	if len(got) != 1 || got[0].Name != "manual" || !got[0].Disabled {
		t.Fatalf("subscription must not overwrite manual metadata: %+v", got)
	}
	if err := ReplaceSubscriptionNodes("sub-a", []Node{{RawURI: rawURI, Name: "subscription", Type: "vless"}}, true); err != nil {
		t.Fatal(err)
	}
	got = LoadNodes()
	if len(got) != 1 || got[0].Name != "subscription" || got[0].Disabled {
		t.Fatalf("authorized adoption must use subscription metadata: %+v", got)
	}
	sources := GetNodeSources(rawURI)
	if len(sources) != 1 || sources[0] != (NodeSource{Type: SourceSubscription, ID: "sub-a"}) {
		t.Fatalf("authorized adoption must remove manual ownership: %+v", sources)
	}
}

func TestUpsertFailureRestoresPrunedHealth(t *testing.T) {
	db.CloseDB()
	resetState()
	defer func() {
		db.CloseDB()
		resetState()
	}()
	if err := db.InitDB(filepath.Join(t.TempDir(), "data.db")); err != nil {
		t.Fatal(err)
	}
	if err := UpsertNodesWithSource([]Node{{RawURI: "uri1", Name: "one"}}, SourceManual, ""); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	healthMap["orphan"] = &NodeHealth{SuccessCount: 7}
	mu.Unlock()
	if err := db.GlobalDB.Close(); err != nil {
		t.Fatal(err)
	}
	if err := UpsertNodesWithSource([]Node{{RawURI: "uri2", Name: "two"}}, SourceManual, ""); err == nil {
		t.Fatal("closed database must make upsert fail")
	}
	mu.Lock()
	defer mu.Unlock()
	if healthMap["orphan"] == nil || healthMap["orphan"].SuccessCount != 7 {
		t.Fatalf("failed upsert must restore pruned health: %+v", healthMap["orphan"])
	}
}

func TestKeepingDeletedSubscriptionNodesConvertsThemToManual(t *testing.T) {
	resetState()
	defer resetState()

	node := Node{RawURI: "vless://id@example.com:443?security=tls#name", Name: "name"}
	if err := ReplaceSubscriptionNodes("sub-a", []Node{node}, false); err != nil {
		t.Fatal(err)
	}
	if err := RemoveSubscriptionSource("sub-a", false); err != nil {
		t.Fatal(err)
	}
	sources := GetNodeSources(node.RawURI)
	if len(sources) != 1 || sources[0] != (NodeSource{Type: SourceManual}) {
		t.Fatalf("保留的订阅节点应转为 manual: %+v", sources)
	}
}

func TestDedupNodesPreservesConnectionParametersAndMergesSources(t *testing.T) {
	resetState()
	defer resetState()

	first := Node{RawURI: "vless://id@example.com:443?security=tls&type=ws#one", Name: "one"}
	second := Node{RawURI: "vless://id@example.com:443?security=reality&type=grpc#two", Name: "two"}
	if err := UpsertNodesWithSource([]Node{first}, SourceManual, ""); err != nil {
		t.Fatal(err)
	}
	if err := UpsertNodesWithSource([]Node{second}, SourceSubscription, "sub-a"); err != nil {
		t.Fatal(err)
	}
	if removed := DedupNodes(); removed != 0 {
		t.Fatalf("连接参数不同的节点不得自动合并, removed=%d", removed)
	}

	sameAsFirst := Node{RawURI: "vless://id@example.com:443?security=tls&type=ws#renamed", Name: "renamed"}
	if err := UpsertNodesWithSource([]Node{sameAsFirst}, SourceSubscription, "sub-b"); err != nil {
		t.Fatal(err)
	}
	preview := PreviewDedupNodes()
	if preview.Groups != 1 || preview.DuplicateCount != 1 {
		t.Fatalf("去重预览不正确: %+v", preview)
	}
	if removed := DedupNodes(); removed != 1 {
		t.Fatalf("仅名称不同的节点应合并, removed=%d", removed)
	}
	sources := GetNodeSources(first.RawURI)
	want := []NodeSource{{Type: SourceManual}, {Type: SourceSubscription, ID: "sub-b"}}
	if len(sources) != len(want) || sources[0] != want[0] || sources[1] != want[1] {
		t.Fatalf("去重后来源应取并集: got %+v want %+v", sources, want)
	}
}

func TestSelectForParallelSkipsActiveCooldown(t *testing.T) {
	resetState()
	defer resetState()

	n1 := Node{RawURI: "uri1", Name: "node1"}
	n2 := Node{RawURI: "uri2", Name: "node2"}
	n3 := Node{RawURI: "uri3", Name: "node3"}
	MergeNodes([]Node{n1, n2, n3})

	// Put n1 and n2 in cooldown, leave n3 normal
	RecordTest("uri1", false, 0, "timeout")
	RecordTest("uri2", false, 0, "timeout")

	// Request 3 nodes, active cooldown nodes must not be reused immediately.
	selected := SelectForParallel(3, 80, false, false)
	if len(selected) != 1 || selected[0].RawURI != "uri3" {
		t.Errorf("Expected only healthy uri3 while others cool down, got %+v", selected)
	}
}

func TestCooldownAndSubHealthyStateSurvivesReload(t *testing.T) {
	db.CloseDB()
	resetState()
	if err := db.InitDB(filepath.Join(t.TempDir(), "data.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		resetState()
		db.CloseDB()
	})

	MergeNodes([]Node{
		{RawURI: "limited", Name: "limited"},
		{RawURI: "healthy", Name: "healthy"},
	})
	RecordRateLimit("limited", 60)

	deadline := time.Now().Add(3 * time.Second)
	for {
		var cooldownUntil, last429At, lastSubHealthyAt int64
		var rateLimitCount int
		err := db.GlobalDB.QueryRow(`SELECT cooldown_until, last_429_at, rate_limit_count, last_sub_healthy_at FROM node_health WHERE raw_uri = ?`, "limited").
			Scan(&cooldownUntil, &last429At, &rateLimitCount, &lastSubHealthyAt)
		if err == nil && cooldownUntil > time.Now().Unix() && last429At > 0 && rateLimitCount == 1 && lastSubHealthyAt > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("健康状态未及时持久化: err=%v cooldown=%d last429=%d rate=%d sub=%d", err, cooldownUntil, last429At, rateLimitCount, lastSubHealthyAt)
		}
		time.Sleep(20 * time.Millisecond)
	}

	resetState()
	health := LoadHealth()
	h := health["limited"]
	if h == nil || h.CooldownUntil <= time.Now().Unix() || h.LastSubHealthyAt == 0 || h.Last429At == 0 || h.RateLimitCount != 1 {
		t.Fatalf("重载后的冷却/亚健康状态错误: %+v", h)
	}
	selected := SelectForParallel(2, 80, false, false)
	if len(selected) != 1 || selected[0].RawURI != "healthy" {
		t.Fatalf("重载后活动冷却节点不应进入候选: %+v", selected)
	}

	mu.Lock()
	h.CooldownUntil = time.Now().Unix() - 1
	mu.Unlock()
	selected = SelectForParallel(2, 80, false, false)
	if len(selected) != 2 {
		t.Fatalf("冷却到期后亚健康节点应作为 Tier 2 候选: %+v", selected)
	}
	if tier := getNodeTier(Node{RawURI: "limited"}, h); tier != 2 {
		t.Fatalf("冷却到期不应自动恢复为健康 Tier 1, got Tier %d", tier)
	}
}

func TestSelectForParallelHonorsTopK(t *testing.T) {
	resetState()
	defer resetState()
	MergeNodes([]Node{
		{RawURI: "a", Name: "a"},
		{RawURI: "b", Name: "b"},
		{RawURI: "c", Name: "c"},
	})
	mu.Lock()
	healthMap["a"] = &NodeHealth{LastSelectedAt: 0}
	healthMap["b"] = &NodeHealth{LastSelectedAt: 10}
	healthMap["c"] = &NodeHealth{LastSelectedAt: 20}
	mu.Unlock()
	atomic.StoreUint64(&atomicRoundRobinIndex, 0)

	selected := SelectForParallel(1, 1, false, false)
	if len(selected) != 1 || selected[0].RawURI != "a" {
		t.Fatalf("topK=1 应只在排序第一的候选中选择: %+v", selected)
	}

	mu.Lock()
	healthMap["a"].LastSelectedAt = 0
	healthMap["b"].LastSelectedAt = 10
	healthMap["c"].LastSelectedAt = 20
	mu.Unlock()
	atomic.StoreUint64(&atomicRoundRobinIndex, 0)
	selected = SelectForParallel(1, 3, false, false)
	if len(selected) != 1 || selected[0].RawURI != "b" {
		t.Fatalf("topK=3 应在三个候选间轮询，本轮预期 b: %+v", selected)
	}
}

func TestSelectForParallelHonorsStickyPriority(t *testing.T) {
	resetState()
	defer resetState()
	MergeNodes([]Node{
		{RawURI: "a", Name: "a"},
		{RawURI: "b", Name: "b"},
	})
	globalStickyPool.Add("b")

	selected := SelectForParallel(1, 1, false, false)
	if len(selected) != 1 || selected[0].RawURI != "a" {
		t.Fatalf("关闭 sticky 优先时应保持普通排序: %+v", selected)
	}
	selected = SelectForParallel(1, 1, false, true)
	if len(selected) != 1 || selected[0].RawURI != "b" {
		t.Fatalf("启用 sticky 优先时应优先 sticky 节点: %+v", selected)
	}
}
