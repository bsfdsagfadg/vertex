package nodes

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

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
	selected := SelectForParallel(2, false, false)
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
		t.Errorf("Expected node1 to NOT be disabled after soft failure (sub-healthy replaces disable)")
	}
	if h1 == nil || h1.LastSubHealthyAt == 0 {
		t.Errorf("Expected LastSubHealthyAt to be set after failed test")
	}

	// Test: succeed the node
	UpdateNodeTestResult("uri1", true, 50, "")
	health = LoadHealth()
	h2 := health["uri1"]
	if h2 == nil || h2.SuccessCount != 1 {
		t.Errorf("Expected 1 success")
	}
	if h2 == nil || h2.LastSubHealthyAt != 0 {
		t.Errorf("Expected LastSubHealthyAt to be cleared after success")
	}
	nodes = LoadNodes()
	if len(nodes) == 0 || nodes[0].Disabled {
		t.Errorf("Expected node1 to be enabled after success")
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

func TestSelectForParallel_SubHealthyFallback(t *testing.T) {
	resetState()
	defer resetState()

	n1 := Node{RawURI: "uri1", Name: "node1"}
	n2 := Node{RawURI: "uri2", Name: "node2"}
	n3 := Node{RawURI: "uri3", Name: "node3"}
	MergeNodes([]Node{n1, n2, n3})

	// Put n1 and n2 in sub-healthy via soft failure, leave n3 healthy
	RecordTest("uri1", false, 0, "timeout")
	RecordTest("uri2", false, 0, "timeout")

	// Request 3 nodes: should get n3 (Tier 1) + n1+n2 (Tier 2 fallback)
	selected := SelectForParallel(3, false, false)
	if len(selected) != 3 {
		t.Errorf("Expected 3 selected (1 Tier1 + 2 Tier2), got %d", len(selected))
	}
}

func TestGetNodeTier(t *testing.T) {
	resetState()
	defer resetState()

	// Tier 3: disabled node
	nDisabled := Node{RawURI: "uri-dis", Name: "dis", Disabled: true}
	if tier := getNodeTier(nDisabled, nil); tier != 3 {
		t.Errorf("Expected disabled node → tier 3, got %d", tier)
	}

	// Tier 1: no health entry at all
	nNoHealth := Node{RawURI: "uri-nohealth", Name: "nohealth"}
	if tier := getNodeTier(nNoHealth, nil); tier != 1 {
		t.Errorf("Expected node without health → tier 1, got %d", tier)
	}

	// Tier 1: healthy node with LastSubHealthyAt == 0
	MergeNodes([]Node{{RawURI: "uri-h1", Name: "h1"}})
	RecordTest("uri-h1", true, 50, "")
	h1 := healthMap["uri-h1"]
	if tier := getNodeTier(Node{RawURI: "uri-h1", Name: "h1"}, h1); tier != 1 {
		t.Errorf("Expected healthy node → tier 1, got %d", tier)
	}

	// Tier 2: sub-healthy (LastSubHealthyAt set within 5s)
	RecordTest("uri-h1", false, 0, "timeout")
	h2 := healthMap["uri-h1"]
	if h2.LastSubHealthyAt == 0 {
		t.Fatal("Expected LastSubHealthyAt to be set")
	}
	if tier := getNodeTier(Node{RawURI: "uri-h1", Name: "h1"}, h2); tier != 2 {
		t.Errorf("Expected sub-healthy node → tier 2, got %d", tier)
	}

	// Tier 2 persists regardless of time: set LastSubHealthyAt to 10s ago
	mu.Lock()
	h2.LastSubHealthyAt = time.Now().Unix() - 10
	mu.Unlock()
	if tier := getNodeTier(Node{RawURI: "uri-h1", Name: "h1"}, h2); tier != 2 {
		t.Errorf("Expected sub-healthy node (10s ago) → tier 2, got %d", tier)
	}
}

func TestSelectForParallel_RoundRobin_InFlight(t *testing.T) {
	resetState()
	defer resetState()

	n1 := Node{RawURI: "uri1", Name: "a"}
	n2 := Node{RawURI: "uri2", Name: "b"}
	n3 := Node{RawURI: "uri3", Name: "c"}
	MergeNodes([]Node{n1, n2, n3})

	// All Tier 1, all InFlight=0: round-robin across calls
	atomic.StoreUint64(&atomicRoundRobinIndex, 0)

	got := make(map[string]int)
	for call := 0; call < 6; call++ {
		sel := SelectForParallel(1, false, false)
		if len(sel) != 1 {
			t.Fatalf("call %d: expected 1 selected, got %d", call, len(sel))
		}
		got[sel[0].RawURI]++
	}

	// Over 6 calls with 3 nodes, each should be picked exactly 2 times
	for _, uri := range []string{"uri1", "uri2", "uri3"} {
		if got[uri] != 2 {
			t.Errorf("Expected %s to be picked 2 times, got %d", uri, got[uri])
		}
	}

	// Node with higher InFlight should be deprioritized
	resetState()
	MergeNodes([]Node{n1, n2, n3})
	// Simulate uri1 having InFlight=2, others InFlight=0
	mu.Lock()
	healthMap["uri1"] = &NodeHealth{InFlight: 2}
	healthMap["uri2"] = &NodeHealth{InFlight: 0}
	healthMap["uri3"] = &NodeHealth{InFlight: 0}
	mu.Unlock()

	sel2 := SelectForParallel(1, false, false)
	if len(sel2) != 1 {
		t.Fatalf("expected 1 selected, got %d", len(sel2))
	}
	if sel2[0].RawURI == "uri1" {
		t.Errorf("Expected lower InFlight node to be preferred, but got uri1 (InFlight=2)")
	}
}

func TestSelectForParallel_SubHealthy5sRecovery(t *testing.T) {
	resetState()
	defer resetState()

	n1 := Node{RawURI: "uri1", Name: "a"}
	n2 := Node{RawURI: "uri2", Name: "b"}
	MergeNodes([]Node{n1, n2})

	// n1 soft-failed → Tier 2; n2 healthy → Tier 1
	RecordTest("uri1", false, 0, "timeout")
	RecordTest("uri2", true, 50, "")

	// Request 2: should get n2 (Tier 1) + n1 (Tier 2 fallback)
	sel := SelectForParallel(2, false, false)
	if len(sel) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(sel))
	}

	// Verify n1 stays Tier 2 even after 10s (no automatic recovery)
	mu.Lock()
	if h := healthMap["uri1"]; h != nil {
		h.LastSubHealthyAt = time.Now().Unix() - 10
	}
	mu.Unlock()

	// n1 should still be Tier 2 (LastSubHealthyAt > 0)
	if h := healthMap["uri1"]; h != nil {
		tier := getNodeTier(n1, h)
		if tier != 2 {
			t.Errorf("Expected n1 to stay Tier 2, got Tier %d", tier)
		}
	}
	// SelectForParallel still works: n2 (Tier 1) + n1 (Tier 2 fallback)
	sel2 := SelectForParallel(2, false, false)
	if len(sel2) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(sel2))
	}
}

