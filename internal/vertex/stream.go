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
	"sync"
	"sync/atomic"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/spool"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

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
// 不再重试（已发出的内容不能重来）。ctx 取消（客户端断开/关闭）时干净结束流：
// 重试退避被打断、上游流连接中断，不再空转。
func (c *VertexAIClient) StreamChat(ctx context.Context, model string, geminiPayload map[string]any, yield func(StreamChunk) bool) {
	op := func(ctx context.Context, proxyURI string) <-chan StreamChunk {
		ch := make(chan StreamChunk, 64)
		// 深度拷贝 geminiPayload，防止并发竞速（StreamParallel / RunRace）时
		// 多个节点协程同时修改或读取同一个 map（引发 concurrent map read and map write 恐慌）
		copiedPayload := deepCopyAny(geminiPayload).(map[string]any)
		go func() {
			defer close(ch)
			c.executeStreamingWithRetries(ctx, model, copiedPayload, proxyURI, func(chunk StreamChunk) bool {
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
	cfg := c.cfg
	maxRetries := cfg.MaxRetries()
	// 单节点重试关闭（并发池开启）：节点内只打 1 次，重试由竞速层换批节点完成。
	if cfg.ParallelPoolEnabled() && !cfg.ParallelPoolRetryEnabled() {
		maxRetries = 0
	}
	// 日志显示智能判断：
	//  - 单节点重试开启：显示节点重试次数 = MaxRetries（Attempt 0/2 → 2/2）
	//  - 单节点重试关闭（竞速换批重试）：显示总尝试次数 = MaxRetries + 1（总轮数 = 重试 + 1）
	attemptDenom := cfg.MaxRetries()
	if !cfg.ParallelPoolRetryEnabled() {
		attemptDenom = cfg.MaxRetries() + 1
	}
	if attemptDenom < 1 {
		attemptDenom = 1
	}
	contentYielded := false
	var lastError *VertexError

	reqID := RequestIDFromContext(ctx)
	sess, err := c.net.CreateSession(cfg.RequestTimeout(), proxyURI, reqID)
	if err != nil {
		yield(StreamChunk{Err: NewInternalError("create session: " + err.Error())})
		return
	}
	defer sess.Close()

	recaptchaToken := ""
	isFirstAuth := true
	attempt := 0

	baseAttempt := 0
	if val, ok := ctx.Value(raceRoundKey{}).(int); ok && (!cfg.ParallelPoolEnabled() || !cfg.ParallelPoolRetryEnabled()) {
		baseAttempt = val
	}

retryLoop:
	for attempt <= maxRetries {
		log.Printf("[Vertex] [StreamChat] 开始尝试 (Attempt %d/%d), 模型=%s, 请求ID=%s, 代理=%s", attempt+baseAttempt, attemptDenom, model, reqID, nodes.GetNodeName(proxyURI))
		if recaptchaToken == "" {
			tok, _ := c.pool.GetTokenWithProxy(proxyURI)
			recaptchaToken = tok
			isFirstAuth = true
		}
		if recaptchaToken == "" {
			if attempt == maxRetries {
				lastError = NewAuthenticationError("Could not fetch recaptcha token.")
				break retryLoop
			}
			attempt++
			if err := sleepCtx(ctx, time.Second); err != nil {
				break retryLoop // ctx 取消：客户端已断开，停止重试
			}
			continue
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
				log.Printf("[Vertex] [StreamChat] (Attempt %d/%d) 节点 %s 触发 429 失败, 请求ID=%s, 代理=%s", attempt+baseAttempt, attemptDenom, model, reqID, nodes.GetNodeName(proxyURI))
				break retryLoop
			}
			// 429：销毁旧 session 重建新的，换 token。
			sess.Close()
			newSess, e := c.net.CreateSession(cfg.RequestTimeout(), proxyURI, reqID)
			if e != nil {
				yield(StreamChunk{Err: NewInternalError("recreate session: " + e.Error())})
				return
			}
			sess = newSess
			recaptchaToken = ""

			// 避免过快重试 429 导致 token 浪费 and 节点持续封禁
			wait := ve.RetryAfter
			if wait <= 0 {
				wait = min(10, 1+attempt)
			}
			log.Printf("[Vertex] [StreamChat] (Attempt %d/%d) 节点 %s 触发 429 将重试 (延迟 %ds), 请求ID=%s, 代理=%s", attempt+baseAttempt, attemptDenom, model, wait, reqID, nodes.GetNodeName(proxyURI))
			attempt++
			if err := sleepCtx(ctx, time.Duration(wait)*time.Second); err != nil {
				break retryLoop
			}

		case ve != nil:
			lastError = ve
			// 内部实现错误直接停止；明确分类的临时网络/上游错误按策略重试。
			if ve.Kind == "internal" || !ve.IsRetryable() || contentYielded || attempt >= maxRetries {
				log.Printf("[Vertex] [StreamChat] (Attempt %d/%d) 节点 %s 触发异常错误失败: [%s] %s, 请求ID=%s, 代理=%s", attempt+baseAttempt, attemptDenom, model, ve.Kind, ve.Message, reqID, nodes.GetNodeName(proxyURI))
				break retryLoop
			}
			log.Printf("[Vertex] [StreamChat] (Attempt %d/%d) 节点 %s 触发异常错误将重试: [%s] %s, 请求ID=%s, 代理=%s", attempt+baseAttempt, attemptDenom, model, ve.Kind, ve.Message, reqID, nodes.GetNodeName(proxyURI))
			attempt++
			if err := sleepCtx(ctx, backoff(attempt)); err != nil {
				break retryLoop
			}

		default:
			// 【关键改动】：直接终止未知原生错误，移除了多余的 attempt 重新入圈重试。
			lastError = NewInternalError(attemptErr.Error())
			break retryLoop
		}
	}

	// 所有重试耗尽且没发出过任何内容 → yield 一个 error chunk（末尾 yield error dict）。
	if !contentYielded && lastError != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			lastError = NewContextError(ctxErr)
		}
		yield(StreamChunk{Err: lastError})
	}
}

type idleTouchingReader struct {
	r     io.Reader
	touch func()
}

func (ir *idleTouchingReader) Read(p []byte) (int, error) {
	n, err := ir.r.Read(p)
	if n > 0 {
		ir.touch()
	}
	return n, err
}

// executeStreamingAttempt 执行单次流式请求：发请求 → 增量扫描 JSON → 提取 chunk → 过滤 finishReason。
//
// emit 回调把清洗后的 Gemini chunk 推给上层；
// emit 返回 false（客户端断开）时扫描正常停止、返回 nil（StreamChat 据 chunkCount>0 收尾，不重试）。
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
		return NewInternalError("marshal payload: " + err.Error())
	}
	defer func() { _ = buf.Close() }()
	reader, err := buf.Reader()
	if err != nil {
		return NewInternalError("spool reader: " + err.Error())
	}
	header := transport.XHRHeaders(
		"application/json", "*/*",
		"https://console.cloud.google.com", "https://console.cloud.google.com/", "cross-site",
	)

	streamReqCtx, cancelStreamReq := context.WithCancel(ctx)
	defer cancelStreamReq()

	preTimeout := time.Duration(max(cfg.StreamIdleTimeoutSeconds()*2, 40)) * time.Second
	postTimeout := time.Duration(cfg.StreamIdleTimeoutSeconds()) * time.Second

	var (
		srRef              atomic.Pointer[transport.StreamResponse]
		lastActiveUnixNano atomic.Int64
		hasReceivedFirst   atomic.Bool
		idleTriggered      atomic.Bool
	)
	lastActiveUnixNano.Store(time.Now().UnixNano())

	touchActivity := func() {
		lastActiveUnixNano.Store(time.Now().UnixNano())
		hasReceivedFirst.CompareAndSwap(false, true)
	}

	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				last := time.Unix(0, lastActiveUnixNano.Load())
				elapsed := time.Since(last)
				timeout := preTimeout
				if hasReceivedFirst.Load() {
					timeout = postTimeout
				}
				if elapsed > timeout {
					if idleTriggered.CompareAndSwap(false, true) {
						log.Printf("[Vertex] [Stream] 触发流包间空闲超时 (已静默 %v > 阈值 %v), 切断连接, 请求ID=%s", elapsed.Round(time.Millisecond), timeout, reqID)
						cancelStreamReq()
						if sr := srRef.Load(); sr != nil && sr.Body != nil {
							_ = sr.Body.Close()
						}
					}
					return
				}
			case <-ctx.Done():
				cancelStreamReq()
				if sr := srRef.Load(); sr != nil && sr.Body != nil {
					_ = sr.Body.Close()
				}
				return
			}
		}
	}()

	sr, err := sess.DoStream(streamReqCtx, "POST", c.getBatchGraphqlURL(), header, reader)
	if err != nil {
		if idleTriggered.Load() {
			return NewUnavailableError("stream idle timeout before first byte")
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return NewContextError(ctxErr)
		}
		return NewNetworkError(fmt.Errorf("upstream request: %w", err))
	}

	srRef.Store(sr)
	if idleTriggered.Load() {
		sr.Close()
		return NewUnavailableError("stream idle timeout before first byte")
	}
	defer sr.Close() // 排干 → close，防串流。

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

		return classifyUpstreamHTTPError(sr.StatusCode, errText)
	}

	// 增量扫描上游流，逐个完整 JSON 对象提取 chunk。
	completionState := newStreamCompletionState()
	scanErr := scanStream(&idleTouchingReader{r: sr.Body, touch: touchActivity}, func(obj map[string]any) (stop bool, err error) {
		return processStreamingObject(obj, emit, completionState)
	})

	if idleTriggered.Load() {
		if completionState.allFinished() {
			return nil
		}
		return NewUnavailableError("stream chunk idle timeout")
	}

	if scanErr != nil && cfg.DebugMode() && !errors.Is(scanErr, context.Canceled) {
		debugReq, _ := json.Marshal(newBody)
		log.Printf("[DEBUG] [StreamChat] 扫描流数据报错! error: %v", scanErr)
		log.Printf("[DEBUG] [StreamChat] 完整请求体: %s", string(debugReq))
	}

	if errors.Is(scanErr, context.Canceled) || errors.Is(scanErr, context.DeadlineExceeded) {
		return scanErr
	}
	if scanErr != nil {
		return NewNetworkError(fmt.Errorf("upstream stream: %w", scanErr))
	}
	return nil
}

