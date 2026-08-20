package api

import "net/http"

// 本文件实现假流式：模型名带 "假流式-"/"fake-" 前缀时，先完整非流式生成、再切片按 SSE 推。
// OpenAI 端点与 Gemini 端点（use_fake 分支）共用此机制。

// splitIntoRuneChunks 把文本切成若干分片用于假流式推送，分片数约为 8。
//
// 必须按 rune（完整字符）切分，不能按字节：多字节 UTF-8 字符（如汉字、emoji）若在字节
// 边界被截断，半个字符经 JSON 序列化会被替换成 U+FFFD（），客户端收到的就是乱码。
// 空文本返回 nil。
func splitIntoRuneChunks(text string) []string {
	runes := []rune(text)
	if len(runes) == 0 {
		return nil
	}
	chunkSize := 1
	if cs := len(runes) / 8; cs > 1 {
		chunkSize = cs
	}
	chunks := make([]string, 0, (len(runes)+chunkSize-1)/chunkSize)
	for i := 0; i < len(runes); i += chunkSize {
		end := i + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[i:end]))
	}
	return chunks
}

// sseWriter 是一个带 flush 的 SSE 行写出器；write 返回 false 表示客户端断开。
type sseWriter struct {
	w           http.ResponseWriter
	contentType string
	wroteHeader bool
	flush       func()
}

func (sw *sseWriter) ensureHeader() {
	if sw.wroteHeader {
		return
	}
	sw.wroteHeader = true
	ct := sw.contentType
	if ct == "" {
		ct = "text/event-stream"
	}
	sw.w.Header().Set("Content-Type", ct)
	sw.w.Header().Set("Cache-Control", "no-store")
	sw.w.Header().Set("Connection", "keep-alive")
	sw.w.Header().Set("X-Accel-Buffering", "no")
	sw.w.WriteHeader(http.StatusOK)
}

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