func TestSelectForParallel_LastSelectedAtSort(t *testing.T) {
	resetState()
	defer resetState()

	// 3 Tier 1 nodes, all InFlight=0, different LastSelectedAt
	n1 := Node{RawURI: "uri-a", Name: "a"}
	n2 := Node{RawURI: "uri-b", Name: "b"}
	n3 := Node{RawURI: "uri-c", Name: "c"}
	MergeNodes([]Node{n1, n2, n3})

	// Create healthMap entries and set LastSelectedAt
	mu.Lock()
	healthMap["uri-a"] = &NodeHealth{LastSelectedAt: time.Now().Unix() - 100, InFlight: 0}
	healthMap["uri-b"] = &NodeHealth{LastSelectedAt: time.Now().Unix() - 10, InFlight: 0}
	healthMap["uri-c"] = &NodeHealth{LastSelectedAt: 0, InFlight: 0}
	mu.Unlock()

	// Control round-robin so sorted[0] is picked
	// Sorted order: [uri-c(0), uri-a(now-100), uri-b(now-10)]
	// With atomicRoundRobinIndex=2, offset=(2+1)%3=0 → picks sorted[0]=uri-c
	atomic.StoreUint64(&atomicRoundRobinIndex, 2)
	sel := SelectForParallel(1, false, false)
	if len(sel) != 1 {
		t.Fatalf("expected 1 selected, got %d", len(sel))
	}
	if sel[0].RawURI != "uri-c" {
		t.Errorf("Expected uri-c (LastSelectedAt=0) to be selected first, got %s", sel[0].RawURI)
	}
}

