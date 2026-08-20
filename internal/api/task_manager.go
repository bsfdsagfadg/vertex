package api

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// TaskState represents the lifecycle status of an asynchronous background task.
type TaskState string

const (
	TaskStateQueued     TaskState = "Queued"
	TaskStateRunning    TaskState = "Running"
	TaskStatePaused     TaskState = "Paused"
	TaskStateTerminated TaskState = "Terminated"
	TaskStateCompleted  TaskState = "Completed"
)

// TaskProgress holds real-time progress and metrics for a background task.
type TaskProgress struct {
	Total       int     `json:"total"`
	Done        int     `json:"done"`
	OkCount     int     `json:"ok_count"`
	FailCount   int     `json:"fail_count"`
	CurrentNode string  `json:"current_node"`
	Extra       any     `json:"extra,omitempty"`
}

// TaskInfo represents metadata and current state of a task.
type TaskInfo struct {
	ID        string       `json:"id"`
	Type      string       `json:"type"`
	State     TaskState    `json:"state"`
	Progress  TaskProgress `json:"progress"`
	Error     string       `json:"error,omitempty"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

// TaskControl provides execution control hooks to the running task worker.
type TaskControl struct {
	ctx        context.Context
	cancelFunc context.CancelFunc
	task       *taskEntry
	tm         *TaskManager
}

// Context returns the task execution context.
func (tc *TaskControl) Context() context.Context {
	return tc.ctx
}

// CheckControl checks whether the task is paused or terminated.
// If paused, it blocks until resumed or context canceled.
// Returns true if the task has been terminated / canceled and should abort immediately.
func (tc *TaskControl) CheckControl() bool {
	select {
	case <-tc.ctx.Done():
		return true
	default:
	}

	tc.task.mu.Lock()
	for tc.task.state == TaskStatePaused {
		tc.task.cond.Wait()
		select {
		case <-tc.ctx.Done():
			tc.task.mu.Unlock()
			return true
		default:
		}
	}
	isTerminated := tc.task.state == TaskStateTerminated
	tc.task.mu.Unlock()
	return isTerminated
}

// UpdateProgress updates the current progress metrics for the task.
func (tc *TaskControl) UpdateProgress(currentNode string, ok bool) {
	tc.task.mu.Lock()
	defer tc.task.mu.Unlock()
	if tc.task.state != TaskStateRunning && tc.task.state != TaskStatePaused {
		return
	}
	tc.task.progress.Done++
	if ok {
		tc.task.progress.OkCount++
	} else {
		tc.task.progress.FailCount++
	}
	tc.task.progress.CurrentNode = currentNode
	tc.task.updatedAt = time.Now()
}

// SetCurrentNode updates the current node or step name.
func (tc *TaskControl) SetCurrentNode(name string) {
	tc.task.mu.Lock()
	defer tc.task.mu.Unlock()
	tc.task.progress.CurrentNode = name
	tc.task.updatedAt = time.Now()
}

// SetExtra updates arbitrary extra info for the task progress.
func (tc *TaskControl) SetExtra(extra any) {
	tc.task.mu.Lock()
	defer tc.task.mu.Unlock()
	tc.task.progress.Extra = extra
	tc.task.updatedAt = time.Now()
}

type taskEntry struct {
	id         string
	taskType   string
	state      TaskState
	progress   TaskProgress
	errText    string
	createdAt  time.Time
	updatedAt  time.Time
	cancelFunc context.CancelFunc

	mu   sync.Mutex
	cond *sync.Cond
}

func newTaskEntry(id, taskType string, total int, cancel context.CancelFunc) *taskEntry {
	entry := &taskEntry{
		id:         id,
		taskType:   taskType,
		state:      TaskStateRunning,
		createdAt:  time.Now(),
		updatedAt:  time.Now(),
		cancelFunc: cancel,
		progress: TaskProgress{
			Total:       total,
			Done:        0,
			OkCount:     0,
			FailCount:   0,
			CurrentNode: "准备中...",
		},
	}
	entry.cond = sync.NewCond(&entry.mu)
	return entry
}

// TaskManager coordinates asynchronous background operations.
type TaskManager struct {
	mu      sync.RWMutex
	tasks   map[string]*taskEntry
	byType  map[string]string // taskType -> active task ID
	counter uint64
}

// NewTaskManager creates a new TaskManager instance.
func NewTaskManager() *TaskManager {
	return &TaskManager{
		tasks:  make(map[string]*taskEntry),
		byType: make(map[string]string),
	}
}

// StartTask launches a background task with lifecycle tracking.
// If an active task of the same taskType is already running/paused, it returns an error.
func (tm *TaskManager) StartTask(
	ctx context.Context,
	taskType string,
	total int,
	fn func(tc *TaskControl) error,
) (string, error) {
	tm.mu.Lock()
	if activeID, exists := tm.byType[taskType]; exists {
		if activeTask, ok := tm.tasks[activeID]; ok {
			activeTask.mu.Lock()
			isRunning := activeTask.state == TaskStateRunning || activeTask.state == TaskStatePaused
			activeTask.mu.Unlock()
			if isRunning {
				tm.mu.Unlock()
				return "", fmt.Errorf("task of type %q is already running (id: %s)", taskType, activeID)
			}
		}
	}

	seq := atomic.AddUint64(&tm.counter, 1)
	taskID := fmt.Sprintf("task_%s_%d_%d", taskType, time.Now().UnixNano(), seq)

	taskCtx, cancel := context.WithCancel(ctx)
	entry := newTaskEntry(taskID, taskType, total, cancel)

	tm.tasks[taskID] = entry
	tm.byType[taskType] = taskID
	tm.mu.Unlock()

	tc := &TaskControl{
		ctx:        taskCtx,
		cancelFunc: cancel,
		task:       entry,
		tm:         tm,
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				entry.mu.Lock()
				entry.state = TaskStateTerminated
				entry.errText = fmt.Sprintf("panic: %v", r)
				entry.progress.CurrentNode = "已终止"
				entry.updatedAt = time.Now()
				entry.cond.Broadcast()
				entry.mu.Unlock()
			}
			cancel()
			tm.cleanupOldTasks()
		}()

		err := fn(tc)

		entry.mu.Lock()
		defer entry.mu.Unlock()
		if entry.state == TaskStateTerminated {
			entry.progress.CurrentNode = "已终止"
		} else if err != nil {
			if entry.state != TaskStateTerminated {
				entry.state = TaskStateCompleted
				entry.errText = err.Error()
				entry.progress.CurrentNode = "已结束"
			}
		} else {
			entry.state = TaskStateCompleted
			entry.progress.CurrentNode = "已完成"
		}
		entry.updatedAt = time.Now()
		entry.cond.Broadcast()
	}()

	return taskID, nil
}

// Pause pauses the task by ID.
func (tm *TaskManager) Pause(taskID string) bool {
	tm.mu.RLock()
	entry, ok := tm.tasks[taskID]
	tm.mu.RUnlock()
	if !ok {
		return false
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.state == TaskStateRunning {
		entry.state = TaskStatePaused
		entry.updatedAt = time.Now()
		return true
	}
	return false
}

// Resume resumes a paused task by ID.
func (tm *TaskManager) Resume(taskID string) bool {
	tm.mu.RLock()
	entry, ok := tm.tasks[taskID]
	tm.mu.RUnlock()
	if !ok {
		return false
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.state == TaskStatePaused {
		entry.state = TaskStateRunning
		entry.updatedAt = time.Now()
		entry.cond.Broadcast()
		return true
	}
	return false
}

// Terminate terminates a task by ID.
func (tm *TaskManager) Terminate(taskID string) bool {
	tm.mu.RLock()
	entry, ok := tm.tasks[taskID]
	tm.mu.RUnlock()
	if !ok {
		return false
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.state == TaskStateRunning || entry.state == TaskStatePaused {
		entry.state = TaskStateTerminated
		entry.progress.CurrentNode = "已终止"
		entry.updatedAt = time.Now()
		if entry.cancelFunc != nil {
			entry.cancelFunc()
		}
		entry.cond.Broadcast()
		return true
	}
	return false
}

// GetTask returns a snapshot of task status by ID.
func (tm *TaskManager) GetTask(taskID string) (TaskInfo, bool) {
	tm.mu.RLock()
	entry, ok := tm.tasks[taskID]
	tm.mu.RUnlock()
	if !ok {
		return TaskInfo{}, false
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()
	return TaskInfo{
		ID:        entry.id,
		Type:      entry.taskType,
		State:     entry.state,
		Progress:  entry.progress,
		Error:     entry.errText,
		CreatedAt: entry.createdAt,
		UpdatedAt: entry.updatedAt,
	}, true
}

// GetActiveTaskByType returns the active (running/paused) task by type if any.
func (tm *TaskManager) GetActiveTaskByType(taskType string) (TaskInfo, bool) {
	tm.mu.RLock()
	taskID, exists := tm.byType[taskType]
	if !exists {
		tm.mu.RUnlock()
		return TaskInfo{}, false
	}
	entry, ok := tm.tasks[taskID]
	tm.mu.RUnlock()
	if !ok {
		return TaskInfo{}, false
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()
	return TaskInfo{
		ID:        entry.id,
		Type:      entry.taskType,
		State:     entry.state,
		Progress:  entry.progress,
		Error:     entry.errText,
		CreatedAt: entry.createdAt,
		UpdatedAt: entry.updatedAt,
	}, true
}

// PauseActiveByType pauses the active task of a given type.
func (tm *TaskManager) PauseActiveByType(taskType string) bool {
	tm.mu.RLock()
	taskID, ok := tm.byType[taskType]
	tm.mu.RUnlock()
	if !ok {
		return false
	}
	return tm.Pause(taskID)
}

// ResumeActiveByType resumes the active task of a given type.
func (tm *TaskManager) ResumeActiveByType(taskType string) bool {
	tm.mu.RLock()
	taskID, ok := tm.byType[taskType]
	tm.mu.RUnlock()
	if !ok {
		return false
	}
	return tm.Resume(taskID)
}

// TerminateActiveByType terminates the active task of a given type.
func (tm *TaskManager) TerminateActiveByType(taskType string) bool {
	tm.mu.RLock()
	taskID, ok := tm.byType[taskType]
	tm.mu.RUnlock()
	if !ok {
		return false
	}
	return tm.Terminate(taskID)
}

// ListTasks returns all tracked tasks.
func (tm *TaskManager) ListTasks() []TaskInfo {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	list := make([]TaskInfo, 0, len(tm.tasks))
	for _, entry := range tm.tasks {
		entry.mu.Lock()
		list = append(list, TaskInfo{
			ID:        entry.id,
			Type:      entry.taskType,
			State:     entry.state,
			Progress:  entry.progress,
			Error:     entry.errText,
			CreatedAt: entry.createdAt,
			UpdatedAt: entry.updatedAt,
		})
		entry.mu.Unlock()
	}
	return list
}

func (tm *TaskManager) cleanupOldTasks() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	cutoff := time.Now().Add(-1 * time.Hour)
	for id, entry := range tm.tasks {
		entry.mu.Lock()
		isDone := entry.state == TaskStateCompleted || entry.state == TaskStateTerminated
		updated := entry.updatedAt
		entry.mu.Unlock()
		if isDone && updated.Before(cutoff) {
			delete(tm.tasks, id)
			if tm.byType[entry.taskType] == id {
				delete(tm.byType, entry.taskType)
			}
		}
	}
}