func classifyUpstreamHTTPError(statusCode int, body string) *VertexError {
	if statusCode == 401 || statusCode == 403 ||
		strings.Contains(body, "Failed to verify action") ||
		strings.Contains(body, "The caller does not have permission") {
		return NewAuthenticationError("Authentication/Recaptcha failed: " + body)
	}
	if json.Valid([]byte(body)) {
		if parsed := parseErrorResponse(body); parsed != nil {
			parsed.UpstreamResponse = body
			return parsed
		}
	}
	return raiseForStatus(statusCode, "", "Upstream Error: "+body, nil, body)
}

// scanStream 跨 chunk 增量扫描花括号配对，逐个完整 JSON 对象回调 onObject（O(n)）。
//
// M27 增量扫描：
// 跨网络 chunk 维护 scanPos/braceCount/inString/escape 状态，下个 chunk 从上次扫到的位置
// 续扫，而非每来一个 chunk 都从 startIdx 重扫整个 buffer（旧逻辑 O(n²）。逐字节逻辑等价。
//
// onObject 返回 (stop, err)：stop=true（命中真实 finishReason / 客户端断开）即正常结束扫描；
// err 非 nil 即中断并上抛（上游错误）。
var scanBufPool = sync.Pool{ //nolint:gochecknoglobals
	New: func() any {
		buf := make([]byte, 16*1024)
		return &buf
	},
}

