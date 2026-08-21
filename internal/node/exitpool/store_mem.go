package exitpool

import (
	"sync/atomic"
)

type Node struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	RawURI   string `json:"raw_uri"`
	Disabled bool   `json:"disabled"`
}

type NodeHealth struct { //nolint:govet
	SuccessCount        int     `json:"success_count"`
	FailCount           int     `json:"fail_count"`
	ConsecutiveFailures int     `json:"consecutive_failures"`
	LastTestMs          float64 `json:"last_test_ms"`
	LastTestError       string  `json:"last_test_error"`
	LastSuccessAt       int64   `json:"last_success_at"`
	LastFailAt          int64   `json:"last_fail_at"`
	CooldownUntil       int64   `json:"cooldown_until"`
	Last429At           int64   `json:"last_429_at"`
	RateLimitCount      int     `json:"rate_limit_count"`
	RecentUseCount      int     `json:"recent_use_count"`
	LastSelectedAt      int64   `json:"last_selected_at"`
	InFlight            int32   `json:"-"`
	LastSubHealthyAt    int64   `json:"last_sub_healthy_at"`
}

// TestProgress 是批量测速进度快照。
type TestProgress struct {
	Running     bool   `json:"running"`
	Paused      bool   `json:"paused"`
	Terminated  bool   `json:"terminated"`
	Total       int    `json:"total"`
	Done        int    `json:"done"`
	OkCount     int    `json:"ok_count"`
	FailCount   int    `json:"fail_count"`
	CurrentNode string `json:"current_node"`
}

// ---- 内存节点池状态方法（与持久化/健康度解耦，见 store_db.go / store_health.go）----

func (m *Manager) IncInFlight(uri string) {
	m.mu.Lock()
	m.ensureLoaded()
	h, exists := m.healthMap[uri]
	if !exists {
		h = &NodeHealth{}
		m.healthMap[uri] = h
	}
	m.mu.Unlock()
	atomic.AddInt32(&h.InFlight, 1)
}

func (m *Manager) DecInFlight(uri string) {
	m.mu.Lock()
	m.ensureLoaded()
	h, exists := m.healthMap[uri]
	m.mu.Unlock()
	if exists {
		atomic.AddInt32(&h.InFlight, -1)
	}
}

func (m *Manager) Reset() {
	m.mu.Lock()
	m.nodeList = nil
	m.healthMap = make(map[string]*NodeHealth)
	m.nodeNameMap = make(map[string]string)
	m.loaded = false
	hooks := m.hooks
	m.mu.Unlock() // 先解锁再通知外部清理（如 transport 解析缓存），避免死锁
	if hooks.InvalidateAll != nil {
		hooks.InvalidateAll()
	}
}

// AllRawURIs 返回全部节点 URI（含禁用），供启动预热 IR 解析缓存使用。
func (m *Manager) AllRawURIs() []string {
	m.mu.RLock()
	if m.loaded {
		uris := make([]string, 0, len(m.nodeList))
		for _, n := range m.nodeList {
			uris = append(uris, n.RawURI)
		}
		m.mu.RUnlock()
		return uris
	}
	m.mu.RUnlock()

	m.mu.Lock()
	m.ensureLoaded()
	uris := make([]string, 0, len(m.nodeList))
	for _, n := range m.nodeList {
		uris = append(uris, n.RawURI)
	}
	m.mu.Unlock()
	return uris
}

// ---- 批量测速进度与流程控制 ----

func (m *Manager) GetTestProgress() TestProgress {
	m.progressMu.RLock()
	defer m.progressMu.RUnlock()
	return m.progress
}

func (m *Manager) IsTestRunning() bool {
	m.progressMu.RLock()
	defer m.progressMu.RUnlock()
	return m.progress.Running
}

func (m *Manager) StartTestProgress(total int) bool {
	m.progressMu.Lock()
	defer m.progressMu.Unlock()
	if m.progress.Running {
		return false
	}
	m.progress = TestProgress{
		Running:     true,
		Paused:      false,
		Terminated:  false,
		Total:       total,
		Done:        0,
		OkCount:     0,
		FailCount:   0,
		CurrentNode: "准备中...",
	}
	return true
}

func (m *Manager) UpdateTestProgress(nodeName string, ok bool) {
	m.progressMu.Lock()
	defer m.progressMu.Unlock()
	if !m.progress.Running || m.progress.Terminated {
		return
	}
	m.progress.Done++
	if ok {
		m.progress.OkCount++
	} else {
		m.progress.FailCount++
	}
	m.progress.CurrentNode = nodeName
}

func (m *Manager) FinishTestProgress() {
	m.progressMu.Lock()
	defer m.progressMu.Unlock()
	m.progress.Running = false
	m.progress.Paused = false
	m.progress.CurrentNode = "测试完成"
	m.testControl.Broadcast()
}

func (m *Manager) PauseTestProgress() {
	m.progressMu.Lock()
	defer m.progressMu.Unlock()
	if m.progress.Running && !m.progress.Terminated {
		m.progress.Paused = true
		m.progress.CurrentNode = "已暂停..."
	}
}

func (m *Manager) ResumeTestProgress() {
	m.progressMu.Lock()
	defer m.progressMu.Unlock()
	if m.progress.Running && m.progress.Paused {
		m.progress.Paused = false
		m.progress.CurrentNode = "恢复测试中..."
		m.testControl.Broadcast()
	}
}

func (m *Manager) TerminateTestProgress() {
	m.progressMu.Lock()
	defer m.progressMu.Unlock()
	if m.progress.Running {
		m.progress.Terminated = true
		m.progress.Paused = false
		m.progress.CurrentNode = "正在终止..."
		m.testControl.Broadcast()
	}
}

func (m *Manager) CheckTestControl() bool {
	m.progressMu.Lock()
	defer m.progressMu.Unlock()
	for m.progress.Running && m.progress.Paused && !m.progress.Terminated {
		m.testControl.Wait()
	}
	return !m.progress.Running || m.progress.Terminated
}

// ---- 节点名映射 ----

func (m *Manager) rebuildNodeNameMapUnsafe() {
	names := make(map[string]string, len(m.nodeList))
	for _, n := range m.nodeList {
		names[n.RawURI] = n.Name
	}
	m.nodeNameMap = names
}

// NodeName 返回节点展示名（满足 transport.NodeNamer）。
func (m *Manager) NodeName(uri string) string {
	m.mu.RLock()
	if m.loaded {
		name, ok := m.nodeNameMap[uri]
		m.mu.RUnlock()
		if ok {
			return name
		}
		return "Unknown"
	}
	m.mu.RUnlock()

	m.mu.Lock()
	m.ensureLoaded()
	name, ok := m.nodeNameMap[uri]
	m.mu.Unlock()
	if ok {
		return name
	}
	return "Unknown"
}