func TestSelectForParallel_Phase3Protection(t *testing.T) {
	resetState()
	defer resetState()

	// Scenario A: fresh node available to replace recently used node
	t.Run("fresh replacement available", func(t *testing.T) {
		resetState()
		n1 := Node{RawURI: "uri-a", Name: "a"}
		n2 := Node{RawURI: "uri-b", Name: "b"}
		n3 := Node{RawURI: "uri-c", Name: "c"}
		n4 := Node{RawURI: "uri-d", Name: "d"}
		n5 := Node{RawURI: "uri-e", Name: "e"}
		MergeNodes([]Node{n1, n2, n3, n4, n5})

		now := time.Now().Unix()
		mu.Lock()
		// Two fresh (never selected), three stale (recently selected)
		healthMap["uri-a"] = &NodeHealth{LastSelectedAt: 0, InFlight: 0}
		healthMap["uri-b"] = &NodeHealth{LastSelectedAt: 0, InFlight: 0}
		healthMap["uri-c"] = &NodeHealth{LastSelectedAt: now - 1, InFlight: 0}
		healthMap["uri-d"] = &NodeHealth{LastSelectedAt: now - 1, InFlight: 0}
		healthMap["uri-e"] = &NodeHealth{LastSelectedAt: now - 1, InFlight: 0}
		mu.Unlock()

		// Sorted by inFlight→LastSelectedAt→URI: [uri-a(0), uri-b(0), uri-c(1), uri-d(1), uri-e(1)]
		// Set offset to skip fresh nodes: need offset=2 → atomicRoundRobinIndex such that (idx+1)%5=2
		atomic.StoreUint64(&atomicRoundRobinIndex, 1)
		sel := SelectForParallel(3, false, false)
		if len(sel) != 3 {
			t.Fatalf("expected 3 selected, got %d", len(sel))
		}
		// Phase 3 should replace uri-c and uri-d with uri-a and uri-b
		// Final selected: [uri-a, uri-b, uri-e]
		if sel[0].RawURI != "uri-a" {
			t.Errorf("Expected sel[0]=uri-a (fresh), got %s", sel[0].RawURI)
		}
		if sel[1].RawURI != "uri-b" {
			t.Errorf("Expected sel[1]=uri-b (fresh), got %s", sel[1].RawURI)
		}
		if sel[2].RawURI != "uri-e" {
			t.Errorf("Expected sel[2]=uri-e (stale, no more fresh), got %s", sel[2].RawURI)
		}
	})

	// Scenario B: all nodes within 5s, no fresh replacement → best-effort, no change
	t.Run("no fresh replacement", func(t *testing.T) {
		resetState()
		n1 := Node{RawURI: "uri-a", Name: "a"}
		n2 := Node{RawURI: "uri-b", Name: "b"}
		n3 := Node{RawURI: "uri-c", Name: "c"}
		MergeNodes([]Node{n1, n2, n3})

		now := time.Now().Unix()
		mu.Lock()
		healthMap["uri-a"] = &NodeHealth{LastSelectedAt: now - 1, InFlight: 0}
		healthMap["uri-b"] = &NodeHealth{LastSelectedAt: now - 1, InFlight: 0}
		healthMap["uri-c"] = &NodeHealth{LastSelectedAt: now - 1, InFlight: 0}
		mu.Unlock()

		sel := SelectForParallel(3, false, false)
		if len(sel) != 3 {
			t.Fatalf("expected 3 selected, got %d", len(sel))
		}
		// All 3 should still be present (best-effort, no replacement possible)
		got := make(map[string]bool)
		for _, s := range sel {
			got[s.RawURI] = true
		}
		for _, uri := range []string{"uri-a", "uri-b", "uri-c"} {
			if !got[uri] {
				t.Errorf("Expected %s to be in selected set, but it was replaced", uri)
			}
		}
	})

	// Scenario C: protection expired (LastSelectedAt >= 5s ago)
	t.Run("protection expired", func(t *testing.T) {
		resetState()
		n1 := Node{RawURI: "uri-a", Name: "a"}
		n2 := Node{RawURI: "uri-b", Name: "b"}
		n3 := Node{RawURI: "uri-c", Name: "c"}
		MergeNodes([]Node{n1, n2, n3})

		now := time.Now().Unix()
		mu.Lock()
		healthMap["uri-a"] = &NodeHealth{LastSelectedAt: now - 10, InFlight: 0}
		healthMap["uri-b"] = &NodeHealth{LastSelectedAt: now - 10, InFlight: 0}
		healthMap["uri-c"] = &NodeHealth{LastSelectedAt: now - 10, InFlight: 0}
		mu.Unlock()

		sel := SelectForParallel(3, false, false)
		if len(sel) != 3 {
			t.Fatalf("expected 3 selected, got %d", len(sel))
		}
		// All nodes should be returned unchanged (protection expired)
		got := make(map[string]bool)
		for _, s := range sel {
			got[s.RawURI] = true
		}
		for _, uri := range []string{"uri-a", "uri-b", "uri-c"} {
			if !got[uri] {
				t.Errorf("Expected %s to be in selected set, but it was replaced", uri)
			}
		}
	})
}

