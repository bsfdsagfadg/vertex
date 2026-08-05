package nodes

import (
	"testing"
	"time"
)

func TestIncDecInFlight(t *testing.T) {
	resetState()
	defer resetState()

	MergeNodes([]Node{{RawURI: "uri1", Name: "node1"}}) //nolint:exhaustruct

	IncInFlight("uri1")
	IncInFlight("uri1")
	mu.Lock()
	h := healthMap["uri1"]
	mu.Unlock()
	if h == nil || h.InFlight != 2 {
		t.Fatalf("expected InFlight=2, got %+v", h)
	}

	DecInFlight("uri1")
	mu.Lock()
	h = healthMap["uri1"]
	mu.Unlock()
	if h.InFlight != 1 {
		t.Fatalf("expected InFlight=1 after dec, got %d", h.InFlight)
	}

	// 不存在的 URI：Dec 不 panic 且不创建条目
	DecInFlight("missing")
	mu.Lock()
	_, exists := healthMap["missing"]
	mu.Unlock()
	if exists {
		t.Fatal("DecInFlight should not create a health entry for missing uri")
	}
}

func TestResetStateClearsEverything(t *testing.T) {
	resetState()
	defer resetState()

	MergeNodes([]Node{{RawURI: "uri1", Name: "node1"}, {RawURI: "uri2", Name: "node2"}}) //nolint:exhaustruct
	RecordTest("uri1", true, 10, "")

	called := false
	ResetStateCallback = func() { called = true }
	defer func() { ResetStateCallback = nil }()

	ResetState()
	if !called {
		t.Fatal("expected ResetStateCallback to be invoked")
	}
	if len(LoadNodes()) != 0 {
		t.Fatalf("expected empty node list after reset, got %d", len(LoadNodes()))
	}
	if len(LoadHealth()) != 0 {
		t.Fatalf("expected empty health map after reset, got %d", len(LoadHealth()))
	}
	if name := GetNodeName("uri1"); name != "Unknown" {
		t.Fatalf("expected Unknown after reset, got %q", name)
	}
}

func TestGetNodeName(t *testing.T) {
	resetState()
	defer resetState()

	MergeNodes([]Node{{RawURI: "uri1", Name: "node1"}}) //nolint:exhaustruct
	if name := GetNodeName("uri1"); name != "node1" {
		t.Fatalf("expected node1, got %q", name)
	}
	if name := GetNodeName("missing"); name != "Unknown" {
		t.Fatalf("expected Unknown for missing uri, got %q", name)
	}
}

func TestTestProgressLifecycle(t *testing.T) {
	resetState()
	defer resetState()

	if IsTestRunning() {
		t.Fatal("expected not running initially")
	}
	if !StartTestProgress(5) {
		t.Fatal("expected first start to succeed")
	}
	if !IsTestRunning() {
		t.Fatal("expected running after start")
	}
	if StartTestProgress(5) {
		t.Fatal("expected second start to be rejected while running")
	}

	p := GetTestProgress()
	if p.Total != 5 || p.Done != 0 || p.CurrentNode != "准备中..." {
		t.Fatalf("unexpected initial progress: %+v", p)
	}

	UpdateTestProgress("nodeA", true)
	UpdateTestProgress("nodeB", false)
	p = GetTestProgress()
	if p.Done != 2 || p.OkCount != 1 || p.FailCount != 1 || p.CurrentNode != "nodeB" {
		t.Fatalf("unexpected progress after updates: %+v", p)
	}

	FinishTestProgress()
	if IsTestRunning() {
		t.Fatal("expected not running after finish")
	}
	if p := GetTestProgress(); p.CurrentNode != "测试完成" {
		t.Fatalf("expected 测试完成 marker, got %q", p.CurrentNode)
	}
	// 已结束后 CheckTestControl 应直接返回 true
	if !CheckTestControl() {
		t.Fatal("expected CheckTestControl true after finish")
	}
}

func TestTestProgressPauseResume(t *testing.T) {
	resetState()
	defer resetState()

	if !StartTestProgress(3) {
		t.Fatal("expected start success")
	}
	PauseTestProgress()
	if p := GetTestProgress(); !p.Paused {
		t.Fatal("expected paused state")
	}

	done := make(chan bool, 1)
	go func() {
		done <- CheckTestControl()
	}()

	time.Sleep(50 * time.Millisecond)
	ResumeTestProgress()

	select {
	case stopped := <-done:
		if stopped {
			t.Fatal("CheckTestControl should return false while still running after resume")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CheckTestControl did not return after resume")
	}

	FinishTestProgress()
	if !CheckTestControl() {
		t.Fatal("expected CheckTestControl true after finish")
	}
}

func TestTestProgressTerminate(t *testing.T) {
	resetState()
	defer resetState()

	if !StartTestProgress(3) {
		t.Fatal("expected start success")
	}
	TerminateTestProgress()
	p := GetTestProgress()
	if !p.Terminated {
		t.Fatal("expected terminated flag")
	}
	// Terminated 后 CheckTestControl 立即返回 true，不阻塞等待恢复
	if !CheckTestControl() {
		t.Fatal("expected CheckTestControl true when terminated")
	}
	// Terminated 后 Update 不再推进计数
	UpdateTestProgress("nodeX", true)
	if p := GetTestProgress(); p.Done != 0 {
		t.Fatalf("expected Done=0 after terminate, got %d", p.Done)
	}
}
