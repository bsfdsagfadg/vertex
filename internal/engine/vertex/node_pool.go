package vertex

import (
	"github.com/bsfdsagfadg/vertex/internal/node/exitpool"
)

// NodePool 是竞速引擎对出口节点池的最小消费契约（生产实现 *exitpool.Manager）。
// 类型位引用 []exitpool.Node 为 AGENTS.md 铁律 #10 记录的显式豁免（R3 下合法）：
// 候选结构体跨域只读传递。
type NodePool interface {
	SelectForParallel(k int, debugMode bool) []exitpool.Node
	NodeName(rawURI string) string
	RecordTest(uri string, ok bool, ms float64, errStr string)
	RecordRateLimit(uri string, seconds int)
	BatchUpdateNodesDisabled(uris []string, disabled bool)
	IncInFlight(uri string)
	DecInFlight(uri string)
	GetAverageLatency() float64
}

// nopNodePool 是节点池未注入时的防御性空实现：选点返回空、记账全部无操作，
// 引擎自然走 ActiveNodeURI 单节点降级路径，与直连/锁定模式语义一致。
type nopNodePool struct{}

func (nopNodePool) SelectForParallel(int, bool) []exitpool.Node { return nil }
func (nopNodePool) NodeName(string) string                      { return "" }
func (nopNodePool) RecordTest(string, bool, float64, string)    {}
func (nopNodePool) RecordRateLimit(string, int)                 {}
func (nopNodePool) BatchUpdateNodesDisabled([]string, bool)     {}
func (nopNodePool) IncInFlight(string)                          {}
func (nopNodePool) DecInFlight(string)                          {}
func (nopNodePool) GetAverageLatency() float64                  { return 0 }
