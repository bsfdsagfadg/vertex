package nodes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

func resetState() {
	mu.Lock()
	defer mu.Unlock()
	nodeList = nil
	healthMap = make(map[string]*NodeHealth)
	loaded = false
	// 彻底清除物理磁盘缓存，防止测试间的数据污染
	_ = os.Remove(filepath.Join(config.ConfigDir(), "nodes.json"))
	_ = os.Remove(filepath.Join(config.ConfigDir(), "node_health.json"))
	// 清零批量测速进度状态，防止 Terminate/Pause 残留污染后续测试（-count>1 / -shuffle 场景）
	progressMu.Lock()
	globalProgress = TestProgress{}
	testControlCond.Broadcast()
	progressMu.Unlock()
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
	selected := SelectForParallel(2, false)
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

func TestDedupNodeIdentityFuncFallback(t *testing.T) {
	resetState()
	defer resetState()
	defer func() { NodeIdentityFunc = nil }()

	// nil NodeIdentityFunc：回退 rawURI 键，不同片段（不同 RawURI）不去重
	NodeIdentityFunc = nil
	n1 := Node{RawURI: "vless://uuid@example.com:443?security=tls#name1", Name: "node1"} //nolint:exhaustruct
	n2 := Node{RawURI: "vless://uuid@example.com:443?security=tls#name2", Name: "node2"} //nolint:exhaustruct
	MergeNodes([]Node{n1, n2})
	if removed := DedupNodes(); removed != 0 {
		t.Errorf("nil NodeIdentityFunc: expected 0 removed (rawURI fallback), got %d", removed)
	}

	resetState()
	// 注册 NodeIdentityFunc：同 server 同凭证不同 RawURI（仅片段不同）→ 同键去重
	NodeIdentityFunc = func(rawURI string) (string, bool) {
		if idx := strings.Index(rawURI, "#"); idx != -1 {
			return rawURI[:idx], true
		}
		return rawURI, true
	}
	MergeNodes([]Node{n1, n2})
	if removed := DedupNodes(); removed != 1 {
		t.Errorf("registered NodeIdentityFunc: expected 1 removed, got %d", removed)
	}
	result := LoadNodes()
	if len(result) != 1 {
		t.Errorf("expected 1 node after dedup, got %d", len(result))
	}

	resetState()
	// 解析失败（ok=false）→ 回退 rawURI 键，不去重
	NodeIdentityFunc = func(rawURI string) (string, bool) {
		return rawURI, false
	}
	MergeNodes([]Node{n1, n2})
	if removed := DedupNodes(); removed != 0 {
		t.Errorf("ok=false identity: expected 0 removed (fallback), got %d", removed)
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

func TestDedupNodesSemantic(t *testing.T) {
	resetState()
	defer resetState()
	defer func() { NodeIdentityFunc = nil }()

	// 注册注入函数模拟"同身份不同 RawURI"（api 层经 nodeIdentityFromIR 实现）
	NodeIdentityFunc = func(rawURI string) (string, bool) {
		if idx := strings.Index(rawURI, "#"); idx != -1 {
			return rawURI[:idx], true
		}
		return rawURI, true
	}

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
