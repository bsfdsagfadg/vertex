// Package entrynodes 管理前置代理（Entry Proxy）节点池。
//
// 与出口竞速节点池（internal/nodes）物理解耦：前置代理仅用于第一跳 SOCKS5
// 回环通道，SQLite 持久化到独立的 entry_nodes / entry_node_health 表，
// 不影响现有竞速引擎的节点选择逻辑。
package entrynodes

import (
	"sync"
)

// Node 是前置代理节点的内存表示，字段与 entry_nodes 表一一对应。
type Node struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	RawURI   string `json:"raw_uri"`
	Disabled bool   `json:"disabled"`
}

// NodeHealth 是前置代理节点的健康度快照，字段与 entry_node_health 表一一对应。
type NodeHealth struct { //nolint:govet
	SuccessCount        int     `json:"success_count"`
	FailCount           int     `json:"fail_count"`
	ConsecutiveFailures int     `json:"consecutive_failures"`
	LastTestMs          float64 `json:"last_test_ms"`
	LastTestError       string  `json:"last_test_error"`
	LastSuccessAt       int64   `json:"last_success_at"`
	LastFailAt          int64   `json:"last_fail_at"`
	CooldownUntil       int64   `json:"cooldown_until"`
}

// ---- 内存前置节点池状态（与持久化解耦，见 store_db.go）----

var (
	mu                     sync.RWMutex                              //nolint:gochecknoglobals
	entryList              []Node                                    //nolint:gochecknoglobals
	entryHealthMap         = make(map[string]*NodeHealth)            //nolint:gochecknoglobals
	entryLoaded            bool                                      //nolint:gochecknoglobals
	EntryDeleteCallback    func(uri string)                          //nolint:gochecknoglobals
	EntryIdentityFunc      func(rawURI string) (key string, ok bool) //nolint:gochecknoglobals
	EntryIsSupportedFunc   func(rawURI string) bool                  //nolint:gochecknoglobals
)

// ResetEntryState 清空内存状态并标记未加载，供测试隔离/全量重置使用。
// 会先解锁再通知外部清理（如 transport 解析缓存），避免死锁。
func ResetEntryState() {
	mu.Lock()
	entryList = nil
	entryHealthMap = make(map[string]*NodeHealth)
	entryLoaded = false
	cb := EntryDeleteCallback
	mu.Unlock()
	if cb != nil {
		cb("")
	}
}
