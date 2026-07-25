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
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/spool"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

// sessionTimeoutFromContext 从 context 的 deadline 推导 Session 的超时秒数。
// 至少返回 1 秒（tls-client.WithTimeoutSeconds 只接受正秒），但 context 的 deadline
// 仍由 Session.Do 优先检查；该值仅用于构造传输层超时。
func sessionTimeoutFromContext(ctx context.Context, defaultSec int) int {
	if d, ok := ctx.Deadline(); ok {
		rem := time.Until(d)
		if rem <= 0 {
			return 1
		}
		sec := int(rem.Seconds())
		if sec < 1 {
			return 1
		}
		return sec
	}
	return defaultSec
}

// finishReasonUnspecified 是匿名 batchGraphql 每帧都携带的 protobuf 默认值（无意义）。
//
// 流式最关键红线（红线⑤）：匿名端点每个增量帧都带 finishReason="FINISH_REASON_UNSPECIFIED"，
// 只有真正结束时才给 STOP/MAX_TOKENS/SAFETY/PROHIBITED_CONTENT 等真实值。
// **绝不能据 UNSPECIFIED 发 finish 事件或结束流**：首帧就命中会立即截断（血泪教训）。
// 只有 finishReason 非空且 != FINISH_REASON_UNSPECIFIED 才主动结束流（否则上游不及时关连接
// 会挂死到 180s 超时）。
const finishReasonUnspecified = "FINISH_REASON_UNSPECIFIED"

// StreamChunk 是真流式中 yield 的单个增量。要么是 Gemini 数据 chunk，要么是错误。
//
// 正常 yield Gemini dict，所有重试耗尽时 yield {"error": {...}}（routes 层据此
// 发 OAI error 事件 + [DONE]）。
type StreamChunk struct {
	// Data 是清洗后的 Gemini 增量（candidates/usageMetadata/...），Err==nil 时有效。
	Data map[string]any
	// Err 非 nil 表示重试耗尽、对外报错（yield error dict）。
	Err *VertexError
}

// StreamChat 真流式入口。
//
// 通过 yield 回调推送增量：回调返回 false 表示客户端断开/上层要求停止，立即终止。
// 单 session复用 + response 排干防串流。重试逻辑与非流式对齐，但 content_yielded 后
// 不再重试（已发出的内容不能重来）。ctx 取消（客户端断开/优雅关闭）时干净结束流：
// 重试退避被打断、上游流连接中断，不再空转。
func (c *VertexAIClient) StreamChat(ctx context.Context, model string, geminiPayload map[string]any, yield func(StreamChunk) bool) {
	op := func(ctx context.Context, proxyURI string) <-chan StreamChunk {
		ch := make(chan StreamChunk, 64)
		go func() {
			defer close(ch)
			c.executeStreamingWithRetries(ctx, model, geminiPayload, proxyURI, func(chunk StreamChunk) bool {
				select {
				case ch <- chunk:
					return true
				case <-ctx.Done():
					return false
				}
			})
		}()
		return ch
	}
	StreamParallel(ctx, c.cfg, op, yield)
}

