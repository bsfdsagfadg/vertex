package transform

import (
	cryptorand "crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// 本文件是流式工具调用 tracker 的独立载体：不再依赖旧的 map 形态
// （旧 stream.go 的 ProcessFunctionCall 接收 map 参数），直接以函数名
// 为输入，供 typed 流式链路与 api 层共用。

var (
	streamReqIDFallback atomic.Bool
	streamReqIDCounter  atomic.Uint64
)

// StreamToolCallTracker 跟踪流式工具调用 ID/Index，确保同一个 functionCall 在
// 增量帧中 id 和 index 保持稳定。
//
// 状态机规则：
//   - 每个 chunk 帧开始前必须调用 BeginFrame 重置帧内同名计数；
//   - 帧内同名多次出现（如同一 chunk 中两次 read_file）视为多个独立的工具调用，
//     分配不同的 (index, callID)，杜绝被误判为同名旧调用的增量续打；
//   - 跨帧按"帧内出现次序"匹配：新帧第 N 次出现的 name 对应历史第 N 个同名 entry，
//     命中则返回既有 (index, callID) 且 isNew=false（续打），未命中则创建新调用。
type StreamToolCallTracker struct {
	mu              sync.Mutex
	entries         []toolCallEntry
	nextIndex       int
	frameOccurrence map[string]int
}

type toolCallEntry struct {
	name   string
	callID string
	index  int
}

// NewStreamToolCallTracker 构造 tracker。
func NewStreamToolCallTracker() *StreamToolCallTracker {
	return &StreamToolCallTracker{frameOccurrence: make(map[string]int)}
}

// BeginFrame 标记一个新 chunk 帧的开始，重置帧内同名出现计数。
func (t *StreamToolCallTracker) BeginFrame() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.frameOccurrence = make(map[string]int)
}

// ProcessFunctionCall 返回稳定 (index, callID, isNew)。
// 帧内第 N 次出现的 name 匹配历史第 N 个同名 entry：命中返回既有值（续打），
// 未命中生成新 ID 和 index。name 为空时始终按新调用处理。
func (t *StreamToolCallTracker) ProcessFunctionCall(name string) (int, string, bool) {
	if t == nil {
		return 0, "", false
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.frameOccurrence == nil {
		t.frameOccurrence = make(map[string]int)
	}

	if name != "" {
		occ := t.frameOccurrence[name]
		t.frameOccurrence[name] = occ + 1
		num := 0
		for _, entry := range t.entries {
			if entry.name != name {
				continue
			}
			if num == occ {
				return entry.index, entry.callID, false
			}
			num++
		}
	}
	// 安全限制：防止空 name 或其他异常导致 entries 无限增长
	if len(t.entries) > 64 {
		t.entries = nil
		t.nextIndex = 0
	}
	idx := t.nextIndex
	t.nextIndex++
	callID := "call_" + reqID()
	t.entries = append(t.entries, toolCallEntry{
		name:   name,
		callID: callID,
		index:  idx,
	})
	return idx, callID, true
}

// HasCalls 返回 tracker 是否处理过至少一个 tool call。
func (t *StreamToolCallTracker) HasCalls() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.entries) > 0
}

// reqID 生成 24 位十六进制 ID。
func reqID() string {
	if !streamReqIDFallback.Load() {
		b := make([]byte, 12)
		if _, err := cryptorand.Read(b); err == nil {
			return hex.EncodeToString(b)
		}
		streamReqIDFallback.Store(true)
	}
	return fmt.Sprintf("%016x%08x", uint64(time.Now().UnixNano()), streamReqIDCounter.Add(1))
}