func scanStream(body io.Reader, onObject func(map[string]any) (bool, error)) error {
	reader := bufio.NewReader(body)
	readBufPtr := scanBufPool.Get().(*[]byte)
	defer scanBufPool.Put(readBufPtr)
	readBuf := *readBufPtr

	var buffer []byte
	scanPos := 0  // 已扫到的位置（buffer 内），下个网络 chunk 从这里续扫。
	startIdx := 0 // 当前对象的起始 '{' 位置。
	braceCount := 0
	inString := false
	escape := false

	const maxBufferSize = 4 * 1024 * 1024

	for {
		n, readErr := reader.Read(readBuf)
		if n > 0 {
			buffer = append(buffer, readBuf[:n]...)

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
					obj := parseJSONObject(jsonStr)

					// In-place compaction to avoid memory allocation and copying overhead on every chunk
					copy(buffer, buffer[endIdx+1:])
					buffer = buffer[:len(buffer)-(endIdx+1)]
					scanPos = 0

					if obj != nil {
						stop, err := onObject(obj)
						if err != nil {
							return err
						}
						if stop {
							return nil
						}
					}
				} else {
					// 未扫到完整对象：记下已扫位置，下个 chunk 续扫，不重扫前缀。
					scanPos = len(buffer)
					break
				}
			}
		}

		if readErr != nil {
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
// 返回 (stop, err)：emit 出真实 finishReason 或客户端断开即 stop=true（结束扫描）；上游错误即 err 非 nil。
func processStreamingObject(obj map[string]any, emit func(map[string]any) bool, states ...*streamCompletionState) (bool, error) {
	var state *streamCompletionState
	if len(states) > 0 {
		state = states[0]
	}
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
				return false, NewAuthenticationError(errMsg)
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
					legacyDone := false
					// inner list 的各项是同一批平级候选；必须全部 emit 后再判断完成。
					for _, itemRaw := range inner {
						if item, ok := itemRaw.(map[string]any); ok {
							for k, v := range outerMeta {
								if _, exists := item[k]; !exists {
									item[k] = v
								}
							}
							if chunk := extractChunk(item); chunk != nil {
								stopByClient, done := emitAndCheckFinish(chunk, emit, state)
								if stopByClient {
									return true, nil
								}
								legacyDone = legacyDone || done
							}
						}
					}
					if state != nil {
						if state.sawUsage && state.allFinished() {
							return true, nil
						}
					} else if legacyDone {
						return true, nil
					}
					continue
				default:
					continue
				}
			}
		}

		if chunk := extractChunk(data); chunk != nil {
			if _, done := emitAndCheckFinish(chunk, emit, state); done {
				return true, nil
			}
		}
	}
	return false, nil
}

