package exitpool

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestIncDecInFlight(t *testing.T) {
	mgr := newMemMgr(nil, Hooks{})

	mgr.MergeNodes([]Node{{RawURI: "uri1", Name: "node1"}}) //nolint:exhaustruct

	mgr.IncInFlight("uri1")
	mgr.IncInFlight("uri1")
	mgr.mu.Lock()
	h := mgr.healthMap["uri1"]
	mgr.mu.Unlock()
	if h == nil || h.InFlight != 2 {
		t.Fatalf("expected InFlight=2, got %+v", h)
	}

	mgr.DecInFlight("uri1")
	mgr.mu.Lock()
	h = mgr.healthMap["uri1"]
	mgr.mu.Unlock()
	if h.InFlight != 1 {
		t.Fatalf("expected InFlight=1 after dec, got %d", h.InFlight)
	}

	// 不存在的 URI：Dec 不 panic 且不创建条目
	mgr.DecInFlight("missing")
	mgr.mu.Lock()
	_, exists := mgr.healthMap["missing"]
	mgr.mu.Unlock()
	if exists {
		t.Fatal("DecInFlight should not create a health entry for missing uri")
	}
}

func TestResetStateClearsEverything(t *testing.T) {
	var called bool
	mgr := newMemMgr(nil, Hooks{InvalidateAll: func() { called = true }})

	mgr.MergeNodes([]Node{{RawURI: "uri1", Name: "node1"}, {RawURI: "uri2", Name: "node2"}}) //nolint:exhaustruct
	mgr.RecordTest("uri1", true, 10, "")

	mgr.Reset()
	if !called {
		t.Fatal("expected hooks.InvalidateAll to be invoked")
	}
	if len(mgr.LoadNodes()) != 0 {
		t.Fatalf("expected empty node list after reset, got %d", len(mgr.LoadNodes()))
	}
	if len(mgr.LoadHealth()) != 0 {
		t.Fatalf("expected empty health map after reset, got %d", len(mgr.LoadHealth()))
	}
	if name := mgr.NodeName("uri1"); name != "Unknown" {
		t.Fatalf("expected Unknown after reset, got %q", name)
	}
}

func TestGetNodeName(t *testing.T) {
	mgr := newMemMgr(nil, Hooks{})

	mgr.MergeNodes([]Node{{RawURI: "uri1", Name: "node1"}}) //nolint:exhaustruct
	if name := mgr.NodeName("uri1"); name != "node1" {
		t.Fatalf("expected node1, got %q", name)
	}
	if name := mgr.NodeName("missing"); name != "Unknown" {
		t.Fatalf("expected Unknown for missing uri, got %q", name)
	}
}

func TestTestProgressLifecycle(t *testing.T) {
	mgr := newMemMgr(nil, Hooks{})

	if mgr.IsTestRunning() {
		t.Fatal("expected not running initially")
	}
	if !mgr.StartTestProgress(5) {
		t.Fatal("expected first start to succeed")
	}
	if !mgr.IsTestRunning() {
		t.Fatal("expected running after start")
	}
	if mgr.StartTestProgress(5) {
		t.Fatal("expected second start to be rejected while running")
	}

	p := mgr.GetTestProgress()
	if p.Total != 5 || p.Done != 0 || p.CurrentNode != "准备中..." {
		t.Fatalf("unexpected initial progress: %+v", p)
	}

	mgr.UpdateTestProgress("nodeA", true)
	mgr.UpdateTestProgress("nodeB", false)
	p = mgr.GetTestProgress()
	if p.Done != 2 || p.OkCount != 1 || p.FailCount != 1 || p.CurrentNode != "nodeB" {
		t.Fatalf("unexpected progress after updates: %+v", p)
	}

	mgr.FinishTestProgress()
	if mgr.IsTestRunning() {
		t.Fatal("expected not running after finish")
	}
	if p := mgr.GetTestProgress(); p.CurrentNode != "测试完成" {
		t.Fatalf("expected 测试完成 marker, got %q", p.CurrentNode)
	}
	// 已结束后 CheckTestControl 应直接返回 true
	if !mgr.CheckTestControl() {
		t.Fatal("expected CheckTestControl true after finish")
	}
}

func TestTestProgressPauseResume(t *testing.T) {
	mgr := newMemMgr(nil, Hooks{})

	if !mgr.StartTestProgress(3) {
		t.Fatal("expected start success")
	}
	mgr.PauseTestProgress()
	if p := mgr.GetTestProgress(); !p.Paused {
		t.Fatal("expected paused state")
	}

	done := make(chan bool, 1)
	go func() {
		done <- mgr.CheckTestControl()
	}()

	time.Sleep(50 * time.Millisecond)
	mgr.ResumeTestProgress()

	select {
	case stopped := <-done:
		if stopped {
			t.Fatal("CheckTestControl should return false while still running after resume")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CheckTestControl did not return after resume")
	}

	mgr.FinishTestProgress()
	if !mgr.CheckTestControl() {
		t.Fatal("expected CheckTestControl true after finish")
	}
}

func TestTestProgressTerminate(t *testing.T) {
	mgr := newMemMgr(nil, Hooks{})

	if !mgr.StartTestProgress(3) {
		t.Fatal("expected start success")
	}
	mgr.TerminateTestProgress()
	p := mgr.GetTestProgress()
	if !p.Terminated {
		t.Fatal("expected terminated flag")
	}
	// Terminated 后 CheckTestControl 立即返回 true，不阻塞等待恢复
	if !mgr.CheckTestControl() {
		t.Fatal("expected CheckTestControl true when terminated")
	}
	// Terminated 后 Update 不再推进计数
	mgr.UpdateTestProgress("nodeX", true)
	if p := mgr.GetTestProgress(); p.Done != 0 {
		t.Fatalf("expected Done=0 after terminate, got %d", p.Done)
	}
}

// rrIndexStore 便于对实例轮询游标做确定性控制。
func rrIndexStore(mgr *Manager, v uint64) {
	atomic.StoreUint64(&mgr.rrIndex, v)
}
