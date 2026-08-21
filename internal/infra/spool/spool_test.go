package spool

import (
	"bytes"
	"io"
	"os"
	"sync"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/infra/jsonx"
)

// TestEncodeJSONMatchesJsonx 验证 EncodeJSON 与 jsonx.Marshal 逐字节一致
// （关 HTML 转义 + 去尾换行），保证发往上游的请求体不变。
func TestEncodeJSONMatchesJsonx(t *testing.T) {
	cases := []any{
		map[string]any{"a": float64(1), "b": "x<y>&z"}, // 含 < > & 验证不转义
		map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "你好"}}}}},
		"plain string",
		[]any{float64(1), float64(2), float64(3)},
	}
	for i, v := range cases {
		buf, err := EncodeJSON(v)
		if err != nil {
			t.Fatalf("case %d EncodeJSON: %v", i, err)
		}
		r, _ := buf.Reader()
		got, _ := io.ReadAll(r)
		want, _ := jsonx.Marshal(v)
		if string(got) != string(want) {
			t.Fatalf("case %d 不一致:\n got=%q\nwant=%q", i, got, want)
		}
		_ = buf.Close()
	}
}

// TestBufferMemOnly 验证内存缓冲：写入、读回完整、Len 正确、不落盘。
func TestBufferMemOnly(t *testing.T) {
	if SpilledBytes() != 0 {
		t.Fatal("SpilledBytes 应为 0")
	}
	SetMaxSpillBytes(123) // 不溢出磁盘，调用不应改变行为

	b := New()
	if _, err := b.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write([]byte("0123456789")); err != nil {
		t.Fatal(err)
	}
	if b.Len() != 15 {
		t.Fatalf("Len 应为 15，got %d", b.Len())
	}
	r, err := b.Reader()
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(r)
	if string(got) != "hello0123456789" {
		t.Fatalf("读回内容错: %q", got)
	}
	if SpilledBytes() != 0 {
		t.Fatal("写入后 SpilledBytes 仍应为 0（不落盘）")
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestWrite_SpillToDisk 验证小阈值下写入超限数据会落盘、读回一致、Close 清理临时文件。
func TestWrite_SpillToDisk(t *testing.T) {
	prev := maxMemSize
	t.Cleanup(func() { SetMaxSpillBytes(prev) })
	SetMaxSpillBytes(1024)

	b := New()
	payload := bytes.Repeat([]byte("0123456789"), 1024) // 10KB
	if _, err := b.Write(payload); err != nil {
		t.Fatal(err)
	}
	if b.Len() != int64(len(payload)) {
		t.Fatalf("Len 应为 %d，got %d", len(payload), b.Len())
	}
	r, err := b.Reader()
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(r)
	if !bytes.Equal(got, payload) {
		t.Fatal("落盘后读回内容不一致")
	}
	if SpilledBytes() == 0 {
		t.Fatal("SpilledBytes 应 > 0（已落盘）")
	}
	filePath := b.filePath
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Fatalf("临时文件 %q 应已被删除", filePath)
	}
}

// TestSpillConcurrent 并发溢出验证：多个 Buffer 并行写入超限数据，SpilledBytes
// 基于前后增量断言（不假设全局计数从零开始）；配合 -race 验证无数据竞争。
func TestSpillConcurrent(t *testing.T) {
	prev := maxMemSize
	t.Cleanup(func() { SetMaxSpillBytes(prev) })
	SetMaxSpillBytes(512)

	before := SpilledBytes()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := New()
			payload := bytes.Repeat([]byte("x"), 4096)
			if _, err := b.Write(payload); err != nil {
				t.Errorf("Write: %v", err)
			}
			_ = b.Close()
		}()
	}
	wg.Wait()
	if delta := SpilledBytes() - before; delta <= 0 {
		t.Fatalf("并发写后 SpilledBytes 增量应为正，got %d", delta)
	}
}

// TestMaxSpillProvider 验证动态 Provider 优先级、返回 0 表示永不落盘、nil 清除后回退静态阈值。
func TestMaxSpillProvider(t *testing.T) {
	prev := maxMemSize
	t.Cleanup(func() {
		SetMaxSpillBytes(prev)
		SetMaxSpillProvider(nil)
	})
	SetMaxSpillBytes(0)

	// Provider 生效：阈值来自 Provider
	SetMaxSpillProvider(func() int64 { return 256 })
	b := New()
	before := SpilledBytes()
	if _, err := b.Write(bytes.Repeat([]byte("y"), 512)); err != nil {
		t.Fatal(err)
	}
	_ = b.Close()
	if SpilledBytes()-before <= 0 {
		t.Fatal("Provider 阈值下应触发落盘")
	}

	// Provider 返回 0：永不落盘
	SetMaxSpillProvider(func() int64 { return 0 })
	b2 := New()
	before = SpilledBytes()
	if _, err := b2.Write(bytes.Repeat([]byte("z"), 8192)); err != nil {
		t.Fatal(err)
	}
	_ = b2.Close()
	if SpilledBytes()-before != 0 {
		t.Fatal("Provider 返回 0 表示永不落盘")
	}

	// nil 清除 Provider：回退静态阈值（此处静态 0 亦永不落盘，验证无 panic 且回退生效）
	SetMaxSpillProvider(nil)
	if limit := getMaxMemSize(); limit != 0 {
		t.Fatalf("清除 Provider 后应回退静态阈值 0，got %d", limit)
	}
	SetMaxSpillBytes(64)
	if limit := getMaxMemSize(); limit != 64 {
		t.Fatalf("静态阈值应生效 64，got %d", limit)
	}
}