func TestRecordRateLimit_CooldownUntil(t *testing.T) {
	resetState()
	defer resetState()

	RecordRateLimit("uri1", 30)
	mu.Lock()
	h := healthMap["uri1"]
	mu.Unlock()
	if h == nil {
		t.Fatal("Expected health entry for uri1")
	}
	now := time.Now().Unix()
	if h.CooldownUntil <= now {
		t.Errorf("Expected CooldownUntil > now, got %d (now=%d)", h.CooldownUntil, now)
	}
	if h.CooldownUntil < now+28 || h.CooldownUntil > now+32 {
		t.Errorf("Expected CooldownUntil ~ now+30, got %d (now=%d)", h.CooldownUntil, now)
	}
}

func TestRecordTest_CooldownNotClearedOnFailure(t *testing.T) {
	resetState()
	defer resetState()

	n1 := Node{RawURI: "uri1", Name: "node1"}
	MergeNodes([]Node{n1})

	RecordRateLimit("uri1", 30)
	mu.Lock()
	h := healthMap["uri1"]
	mu.Unlock()
	if h == nil {
		t.Fatal("Expected health entry")
	}
	cooldown := h.CooldownUntil
	now := time.Now().Unix()
	if cooldown <= now {
		t.Fatalf("Expected CooldownUntil in the future after RecordRateLimit, got %d (now=%d)", cooldown, now)
	}

	RecordTest("uri1", false, 0, "timeout")

	mu.Lock()
	h2 := healthMap["uri1"]
	mu.Unlock()
	if h2 == nil {
		t.Fatal("Expected health entry after RecordTest")
	}
	if h2.CooldownUntil != cooldown {
		t.Errorf("RecordTest failure should NOT clear CooldownUntil: got %d, want %d", h2.CooldownUntil, cooldown)
	}
}

