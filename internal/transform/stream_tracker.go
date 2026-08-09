package transform

import (
	cryptorand "crypto/rand"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"
)

// 本文件是流式工具调用 tracker 的独立载体：不再依赖旧的 map 形态
// （旧 stream.go 的 ProcessFunctionCall 接收 map 参数），直接以函数名
// 为输入，供 typed 流式链路（stream_typed.go 与 api 层）共用。

var (
	streamReqIDFallback atomic.Bool
	streamReqIDCounter  atomic.Uint64
)

// StreamToolCallTracker 跟踪流式工具调用 ID/Index，确保同一个 functionCall 在
// 增量帧中 id 和 index 保持稳定（符合 OpenAI SSE 规范）。
type StreamToolCallTracker struct {
	entries   []toolCallEntry
	nextIndex int
}

type toolCallEntry struct {
	name   string
	callID string
	index  int
}

// NewStreamToolCallTracker 构造 tracker。
func NewStreamToolCallTracker() *StreamToolCallTracker {
	return &StreamToolCallTracker{}
}

// ProcessFunctionCall 返回稳定 (index, callID, isNew)。首次遇到该 name 时生成
// 新 ID 和 index；后续复用已有值。name 为空时始终按新调用处理。
func (t *StreamToolCallTracker) ProcessFunctionCall(name string) (int, string, bool) {
	if name != "" {
		for _, entry := range t.entries {
			if entry.name == name {
				return entry.index, entry.callID, false
			}
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
	return t != nil && len(t.entries) > 0
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