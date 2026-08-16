package vertex

import (
	"errors"
	"log"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/nodes"
)

// ApplyNodeFailure 是节点池健康态的旁路消费者：吃透传错误，改节点池健康态，
// 不触碰错误内容、不影响竞速裁决。以 VertexError 为输入，不返回任何值。
//
// 归因规则（按顺序）：
//   - uri == ""（直连/前置代理）：无操作——直连没有可归属的出口节点，前置代理身份在建连时
//     round-robin 轮换且 Session 不携带实际被轮询到的前置身份，误禁用会破坏前置池容错；
//   - 流式包间空闲超时（ErrStreamIdleTimeout）：15 秒短时避让，避免下一轮反复选中僵死节点；
//   - Kind == "infra"（rT 子系统耗尽）：无操作——全局故障，不归因节点，避免误记失败；
//   - Kind == "ratelimit"：30 秒歇息；
//   - Kind == "auth"：记录失败并禁用节点（认证失败即节点密钥/token 失效，需要人工恢复）；
//   - 其余：记录失败。
func ApplyNodeFailure(uri string, err error) {
	if uri == "" || err == nil {
		return
	}
	ve := asVertexError(err)
	if ve == nil {
		nodes.RecordTest(uri, false, 0, err.Error())
		return
	}
	switch {
	case errors.Is(err, ErrStreamIdleTimeout) ||
		(ve.Kind == "network" && strings.Contains(ve.Message, "idle timeout")):
		log.Printf("[Racing] 节点 %s 触发流式包间空闲超时，进入 15 秒短时避让", nodes.GetNodeName(uri))
		nodes.RecordRateLimit(uri, 15)
	case ve.Kind == "infra":
		return
	case ve.Kind == "ratelimit":
		log.Printf("[Racing] 节点 %s 触发 429 API 限制，进入 30 秒短时歇息", nodes.GetNodeName(uri))
		nodes.RecordRateLimit(uri, 30)
	case ve.Kind == "auth":
		log.Printf("[Racing] 节点 %s 触发认证失败 (502)，即将禁用", nodes.GetNodeName(uri))
		nodes.RecordTest(uri, false, 0, err.Error())
		nodes.BatchUpdateNodesDisabled([]string{uri}, true)
	default:
		nodes.RecordTest(uri, false, 0, err.Error())
	}
}
