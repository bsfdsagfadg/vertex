package nodes

import (
	"sync"
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

// ---- 内存节点池状态（与持久化/健康度解耦，见 store_db.go / store_health.go）----

var (
	mu                       sync.RWMutex                              //nolint:gochecknoglobals
	nodeList                 []Node                                    //nolint:gochecknoglobals
	healthMap                = make(map[string]*NodeHealth)            //nolint:gochecknoglobals
	loaded                   bool                                      //nolint:gochecknoglobals
	DeleteNodeCallback       func(uri string)                          //nolint:gochecknoglobals
	DeleteNodesBatchCallback func(uris []string)                       //nolint:gochecknoglobals
	NodeIdentityFunc         func(rawURI string) (key string, ok bool) //nolint:gochecknoglobals
	ResetStateCallback       func()                                    //nolint:gochecknoglobals
	IsSupportedFunc          func(rawURI string) bool                  //nolint:gochecknoglobals
	atomicRoundRobinIndex    uint64                                    //nolint:gochecknoglobals
	nodeNameMap              = make(map[string]string)                 //nolint:gochecknoglobals
)

func IncInFlight(uri string) {
	mu.Lock()
	ensureLoaded()
	h, exists := healthMap[uri]
	if !exists {
		h = &NodeHealth{}
		healthMap[uri] = h
	}
	mu.Unlock()
	atomic.AddInt32(&h.InFlight, 1)
}

func DecInFlight(uri string) {
	mu.Lock()
	ensureLoaded()
	h, exists := healthMap[uri]
	mu.Unlock()
	if exists {
		atomic.AddInt32(&h.InFlight, -1)
	}
}

func ResetState() {
	mu.Lock()
	nodeList = nil
	healthMap = make(map[string]*NodeHealth)
	nodeNameMap = make(map[string]string)
	loaded = false
	cb := ResetStateCallback
	mu.Unlock() // 先解锁再通知外部清理（如 transport 解析缓存），避免死锁
	if cb != nil {
		cb()
	}
}

// GetAllRawURIs 返回全部节点 URI（含禁用），供启动预热 IR 解析缓存使用。
func GetAllRawURIs() []string {
	mu.RLock()
	if loaded {
		uris := make([]string, 0, len(nodeList))
		for _, n := range nodeList {
			uris = append(uris, n.RawURI)
		}
		mu.RUnlock()
		return uris
	}
	mu.RUnlock()

	mu.Lock()
	ensureLoaded()
	uris := make([]string, 0, len(nodeList))
	for _, n := range nodeList {
		uris = append(uris, n.RawURI)
	}
	mu.Unlock()
	return uris
}

// ---- 批量测速进度与流程控制 ----

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

var (
	//nolint:gochecknoglobals // Test progress lock
	progressMu sync.RWMutex
	//nolint:gochecknoglobals // Test progress state
	globalProgress TestProgress
	//nolint:gochecknoglobals // Test progress control cond
	testControlCond = sync.NewCond(&progressMu)
)

func GetTestProgress() TestProgress {
	progressMu.RLock()
	defer progressMu.RUnlock()
	return globalProgress
}

func IsTestRunning() bool {
	progressMu.RLock()
	defer progressMu.RUnlock()
	return globalProgress.Running
}

func StartTestProgress(total int) bool {
	progressMu.Lock()
	defer progressMu.Unlock()
	if globalProgress.Running {
		return false
	}
	globalProgress = TestProgress{
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

func UpdateTestProgress(nodeName string, ok bool) {
	progressMu.Lock()
	defer progressMu.Unlock()
	if !globalProgress.Running || globalProgress.Terminated {
		return
	}
	globalProgress.Done++
	if ok {
		globalProgress.OkCount++
	} else {
		globalProgress.FailCount++
	}
	globalProgress.CurrentNode = nodeName
}

func FinishTestProgress() {
	progressMu.Lock()
	defer progressMu.Unlock()
	globalProgress.Running = false
	globalProgress.Paused = false
	globalProgress.CurrentNode = "测试完成"
	testControlCond.Broadcast()
}

func PauseTestProgress() {
	progressMu.Lock()
	defer progressMu.Unlock()
	if globalProgress.Running && !globalProgress.Terminated {
		globalProgress.Paused = true
		globalProgress.CurrentNode = "已暂停..."
	}
}

func ResumeTestProgress() {
	progressMu.Lock()
	defer progressMu.Unlock()
	if globalProgress.Running && globalProgress.Paused {
		globalProgress.Paused = false
		globalProgress.CurrentNode = "恢复测试中..."
		testControlCond.Broadcast()
	}
}

func TerminateTestProgress() {
	progressMu.Lock()
	defer progressMu.Unlock()
	if globalProgress.Running {
		globalProgress.Terminated = true
		globalProgress.Paused = false
		globalProgress.CurrentNode = "正在终止..."
		testControlCond.Broadcast()
	}
}

func CheckTestControl() bool {
	progressMu.Lock()
	defer progressMu.Unlock()
	for globalProgress.Running && globalProgress.Paused && !globalProgress.Terminated {
		testControlCond.Wait()
	}
	return !globalProgress.Running || globalProgress.Terminated
}

// ---- 节点名映射 ----

func rebuildNodeNameMapUnsafe() {
	m := make(map[string]string, len(nodeList))
	for _, n := range nodeList {
		m[n.RawURI] = n.Name
	}
	nodeNameMap = m
}

func GetNodeName(uri string) string {
	mu.RLock()
	if loaded {
		name, ok := nodeNameMap[uri]
		mu.RUnlock()
		if ok {
			return name
		}
		return "Unknown"
	}
	mu.RUnlock()

	mu.Lock()
	ensureLoaded()
	name, ok := nodeNameMap[uri]
	mu.Unlock()
	if ok {
		return name
	}
	return "Unknown"
}
