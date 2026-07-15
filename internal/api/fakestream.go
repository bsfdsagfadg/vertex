package api

import "net/http"

// 本文件实现假非流：模型名带 "假非流-" 前缀时，先完整非流式生成、再以单包 SSE 返回。
// OpenAI 端点与 Gemini 端点（use_fake 分支）共用此机制。

// sseWriter 是一个带 flush 的 SSE 行写出器；write 返回 false 表示客户端断开。
type sseWriter struct {
	w     http.ResponseWriter
	flush func()
}

// write 写一条原始字符串并 flush。返回 false 表示客户端断开。
func (sw *sseWriter) write(line string) bool {
	if _, err := sw.w.Write([]byte(line)); err != nil {
		return false
	}
	if sw.flush != nil {
		sw.flush()
	}
	return true
}