func (c *VertexAIClient) executeStreamingWithRetries(ctx context.Context, model string, geminiPayload map[string]any, proxyURI string, yield func(StreamChunk) bool) {
	if ctx.Err() != nil {
		yield(StreamChunk{Err: NewContextError(ctx.Err())})
		return
	}

	cfg := c.cfg
	maxRetries := cfg.MaxRetries()
	if cfg.ParallelPoolEnabled() && !cfg.ParallelPoolRetryEnabled() {
		maxRetries = 0
	}
	contentYielded := false
	var lastError *VertexError

	reqID := RequestIDFromContext(ctx)
	sess, err := c.net.CreateSession(sessionTimeoutFromContext(ctx, 180), proxyURI, reqID)
	if err != nil {
		yield(StreamChunk{Err: NewInternalError("create session: " + err.Error(), nil)})
		return
	}
	defer func() { sess.Close() }()

	recaptchaToken := ""
	isFirstAuth := true
	attempt := 0

retryLoop:
	for attempt <= maxRetries {
		log.Printf("[Vertex] [StreamChat] 开始尝试 (Attempt %d/%d), 模型=%s, 请求ID=%s, 代理=%s", attempt, maxRetries, model, reqID, nodes.GetNodeName(proxyURI))
		if recaptchaToken == "" {
			tok, _ := c.pool.GetTokenWithProxy(ctx, proxyURI)
			recaptchaToken = tok
			isFirstAuth = true
		}
		if recaptchaToken == "" {
			nodes.RecordTest(proxyURI, false, 0, "Could not fetch recaptcha token")
			lastError = NewAuthenticationError("Could not fetch recaptcha token for node", nil)
			break retryLoop
		}

		// 单次流式尝试：把增量 yield 给上层，统计本次 attempt yield 的 chunk 数。
		chunkCount := 0
		attemptErr := c.executeStreamingAttempt(ctx, sess, model, geminiPayload, recaptchaToken, isFirstAuth, func(ch map[string]any) bool {
			chunkCount++
			contentYielded = true
			return yield(StreamChunk{Data: ch})
		})

		if attemptErr == nil {
			// 本次尝试无错误。若 0 chunk 且仍是首帧 → 认证重试（同 token 再打一次）。
			if chunkCount == 0 && isFirstAuth {
				isFirstAuth = false
				if err := sleepCtx(ctx, 500*time.Millisecond); err != nil {
					break retryLoop
				}
				continue
			}
			return
		}

		if errors.Is(attemptErr, context.Canceled) || errors.Is(attemptErr, context.DeadlineExceeded) {
			lastError = NewContextError(attemptErr)
			break retryLoop
		}

		ve := asVertexError(attemptErr)
		switch {
		case ve != nil && ve.Kind == "auth":
			isVerifyFail := strings.Contains(ve.Message, "Failed to verify action") ||
				strings.Contains(ve.Message, "The caller does not have permission")
			if isFirstAuth && isVerifyFail {
				// 首次认证重试：token 不清空，同一 token 再打一次（匿名端点首帧预期 verify-fail）。
				isFirstAuth = false
				if err := sleepCtx(ctx, 500*time.Millisecond); err != nil {
					break retryLoop
				}
				continue
			}
			recaptchaToken = ""
			isFirstAuth = true
			lastError = ve
			if contentYielded || attempt >= maxRetries {
				break retryLoop
			}
			attempt++
			if err := sleepCtx(ctx, time.Second); err != nil {
				break retryLoop
			}

		case ve != nil && ve.Kind == "ratelimit":
			lastError = ve
			if contentYielded || attempt >= maxRetries {
				log.Printf("[Vertex] [StreamChat] (Attempt %d/%d) 节点 %s 触发 429 失败, 请求ID=%s, 代理=%s", attempt, maxRetries, model, reqID, nodes.GetNodeName(proxyURI))
				break retryLoop
			}
			// 429：销毁旧 session 重建新的，换 token。
			sess.Close()
			newSess, e := c.net.CreateSession(sessionTimeoutFromContext(ctx, 180), proxyURI, reqID)
			if e != nil {
				yield(StreamChunk{Err: NewInternalError("recreate session: " + e.Error(), nil)})
				return
			}
			sess = newSess
			recaptchaToken = ""

			// 避免过快重试 429 导致 token 浪费 and 节点持续封禁
			wait := ve.RetryAfter
			if wait <= 0 {
				wait = min(10, 1+attempt)
			}
			log.Printf("[Vertex] [StreamChat] (Attempt %d/%d) 节点 %s 触发 429 将重试 (延迟 %ds), 请求ID=%s, 代理=%s", attempt, maxRetries, model, wait, reqID, nodes.GetNodeName(proxyURI))
			attempt++
			if err := sleepCtx(ctx, time.Duration(wait)*time.Second); err != nil {
				break retryLoop
			}

		case ve != nil:
			lastError = ve
			// 【关键改动】：如果是网络不通等内部错误，直接熔断并停止重试。
			if ve.Kind == "internal" || !ve.IsRetryable() || contentYielded || attempt >= maxRetries {
				log.Printf("[Vertex] [StreamChat] (Attempt %d/%d) 节点 %s 触发异常错误失败: [%s] %s, 请求ID=%s, 代理=%s", attempt, maxRetries, model, ve.Kind, ve.Message, reqID, nodes.GetNodeName(proxyURI))
				break retryLoop
			}
			log.Printf("[Vertex] [StreamChat] (Attempt %d/%d) 节点 %s 触发异常错误将重试: [%s] %s, 请求ID=%s, 代理=%s", attempt, maxRetries, model, ve.Kind, ve.Message, reqID, nodes.GetNodeName(proxyURI))
			attempt++
			if err := sleepCtx(ctx, backoff(attempt)); err != nil {
				break retryLoop
			}

		default:
			// 【关键改动】：直接终止未知原生错误，移除了多余的 attempt 重新入圈重试。
			lastError = NewInternalError(attemptErr.Error(), nil)
			break retryLoop
		}
	}

	// 所有重试耗尽且没发出过任何内容 → yield 一个 error chunk（末尾 yield error dict）。
	if !contentYielded && lastError != nil {
		yield(StreamChunk{Err: lastError})
	}
}

