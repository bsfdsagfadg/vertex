// Package spool 提供"先写后读"的字节缓冲，用于承载请求/媒体体的序列化输出。
//
// 缓冲优先驻留内存；超出 SetMaxSpillBytes 设定的阈值后自动溢出到 os.TempDir 临时文件，
// 释放内存。调用方无需感知缓冲介质（Write/Reader/Close 接口一致）。
package spool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

var (
	//nolint:gochecknoglobals // Internal spool state
	maxMemSize int64 // 0 = unlimited（永不落盘）
	//nolint:gochecknoglobals // Internal spool state
	spilledBytes int64
	//nolint:gochecknoglobals // Dynamic spill provider for hot-reload
	maxSpillProvider func() int64
)

// SetMaxSpillBytes 设置磁盘溢出阈值（字节数）；0 或负数表示永不落盘。
func SetMaxSpillBytes(limit int64) {
	maxMemSize = limit
}

// SetMaxSpillProvider 注册一个动态提供 spill 阈值的函数（支持热重载）。
// 非 nil 时优先于 SetMaxSpillBytes 的静态值。
func SetMaxSpillProvider(fn func() int64) {
	maxSpillProvider = fn
}

func getMaxMemSize() int64 {
	if maxSpillProvider != nil {
		return maxSpillProvider()
	}
	return maxMemSize
}

// SpilledBytes 返回进程启动以来写入临时文件的累计字节数。
func SpilledBytes() int64 { return spilledBytes }

// Buffer 是"先写后读"字节缓冲，支持自动磁盘溢出。
//
// 非并发安全——约定单个逻辑请求内串行 Write→Reader→Close 使用。
type Buffer struct { //nolint:govet
	mem          []byte
	file         *os.File
	filePath     string
	totalWritten int64
	spilled      bool
}

// New 构造一个空 Buffer。
func New() *Buffer { return &Buffer{} } //nolint:exhaustruct

// Write 实现 io.Writer：优先写入内存，超出 maxMemSize 后溢出到临时文件。
func (b *Buffer) Write(p []byte) (int, error) {
	if b.spilled {
		n, err := b.file.Write(p)
		if n > 0 {
			b.totalWritten += int64(n)
		}
		return n, err

	}

	if limit := getMaxMemSize(); limit > 0 && int64(len(b.mem)+len(p)) > limit {
		tmp, err := os.CreateTemp("", "spool-*")
		if err != nil {
			return 0, fmt.Errorf("error: %w", err)

		}
		b.file = tmp
		b.filePath = tmp.Name()
		b.spilled = true

		if len(b.mem) > 0 {
			if _, err := b.file.Write(b.mem); err != nil {
				return 0, fmt.Errorf("error: %w", err)

			}
		}
		n, err := b.file.Write(p)
		if n > 0 {
			b.totalWritten = int64(len(b.mem)) + int64(n)
		}
		b.mem = nil
		spilledBytes += b.totalWritten
		return n, err

	}

	b.mem = append(b.mem, p...)
	b.totalWritten += int64(len(p))
	return len(p), nil
}

// Len 返回已写入的总字节数（含溢出到磁盘的部分）。
func (b *Buffer) Len() int64 {
	if b.spilled {
		return b.totalWritten
	}
	return int64(len(b.mem))
}

// Reader 返回从头读取已写内容的 io.Reader。
// 已溢出时 seek 到文件开头返回 *os.File，否则返回 bytes.Reader。
func (b *Buffer) Reader() (io.Reader, error) {
	if b.spilled {
		if _, err := b.file.Seek(0, 0); err != nil {
			return nil, fmt.Errorf("error: %w", err)

		}
		return b.file, nil
	}
	return bytes.NewReader(b.mem), nil
}

// Close 释放内存，关闭并删除临时文件（幂等）。
func (b *Buffer) Close() error {
	b.mem = nil
	if b.file != nil {
		_ = b.file.Close()
		if b.filePath != "" {
			_ = os.Remove(b.filePath)
		}
		b.file = nil
		b.filePath = ""
		b.spilled = false
	}
	return nil
}

func (b *Buffer) trimTrailingNewline() {
	if b.spilled {
		if b.totalWritten > 0 {
			if _, err := b.file.Seek(-1, 2); err == nil {
				lastByte := make([]byte, 1)
				if _, err := b.file.Read(lastByte); err == nil && lastByte[0] == '\n' {
					_ = b.file.Truncate(b.totalWritten - 1)
					b.totalWritten--
				}
			}
		}
		return
	}
	if n := len(b.mem); n > 0 && b.mem[n-1] == '\n' {
		b.mem = b.mem[:n-1]
	}
}

// EncodeJSON 把 v 关闭 HTML 转义地序列化进一个 Buffer 并去掉尾换行，行为对齐 jsonx.Marshal。
// 返回的 Buffer 用完必须 Close。出错时已自行 Close 并返回 nil。
func EncodeJSON(v any) (*Buffer, error) {
	b := New()
	enc := json.NewEncoder(b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		_ = b.Close()
		return nil, fmt.Errorf("error: %w", err)

	}
	b.trimTrailingNewline()
	return b, nil
}
