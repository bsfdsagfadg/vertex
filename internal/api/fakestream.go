package api

import "net/http"

// 本文件实现假非流：模型名带 "假非流-" 前缀时，先完整非流式生成、再以单包 SSE 返回。
// OpenAI 端点与 Gemini 端点（use_fake 分支）共用此机制。

// sseWriter 是一个带 flush 的 SSE 行写出器；write 返回 false 表示客户端断开。
// 仅当首次写入数据时才发送 200 OK 及 SSE Headers，允许在首帧前返回 JSON 错误。
type sseWriter struct {
	w           http.ResponseWriter
	flush       func()
	wroteHeader bool
	contentType string
}

// ensureHeader 在首次写入时延迟发送 200 OK 和 Content-Type 等 SSE headers。
func (sw *sseWriter) ensureHeader() {
	if sw.wroteHeader {
		return
	}
	sw.w.Header().Set("Content-Type", sw.contentType)
	sw.w.Header().Set("Cache-Control", "no-cache")
	sw.w.Header().Set("Connection", "keep-alive")
	sw.w.Header().Set("Transfer-Encoding", "chunked")
	sw.w.Header().Set("X-Accel-Buffering", "no")
	sw.w.WriteHeader(http.StatusOK)
	sw.wroteHeader = true
}

// hasWritten 返回是否已发送过 SSE headers（即是否有过有效写入）。
func (sw *sseWriter) hasWritten() bool {
	return sw.wroteHeader
}

// write 写一条原始字符串并 flush。返回 false 表示客户端断开。
func (sw *sseWriter) write(line string) bool {
	sw.ensureHeader()
	if _, err := sw.w.Write([]byte(line)); err != nil {
		return false
	}
	if sw.flush != nil {
		sw.flush()
	}
	return true
}
