package exitpool

import (
	"database/sql"
	"sync"
)

// IdentityResolver 由外部注入，替代原 NodeIdentityFunc/IsSupportedFunc 函数指针全局变量。
// 接口定义于消费方（node 域），实现位于 transport（NewIdentityResolver）；
// 仅暴露 (key, ok)/(bool) 原语，不泄漏 transport.ParsedNode 类型。
type IdentityResolver interface {
	Identity(rawURI string) (key string, ok bool)
	Supported(rawURI string) bool
}

// Hooks 聚合跨域失效回调（双钩子：删除联动 + 全量重置联动）。
type Hooks struct {
	// InvalidateParsed 节点删除联动：失效 IR 解析缓存
	// （替代 DeleteNodeCallback + DeleteNodesBatchCallback，统一为批量形参）。
	InvalidateParsed func(uris []string)
	// InvalidateAll 全量重置联动：清空整个 IR 解析缓存
	// （替代 ResetStateCallback）。
	InvalidateAll func()
}

// healthQueueCap 是健康度异步写队列容量（原包级 healthQueue 语义）。
const healthQueueCap = 1024

// Manager 封装出口节点池的全部内存状态、持久化编排与健康度调度。
type Manager struct {
	mu          sync.RWMutex
	nodeList    []Node
	healthMap   map[string]*NodeHealth
	nodeNameMap map[string]string
	loaded      bool

	rrIndex uint64 // 竞速轮询游标（mu 保护下原子递增）

	database *sql.DB          // 进程级 SQLite 句柄（nil=无库内存模式）
	identity IdentityResolver // 替代 NodeIdentityFunc / IsSupportedFunc
	hooks    Hooks            // 替代 Delete/DeleteBatch/ResetState 三类回调

	queue chan healthEvent // 健康度异步写队列

	progressMu  sync.RWMutex
	progress    TestProgress
	testControl *sync.Cond
}

// NewManager 构造出口节点池管理器。database 为 nil 时运行于无库内存模式；
// identity/hooks 可为零值，由装配链显式注入。
func NewManager(database *sql.DB, identity IdentityResolver, hooks Hooks) *Manager {
	m := &Manager{
		healthMap:   make(map[string]*NodeHealth),
		nodeNameMap: make(map[string]string),
		database:    database,
		identity:    identity,
		hooks:       hooks,
		queue:       make(chan healthEvent, healthQueueCap),
	}
	m.testControl = sync.NewCond(&m.progressMu)
	return m
}