// emitAndCheckFinish emit 一个 chunk 并判定是否应结束流。
//
// 无状态调用保留旧语义；带状态调用会等待所有已见 candidate 完成且收到 usageMetadata，
// 避免首候选先结束时截断其他候选或丢失延迟 usage 包。
// 返回 (stopByClient, done)：done=true 表示应停止扫描；stopByClient 区分是客户端断开还是正常 finish。
func emitAndCheckFinish(chunk map[string]any, emit func(map[string]any) bool, states ...*streamCompletionState) (stopByClient bool, done bool) {
	if !emit(chunk) {
		// 客户端断开 / 上层要求停止。
		log.Printf("[Stream] 客户端主动断开，导致流结束")
		return true, true
	}
	if len(states) > 0 && states[0] != nil {
		state := states[0]
		state.observe(chunk)
		return false, state.sawUsage && state.allFinished()
	}
	fr := chunkFinishReason(chunk)
	if fr != "" && fr != finishReasonUnspecified {
		// 真实 finishReason：主动结束（避免上游不关连接挂到 180s）。
		return false, true
	}
	return false, false
}

type streamCompletionState struct {
	seen     map[int]struct{}
	finished map[int]struct{}
	sawUsage bool
}

func newStreamCompletionState() *streamCompletionState {
	return &streamCompletionState{
		seen:     map[int]struct{}{},
		finished: map[int]struct{}{},
		sawUsage: false,
	}
}

func (s *streamCompletionState) observe(chunk map[string]any) {
	if s == nil {
		return
	}
	if usage, ok := chunk["usageMetadata"].(map[string]any); ok && len(usage) > 0 {
		s.sawUsage = true
	}
	candidates, _ := chunk["candidates"].([]any)
	for position, rawCandidate := range candidates {
		candidate, ok := rawCandidate.(map[string]any)
		if !ok {
			continue
		}
		index := position
		if candidate["index"] != nil {
			index = toInt(candidate["index"], position)
		}
		s.seen[index] = struct{}{}
		finish := toStr(candidate["finishReason"])
		if finish != "" && finish != finishReasonUnspecified {
			s.finished[index] = struct{}{}
		}
	}
}

func (s *streamCompletionState) allFinished() bool {
	return s != nil && len(s.seen) > 0 && len(s.finished) == len(s.seen)
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
