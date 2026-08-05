package vertex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
)

// scanStream 跨 chunk 增量扫描花括号配对，逐个完整 JSON 对象回调 onObject（O(n)）。
//
// M27 增量扫描：
// 跨网络 chunk 维护 scanPos/braceCount/inString/escape 状态，下个 chunk 从上次扫到的位置
// 续扫，而非每来一个 chunk 都从 startIdx 重扫整个 buffer（旧逻辑 O(n²）。逐字节逻辑等价。
//
// onObject 返回 (stop, err)：stop=true（命中真实 finishReason）即正常结束扫描；客户端断开由 ctx.Err() 路径处理；
// err 非 nil 即中断并上抛（上游错误）。
var scanBufferPool = sync.Pool{
	New: func() any {
		buf := make([]byte, 16*1024)
		return &buf
	},
}

func scanStream(ctx context.Context, body io.Reader, onObject func(map[string]any) (bool, error), touchActivity func()) error {
	reader := bufio.NewReader(body)
	bufPtr := scanBufferPool.Get().(*[]byte)
	defer scanBufferPool.Put(bufPtr)
	readBuf := *bufPtr

	var buffer []byte
	scanPos := 0  // 已扫到的位置（buffer 内），下个网络 chunk 从这里续扫。
	startIdx := 0 // 当前对象的起始 '{' 位置。
	braceCount := 0
	inString := false
	escape := false

	const (
		maxBufferSize     = 4 * 1024 * 1024
		hardMaxBufferSize = 64 * 1024 * 1024
	)

	for {
		n, readErr := reader.Read(readBuf)
		if n > 0 {
			if touchActivity != nil {
				touchActivity()
			}
			buffer = append(buffer, readBuf[:n]...)

			if len(buffer) > hardMaxBufferSize {
				return fmt.Errorf("scanStream: single object exceeds hard buffer limit of %d bytes", hardMaxBufferSize)
			}

			if len(buffer) > maxBufferSize && braceCount == 0 {
				log.Printf("[DEBUG-scan] buffer exceeded %d bytes, resetting from scanPos=%d", maxBufferSize, scanPos)
				buffer = buffer[scanPos:]
				scanPos = 0
				startIdx = 0
			}

			for {
				if scanPos == 0 {
					// 找下一个对象的起始 '{'。
					startIdx = bytes.IndexByte(buffer, '{')
					if startIdx == -1 {
						buffer = buffer[:0]
						break
					}
					scanPos = startIdx
					braceCount = 0
					inString = false
					escape = false
				}

				endIdx := -1
				for i := scanPos; i < len(buffer); i++ {
					ch := buffer[i]
					if escape {
						escape = false
						continue
					}
					if ch == '\\' {
						escape = true
						continue
					}
					if ch == '"' {
						inString = !inString
						continue
					}
					if !inString {
						if ch == '{' {
							braceCount++
						} else if ch == '}' {
							braceCount--
							if braceCount == 0 {
								endIdx = i
								break
							}
						}
					}
				}

				if endIdx != -1 {
					jsonStr := buffer[startIdx : endIdx+1]
					// 复制出对象后裁剪 buffer（drop 已消费前缀），重置扫描状态。
					buffer = buffer[endIdx+1:]
					scanPos = 0

					obj := parseJSONObject(jsonStr)
					if obj != nil {
						stop, err := onObject(obj)
						if err != nil {
							return err
						}
						if stop {
							return nil
						}
					} else {
						log.Printf("[WARN-scanStream] 无法解析的 JSON 数据块 (前 200 字节): %s, raw_len: %d",
							truncateStr(string(jsonStr), 200), len(jsonStr))
					}
					// jsonStr 解析失败（半截/畸形）静默跳过。
				} else {
					// 未扫到完整对象：记下已扫位置，下个 chunk 续扫，不重扫前缀。
					scanPos = len(buffer)
					break
				}
			}
		}

		if readErr != nil {
			if ctx.Err() != nil {
				return context.Canceled
			}
			if errors.Is(readErr, context.Canceled) || errors.Is(readErr, context.DeadlineExceeded) {
				return fmt.Errorf("error: %w", readErr)

			}
			// EOF 或读错误：流结束（正常 EOF 直接返回 nil，上层会按 got_content 判定空响应）。
			return nil
		}
	}
}

// parseJSONObject 把单个 JSON 对象字符串解析为 map，失败返回 nil（解析失败跳过）。
func parseJSONObject(b []byte) map[string]any {
	b = bytes.TrimSpace(b)
	var obj map[string]any
	if err := json.Unmarshal(b, &obj); err != nil {
		return nil
	}
	return obj
}

// truncateStr 截断字符串到 max 长度，用于日志输出避免巨型 payload 刷屏。
func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