// executeStreamingAttempt 执行单次流式请求：发请求 → 增量扫描 JSON → 提取 chunk → 过滤 finishReason。
//
// emit 回调把清洗后的 Gemini chunk 推给上层；
// emit 始终返回 true；客户端断开由 ctx 取消触发 read 报错，scanStream 干净结束。
// ctx 绑定 to 上游流连接：ctx 取消时 Body.Read 报错，scanStream 干净结束（返回 nil，不 panic）。
func (c *VertexAIClient) executeStreamingAttempt(ctx context.Context, sess *transport.Session, model string, geminiPayload map[string]any, recaptchaToken string, _ bool, emit func(map[string]any) bool) error {
	reqID := RequestIDFromContext(ctx)
	log.Printf("[Vertex] [executeStreamingAttempt] 准备发送流式请求: 模型=%s, 请求ID=%s, 代理=%s", model, reqID, nodes.GetNodeName(sess.ProxyURI))
	cfg := c.cfg
	newBody := buildRequestPayload(model, geminiPayload, recaptchaToken, cfg)
	// 上游请求 payload 序列化到 spool 缓冲（大媒体自动落盘）。流式：请求体在 DoStream 发送期被读取，
	// 缓冲存活到本函数返回（整个流消费完）后由 defer Close 删除临时文件。
	buf, err := spool.EncodeJSON(newBody)
	if err != nil {
		return NewInternalError("marshal payload: " + err.Error(), nil)
	}
	defer func() { _ = buf.Close() }()
	reader, err := buf.Reader()
	if err != nil {
		return NewInternalError("spool reader: " + err.Error(), nil)
	}
	header := transport.XHRHeaders(
		"application/json", "*/*",
		"https://console.cloud.google.com", "https://console.cloud.google.com/", "cross-site",
	)

	sr, err := sess.DoStream(ctx, "POST", c.getBatchGraphqlURL(), header, reader)
	if err != nil {
		return classifyNetworkError(err)
	}
	defer sr.Close() // 排干 → close，防串流。

	// ═══ 唯一中断调度点 ═══
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			sr.Body.Close()
		case <-done:
		}
	}()
	// ══════════════════════

	// HTTP 错误：读完 error body 后按状态映射（与非流式 executeCompleteRequest 一致）。
	if sr.StatusCode != 200 {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(sr.Body)
		errText := buf.String()
		if cfg.DebugMode() {
			debugReq, _ := json.Marshal(newBody)
			log.Printf("[DEBUG] [StreamChat] HTTP 报错! 状态码: %d", sr.StatusCode)
			log.Printf("[DEBUG] [StreamChat] 完整请求体: %s", string(debugReq))
			log.Printf("[DEBUG] [StreamChat] 上游回复: %s", errText)
		} else if sr.StatusCode == 400 {
			debugBody, _ := json.Marshal(newBody["variables"])
			log.Printf("[Vertex] [Stream] 收到 400 Bad Request, Variables Payload: %s", string(debugBody))
		}

		if sr.StatusCode == 401 || sr.StatusCode == 403 ||
			strings.Contains(errText, "Failed to verify action") ||
			strings.Contains(errText, "The caller does not have permission") {
			return NewAuthenticationError("Authentication/Recaptcha failed: " + errText, nil)
		}
		if parsed := parseErrorResponse(errText); parsed != nil {
			parsed.UpstreamResponse = errText
			return parsed
		}
		return raiseForStatus(sr.StatusCode, "", "Upstream Error: "+errText, nil, errText)
	}

	// 增量扫描上游流，逐个完整 JSON 对象提取 chunk。
	scanErr := scanStream(ctx, sr.Body, func(obj map[string]any) (stop bool, err error) {
		// 从单个上游对象提取（可能多个）chunk，逐个 emit；命中真实 finishReason 即结束。
		return processStreamingObject(obj, emit)
	})

	if scanErr != nil && cfg.DebugMode() && !errors.Is(scanErr, context.Canceled) {
		debugReq, _ := json.Marshal(newBody)
		log.Printf("[DEBUG] [StreamChat] 扫描流数据报错! error: %v", scanErr)
		log.Printf("[DEBUG] [StreamChat] 完整请求体: %s", string(debugReq))
	}

	if errors.Is(scanErr, context.Canceled) {
		return scanErr
	}

	if scanErr != nil {
		return classifyNetworkError(scanErr)
	}

	return nil
}

