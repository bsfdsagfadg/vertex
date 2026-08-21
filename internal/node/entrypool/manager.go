// Package entrypool 管理前置代理（Entry Proxy）节点池。
//
// 与出口竞速节点池（internal/node/exitpool）物理解耦：前置代理仅用于第一跳 SOCKS5
// 回环通道，SQLite 持久化到独立的 entry_nodes / entry_node_health 表，
// 不影响现有竞速引擎的节点选择逻辑。
package entrypool

import (
	"database/sql"
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

// IdentityResolver 由外部注入（transport.NewIdentityResolver 实现），
// 替代原 EntryIdentityFunc/EntryIsSupportedFunc 函数指针全局变量。
type IdentityResolver interface {
	Identity(rawURI string) (key string, ok bool)
	Supported(rawURI string) bool
}

// EntryHooks 聚合跨域失效回调（删除为单 URI 形参，对齐原 EntryDeleteCallback）。
type EntryHooks struct {
	// InvalidateParsed 前置节点删除联动：失效 IR 解析缓存。
	InvalidateParsed func(uri string)
}

// EntryManager 封装前置节点池的全部内存状态与持久化编排。
type EntryManager struct {
	mu             sync.RWMutex
	entryList      []Node
	entryHealthMap map[string]*NodeHealth
	entryLoaded    bool

	database *sql.DB          // 进程级 SQLite 句柄（nil=无库内存模式）
	identity IdentityResolver // 替代 EntryIdentityFunc / EntryIsSupportedFunc
	hooks    EntryHooks       // 替代 EntryDeleteCallback / ResetEntryState 联动
}

// NewEntryManager 构造前置节点池管理器。database 为 nil 时运行于无库内存模式。
func NewEntryManager(database *sql.DB, identity IdentityResolver, hooks EntryHooks) *EntryManager {
	return &EntryManager{
		entryHealthMap: make(map[string]*NodeHealth),
		database:       database,
		identity:       identity,
		hooks:          hooks,
	}
}
