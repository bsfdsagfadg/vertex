package api

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTaskManagerLifecycle(t *testing.T) {
	tm := NewTaskManager()

	started := make(chan struct{})
	canFinish := make(chan struct{})

	taskID, err := tm.StartTask(context.Background(), "test_type", 10, func(tc *TaskControl) error {
		close(started)
		tc.UpdateProgress("node_1", true)
		<-canFinish
		tc.UpdateProgress("node_2", true)
		return nil
	})
	if err != nil {
		t.Fatalf("StartTask failed: %v", err)
	}
	if taskID == "" {
		t.Fatal("empty taskID returned")
	}

	<-started

	info, ok := tm.GetTask(taskID)
	if !ok {
		t.Fatalf("task %s not found", taskID)
	}
	if info.State != TaskStateRunning {
		t.Fatalf("expected state Running, got %s", info.State)
	}
	if info.Progress.Done != 1 || info.Progress.OkCount != 1 {
		t.Fatalf("unexpected progress: %+v", info.Progress)
	}

	// Try starting duplicate task type
	_, dupErr := tm.StartTask(context.Background(), "test_type", 5, func(tc *TaskControl) error {
		return nil
	})
	if dupErr == nil {
		t.Fatal("expected duplicate task start error, got nil")
	}

	// Pause and resume
	if !tm.Pause(taskID) {
		t.Fatal("failed to pause task")
	}
	info, _ = tm.GetTask(taskID)
	if info.State != TaskStatePaused {
		t.Fatalf("expected state Paused, got %s", info.State)
	}

	if !tm.Resume(taskID) {
		t.Fatal("failed to resume task")
	}
	info, _ = tm.GetTask(taskID)
	if info.State != TaskStateRunning {
		t.Fatalf("expected state Running, got %s", info.State)
	}

	close(canFinish)

	// Wait for completion
	var finalInfo TaskInfo
	for i := 0; i < 20; i++ {
		time.Sleep(20 * time.Millisecond)
		finalInfo, _ = tm.GetTask(taskID)
		if finalInfo.State == TaskStateCompleted {
			break
		}
	}
	if finalInfo.State != TaskStateCompleted {
		t.Fatalf("expected state Completed, got %s", finalInfo.State)
	}
	if finalInfo.Progress.Done != 2 || finalInfo.Progress.OkCount != 2 {
		t.Fatalf("unexpected final progress: %+v", finalInfo.Progress)
	}
}

func TestTaskManagerTermination(t *testing.T) {
	tm := NewTaskManager()

	started := make(chan struct{})
	taskID, err := tm.StartTask(context.Background(), "node_test", 100, func(tc *TaskControl) error {
		close(started)
		for i := 0; i < 100; i++ {
			if tc.CheckControl() {
				return errors.New("aborted")
			}
			time.Sleep(10 * time.Millisecond)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("StartTask failed: %v", err)
	}

	<-started

	if !tm.Terminate(taskID) {
		t.Fatal("failed to terminate task")
	}

	var finalInfo TaskInfo
	for i := 0; i < 20; i++ {
		time.Sleep(10 * time.Millisecond)
		finalInfo, _ = tm.GetTask(taskID)
		if finalInfo.State == TaskStateTerminated {
			break
		}
	}
	if finalInfo.State != TaskStateTerminated {
		t.Fatalf("expected state Terminated, got %s", finalInfo.State)
	}
}

func TestTaskManagerPauseBlocksCheckControl(t *testing.T) {
	tm := NewTaskManager()

	started := make(chan struct{})
	stepDone := make(chan int, 5)

	taskID, err := tm.StartTask(context.Background(), "pause_test", 10, func(tc *TaskControl) error {
		close(started)
		for i := 1; i <= 3; i++ {
			if tc.CheckControl() {
				return errors.New("terminated")
			}
			stepDone <- i
			time.Sleep(30 * time.Millisecond)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("StartTask failed: %v", err)
	}

	<-started
	firstStep := <-stepDone
	if firstStep != 1 {
		t.Fatalf("expected step 1, got %d", firstStep)
	}

	tm.Pause(taskID)
	time.Sleep(80 * time.Millisecond)

	// No new step should arrive while paused
	select {
	case next := <-stepDone:
		t.Fatalf("unexpected step %d arrived while paused", next)
	default:
	}

	tm.Resume(taskID)
	secondStep := <-stepDone
	if secondStep != 2 {
		t.Fatalf("expected step 2 after resume, got %d", secondStep)
	}
}