// scanStream 跨 chunk 增量扫描花括号配对，逐个完整 JSON 对象回调 onObject（O(n)）。
//
// M27 增量扫描：
// 跨网络 chunk 维护 scanPos/braceCount/inString/escape 状态，下个 chunk 从上次扫到的位置
// 续扫，而非每来一个 chunk 都从 startIdx 重扫整个 buffer（旧逻辑 O(n²）。逐字节逻辑等价。
//
// onObject 返回 (stop, err)：stop=true（命中真实 finishReason）即正常结束扫描；客户端断开由 ctx.Err() 路径处理；
// err 非 nil 即中断并上抛（上游错误）。
func scanStream(ctx context.Context, body io.Reader, onObject func(map[string]any) (bool, error)) error {
	reader := bufio.NewReader(body)
	readBuf := make([]byte, 16*1024)

	var buffer []byte
	scanPos := 0  // 已扫到的位置（buffer 内），下个网络 chunk 从这里续扫。
	startIdx := 0 // 当前对象的起始 '{' 位置。
	braceCount := 0
	inString := false
	escape := false

	const (
		maxBufferSize    = 4 * 1024 * 1024
		hardMaxBufferSize = 64 * 1024 * 1024
	)

	for {
		n, readErr := reader.Read(readBuf)
		if n > 0 {
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
	var obj map[string]any
	if err := json.Unmarshal(b, &obj); err != nil {
		return nil
	}
	return obj
}

// processStreamingObject 从单个上游 JSON 对象提取增量 chunk。
//
// 先识别 results 内的错误（"Failed to verify action" → AuthenticationError 触发重试），
// 再 unwrap data.ui.streamGenerateContentAnonymous，最后 _extract_chunk 清洗后 emit。
// 返回 (stop, err)：emit 出真实 finishReason 即 stop=true（结束扫描）；客户端断开由 ctx.Err() 路径处理；上游错误即 err 非 nil。
func processStreamingObject(obj map[string]any, emit func(map[string]any) bool) (bool, error) {
	results, _ := obj["results"].([]any)
	for _, rRaw := range results {
		result, ok := rRaw.(map[string]any)
		if !ok {
			continue
		}

		// results 内的错误处理。
		if errs, ok := result["errors"].([]any); ok && len(errs) > 0 {
			errMsg := ""
			if first, ok := errs[0].(map[string]any); ok {
				errMsg = toStr(first["message"])
			} else {
				errMsg = toStr(errs[0])
			}
			if strings.Contains(errMsg, "Failed to verify action") ||
				strings.Contains(errMsg, "The caller does not have permission") {
				return false, NewAuthenticationError(errMsg, nil)
			}
			if parsed := parseErrorResponse(map[string]any{"errors": errs}); parsed != nil {
				return false, parsed
			}
		}

		data, ok := result["data"].(map[string]any)
		if !ok {
			continue
		}

		// unwrap data.ui.streamGenerateContentAnonymous（匿名端点把载荷包在这里面）。
		if ui, ok := data["ui"].(map[string]any); ok {
			if innerRaw, exists := ui["streamGenerateContentAnonymous"]; exists {
				switch inner := innerRaw.(type) {
				case map[string]any:
					data = inner
				case []any:
					outerMeta := map[string]any{}
					for _, key := range []string{"usageMetadata", "modelVersion", "responseId", "promptFeedback"} {
						if v, ok := data[key]; ok && isTruthyAny(v) {
							outerMeta[key] = v
						}
					}
					// 极少数情况 inner 是 list：逐项 extract+emit，本 result 处理完跳过下方。
					for _, itemRaw := range inner {
						if item, ok := itemRaw.(map[string]any); ok {
							for k, v := range outerMeta {
								if _, exists := item[k]; !exists {
									item[k] = v
								}
							}
							if chunk := extractChunk(item); chunk != nil {
								if done := emitAndCheckFinish(chunk, emit); done {
									return true, nil
								}
							}
						}
					}
					continue
				default:
					continue
				}
			}
		}

		if chunk := extractChunk(data); chunk != nil {
			if done := emitAndCheckFinish(chunk, emit); done {
				return true, nil
			}
		}
	}
	return false, nil
}

// emitAndCheckFinish emit 一个 chunk 并判定是否应结束流。
//
// finishReason 过滤（红线⑤）：emit 后取 chunk 的 candidates[0].finishReason，
// **仅当非空且 != FINISH_REASON_UNSPECIFIED 才主动结束流**。
// 返回 (stopByClient, done)：done=true 表示应停止扫描；stopByClient 区分是客户端断开还是正常 finish。
func emitAndCheckFinish(chunk map[string]any, emit func(map[string]any) bool) (done bool) {
	emit(chunk)
	fr := chunkFinishReason(chunk)
	if fr != "" && fr != finishReasonUnspecified {
		// 真实 finishReason：主动结束（避免上游不关连接挂到 180s）。
		return true
	}
	return false
}

// chunkFinishReason 取 chunk 的 candidates[0].finishReason。
func chunkFinishReason(chunk map[string]any) string {
	cands, ok := chunk["candidates"].([]any)
	if !ok || len(cands) == 0 {
		return ""
	}
	c, ok := cands[0].(map[string]any)
	if !ok {
		return ""
	}
	return toStr(c["finishReason"])
}

// extractChunk 从 Gemini 数据中提取标准化 chunk，清洗畸形嵌套。
//
// 对齐 Python _process_streaming_object：candidates key 存在且非 nil 时保留
// （即使空列表），总是复制 metadata 字段，仅当 chunk完全无字段时返回 nil。
func extractChunk(data map[string]any) map[string]any {
	chunk := map[string]any{}

	if raw, ok := data["candidates"]; ok && raw != nil {
		candidatesRaw, _ := raw.([]any)
		if len(candidatesRaw) > 0 {
			cleaned := make([]any, 0, len(candidatesRaw))
			for _, cRaw := range candidatesRaw {
				candidate, ok := cRaw.(map[string]any)
				if !ok {
					continue
				}
				content, hasContent := candidate["content"].(map[string]any)
				if hasContent {
					parts, ok := content["parts"].([]any)
					if ok {
						cleanedParts := cleanStreamParts(parts)
						cc := shallowCopy(candidate)
						role := toStr(content["role"])
						if role == "" {
							role = "model"
						}
						cc["content"] = map[string]any{"role": role, "parts": cleanedParts}
						cleaned = append(cleaned, cc)
					} else {
						cleaned = append(cleaned, candidate)
					}
				} else {
					cleaned = append(cleaned, candidate)
				}
			}
			if len(cleaned) > 0 {
				chunk["candidates"] = cleaned
			} else {
				chunk["candidates"] = candidatesRaw
			}
		} else {
			chunk["candidates"] = candidatesRaw
		}
	}

	for _, key := range []string{"usageMetadata", "modelVersion", "responseId", "promptFeedback", "createTime"} {
		if v, ok := data[key]; ok && isTruthyAny(v) {
			chunk[key] = v
		}
	}

	if len(chunk) == 0 {
		return nil
	}
	return chunk
}

// cleanStreamParts 清洗 parts 列表，展开畸形嵌套 + 移除 protobuf 空默认字段。
func cleanStreamParts(parts []any) []any {
	cleaned := make([]any, 0, len(parts))
	for _, pRaw := range parts {
		part, ok := pRaw.(map[string]any)
		if !ok {
			continue
		}
		textVal, hasText := part["text"]
		if hasText {
			if _, isStr := textVal.(string); !isStr {
				// 畸形嵌套：text 值是 list/dict 而非字符串，递归提取真正文本。
				extracted := extractTextRecursive(textVal, 0)
				if extracted != "" {
					newPart := cleanPart(part)
					if newPart != nil {
						newPart["text"] = extracted
						cleaned = append(cleaned, newPart)
					}
				}
				continue
			}
		}
		if cp := cleanPart(part); cp != nil {
			cleaned = append(cleaned, cp)
		}
	}
	return cleaned
}

// cleanPart 清洗单个 Gemini part，移除内部 protobuf 空默认字段，仅保留真实内容字段。
func cleanPart(part map[string]any) map[string]any {
	cleaned := shallowCopy(part)

	// 移除内部 protobuf oneof 指示器（always "text" / "inlineData" / "functionCall" / "functionResponse"）
	delete(cleaned, "data")

	// fileData：仅在 uri 为空时移除
	if fd, ok := cleaned["fileData"].(map[string]any); ok {
		if toStr(fd["fileUri"]) == "" && toStr(fd["mimeType"]) == "" {
			delete(cleaned, "fileData")
		}
	}

	// functionCall：name 和 args 都为空/无意义时移除
	if fc, ok := cleaned["functionCall"].(map[string]any); ok {
		hasName := toStr(fc["name"]) != ""
		hasArgs := false
		if args, ok := fc["args"]; ok && args != nil {
			if m, ok := args.(map[string]any); ok && len(m) > 0 {
				hasArgs = true
			}
		}
		if !hasName && !hasArgs {
			delete(cleaned, "functionCall")
		} else if name, ok := fc["name"].(string); ok && name != "" {
			if argStr, ok := fc["args"].(string); ok && argStr != "" {
				var parsed any
				if err := json.Unmarshal([]byte(argStr), &parsed); err == nil {
					fc["args"] = parsed
				}
			}
		}
	}

	// functionResponse：name 和 response 都为空时移除
	if fr, ok := cleaned["functionResponse"].(map[string]any); ok {
		hasName := toStr(fr["name"]) != ""
		hasResp := false
		if resp, ok := fr["response"]; ok && resp != nil {
			if m, ok := resp.(map[string]any); ok && len(m) > 0 {
				hasResp = true
			}
		}
		if !hasName && !hasResp {
			delete(cleaned, "functionResponse")
		} else if respStr, ok := fr["response"].(string); ok && respStr != "" {
			fr["response"] = map[string]any{"result": respStr}
		}
	}

	// inlineData：data 为空时移除
	if id, ok := cleaned["inlineData"].(map[string]any); ok {
		if toStr(id["data"]) == "" {
			delete(cleaned, "inlineData")
		}
	}

	// 支持代码块、代码执行结果透传
	for _, key := range []string{"executableCode", "codeExecutionResult"} {
		if v, ok := cleaned[key]; ok && isTruthyAny(v) {
			return cleaned
		}
	}

	// 如果只剩 thought/thoughtSignature 等非内容标记，返回 nil
	for k := range cleaned {
		switch k {
		case "thought", "thoughtSignature":
			continue
		default:
			return cleaned
		}
	}
	return nil
}

// extractTextRecursive 从嵌套结构中递归提取纯文本，防止无限递归（depth>20 截断）。
func extractTextRecursive(val any, depth int) string {
	if depth > 20 {
		s := toStr(val)
		if len(s) > 500 {
			return s[:500]
		}
		return s
	}
	switch v := val.(type) {
	case string:
		return v
	case map[string]any:
		if t, ok := v["text"]; ok {
			return extractTextRecursive(t, depth+1)
		}
		return ""
	case []any:
		var sb strings.Builder
		for _, item := range v {
			if t := extractTextRecursive(item, depth+1); t != "" {
				sb.WriteString(t)
			}
		}
		return sb.String()
	default:
		return ""
	}
}