func TestSelectForParallel_Tier2RoundRobin(t *testing.T) {
	resetState()
	defer resetState()

	n1 := Node{RawURI: "uri-a", Name: "a"}
	n2 := Node{RawURI: "uri-b", Name: "b"}
	n3 := Node{RawURI: "uri-c", Name: "c"}
	MergeNodes([]Node{n1, n2, n3})

	now := time.Now().Unix()
	mu.Lock()
	// All Tier 2: LastSubHealthyAt with different values → deterministic sort.
	// Sort order (InFlight=0 → LastSelectedAt=0 → LastSubHealthyAt ASC): a(100), b(50), c(10)
	healthMap["uri-a"] = &NodeHealth{LastSubHealthyAt: now - 100}
	healthMap["uri-b"] = &NodeHealth{LastSubHealthyAt: now - 50}
	healthMap["uri-c"] = &NodeHealth{LastSubHealthyAt: now - 10}
	mu.Unlock()

	// sorted = [uri-a, uri-b, uri-c]
	// Starting from atomicRoundRobinIndex=0:
	//   offset = (0+1)%3 = 1 → picks sorted[1] = uri-b
	//   offset = (1+1)%3 = 2 → picks sorted[2] = uri-c
	//   offset = (2+1)%3 = 0 → picks sorted[0] = uri-a
	// Note: actual distribution may not be perfectly even because
	// SelectForParallel updates LastSelectedAt on selected nodes,
	// which shifts the sort order in subsequent calls.
	atomic.StoreUint64(&atomicRoundRobinIndex, 0)

	got := make(map[string]int)
	for call := 0; call < 9; call++ {
		sel := SelectForParallel(1, false, false)
		if len(sel) != 1 {
			t.Fatalf("call %d: expected 1 selected, got %d", call, len(sel))
		}
		got[sel[0].RawURI]++
	}

	for _, uri := range []string{"uri-a", "uri-b", "uri-c"} {
		if got[uri] == 0 {
			t.Errorf("%s was never selected — round-robin not working", uri)
		}
	}
	for _, uri := range []string{"uri-a", "uri-b", "uri-c"} {
		if got[uri] > 6 {
			t.Errorf("%s selected %d/9 times — likely no round-robin", uri, got[uri])
		}
	}
}

func TestSelectForParallel_Tier2SkipsCooldown(t *testing.T) {
	resetState()
	defer resetState()

	n1 := Node{RawURI: "uri-a", Name: "a"}
	n2 := Node{RawURI: "uri-b", Name: "b"}
	n3 := Node{RawURI: "uri-c", Name: "c"}
	MergeNodes([]Node{n1, n2, n3})

	now := time.Now().Unix()
	mu.Lock()
	// All Tier 2. uri-a is in cooldown.
	// Different LastSubHealthyAt to ensure deterministic sort: [uri-a, uri-b, uri-c]
	healthMap["uri-a"] = &NodeHealth{LastSubHealthyAt: now - 100, CooldownUntil: now + 300}
	healthMap["uri-b"] = &NodeHealth{LastSubHealthyAt: now - 50}
	healthMap["uri-c"] = &NodeHealth{LastSubHealthyAt: now - 10}
	mu.Unlock()

	atomic.StoreUint64(&atomicRoundRobinIndex, 0)
	// offset=(0+1)%3=1 → skips iterating [uri-b, uri-c]; uri-a in group but CooldownUntil>now
	selected := SelectForParallel(2, false, false)
	if len(selected) != 2 {
		t.Errorf("Expected 2 nodes (skip cooldown), got %d", len(selected))
	}
	for _, s := range selected {
		if s.RawURI == "uri-a" {
			t.Errorf("uri-a should be skipped due to CooldownUntil > now, but it was selected")
		}
	}

	// Requesting 3 with only 2 not in cooldown → get only 2
	selected2 := SelectForParallel(3, false, false)
	if len(selected2) != 2 {
		t.Errorf("Expected 2 nodes even when k=3 (uri-a in cooldown), got %d", len(selected2))
	}
}
