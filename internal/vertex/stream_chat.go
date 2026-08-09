package vertex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/spool"
	"github.com/bsfdsagfadg/vertex/internal/transform"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

// ErrStreamIdleTimeout 是流式包间空闲超时哨兵错误。
// 首包前触发会自动重试/故障转移；首包后触发向客户端推送超时 error chunk。
var ErrStreamIdleTimeout = errors.New("stream idle timeout")

// 流式包间空闲超时的防御性下限（对称防护）。
// 正常值 pre=idle*2、post=idle；下限沿用同一 *2 联动关系：
// post 下限 10s（容忍思考模型停顿+网络抖动，留余量防误杀），pre 下限 = post 下限 * 2 = 20s。
// 仅兜住 idle<10 的极端激进配置；idle>=20 的正常用户完全不受影响。
const (
	minPostStreamTimeout = 10 * time.Second         // 包间防御性下限：容忍思考停顿，防零字节秒杀（新增对称防护）
	minPreStreamTimeout  = 2 * minPostStreamTimeout // 首包前防御性下限：与 post 下限 *2 联动，单一原则
)

// maxPendingMetadataChunks 是首内容帧前缓存元数据帧的防御性上限。
// 正常 Gemini 流式响应首内容帧前仅 1~3 个元数据帧；超额时静默丢弃后续元数据帧，
// 仅保留前 maxPendingMetadataChunks 帧待首内容帧到来时一并 flush。
// 丢弃的帧为纯元数据（promptFeedback/usageMetadata 等），不影响客户端内容解析正确性。
const maxPendingMetadataChunks = 128

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

// StreamChunk 是真流式中 yield 的单个增量。要么是 Gemini 数据 chunk，要么是错误。
type StreamChunk struct {
	// Data 是清洗后的 Gemini 增量（candidates/usageMetadata/...），Err==nil 时有效。
	Data *transform.GeminiChunk
	// Err 非 nil 表示重试耗尽、对外报错。
	Err *VertexError
}

// StreamChat 真流式入口。
func (c *VertexAIClient) StreamChat(ctx context.Context, model string, req *transform.GeminiRequest, yield func(StreamChunk) bool) {
	op := func(ctx context.Context, proxyURI string) <-chan StreamChunk {
		ch := make(chan StreamChunk, 64)
		go func() {
			defer close(ch)
			c.executeStreamingWithRetries(ctx, model, req, proxyURI, func(chunk StreamChunk) bool {
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

func (c *VertexAIClient) executeStreamingWithRetries(ctx context.Context, model string, req *transform.GeminiRequest, proxyURI string, yield func(StreamChunk) bool) {
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
			tok, err := c.pool.GetTokenShared(ctx)
			if err != nil {
				log.Printf("[Vertex] [StreamChat] 获取 recaptcha token 失败: %v, 代理=%s", err, nodes.GetNodeName(proxyURI))
			}
			recaptchaToken = tok
			isFirstAuth = true
		}
		if recaptchaToken == "" {
			log.Printf("[Vertex] [StreamChat] 代理 %s 获取 recaptcha token 失败，停止重试", nodes.GetNodeName(proxyURI))
			lastError = NewAuthenticationError("Could not fetch recaptcha token for node", nil)
			break retryLoop
		}

		validChunkCount := 0
		var pendingChunks []*transform.GeminiChunk

		attemptErr := c.executeStreamingAttempt(ctx, sess, model, req, recaptchaToken, isFirstAuth, func(ch *transform.GeminiChunk) bool {
			if isValidContentChunkTyped(ch) {
				for _, p := range pendingChunks {
					if !yield(StreamChunk{Data: p}) {
						return false
					}
				}
				pendingChunks = nil
				contentYielded = true
				validChunkCount++
				return yield(StreamChunk{Data: ch})
			}
			if contentYielded {
				return yield(StreamChunk{Data: ch})
			}
			if len(pendingChunks) < maxPendingMetadataChunks {
				pendingChunks = append(pendingChunks, ch)
			}
			return true
		})

		if attemptErr == nil {
			if validChunkCount > 0 {
				for _, p := range pendingChunks {
					if !yield(StreamChunk{Data: p}) {
						break
					}
				}
				pendingChunks = nil
				return
			}
			pendingChunks = nil
			attemptErr = NewEmptyResponseError("Upstream returned empty response (no content)", nil)
		}

		if errors.Is(attemptErr, context.Canceled) || errors.Is(attemptErr, context.DeadlineExceeded) {
			log.Printf("[Vertex] [StreamChat] 客户端取消请求/上下文超时，停止重试: 请求ID=%s, 代理=%s, err=%v", reqID, nodes.GetNodeName(proxyURI), attemptErr)
			lastError = NewContextError(attemptErr)
			break retryLoop
		}

		ve := asVertexError(attemptErr)
		switch {
		case ve != nil && ve.Kind == "auth":
			isVerifyFail := strings.Contains(ve.Message, "Failed to verify action") ||
				strings.Contains(ve.Message, "The caller does not have permission")
			if isFirstAuth && isVerifyFail {
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
			sess.Close()
			newSess, e := c.net.CreateSession(sessionTimeoutFromContext(ctx, 180), proxyURI, reqID)
			if e != nil {
				yield(StreamChunk{Err: NewInternalError("recreate session: " + e.Error(), nil)})
				return
			}
			sess = newSess

			wait := ve.RetryAfter
			if wait <= 0 {
				wait = min(10, 1+attempt)
			}
			log.Printf("[Vertex] [StreamChat] (Attempt %d/%d) 节点 %s 触发 429 将重试 (延迟 %ds), 请求ID=%s, 代理=%s", attempt, maxRetries, model, wait, reqID, nodes.GetNodeName(proxyURI))
			attempt++
			if err := sleepCtx(ctx, time.Duration(wait)*time.Second); err != nil {
				break retryLoop
			}

		case ve != nil && isEmptyResponseError(ve):
			log.Printf("[Vertex] [StreamChat] (Attempt %d/%d) 节点 %s 触发空响应错误，准备重试, 请求ID=%s, 代理=%s", attempt, maxRetries, model, reqID, nodes.GetNodeName(proxyURI))
			isFirstAuth = true
			lastError = ve
			if contentYielded || attempt >= maxRetries {
				break retryLoop
			}
			sess.Close()
			newSess, e := c.net.CreateSession(sessionTimeoutFromContext(ctx, 180), proxyURI, reqID)
			if e != nil {
				yield(StreamChunk{Err: NewInternalError("recreate session: " + e.Error(), nil)})
				return
			}
			sess = newSess
			attempt++
			if err := sleepCtx(ctx, backoff(attempt)); err != nil {
				break retryLoop
			}

		case ve != nil:
			lastError = ve
			if ve.Kind == "internal" || !ve.IsRetryable() || contentYielded || attempt >= maxRetries {
				log.Printf("[Vertex] [StreamChat] (Attempt %d/%d) 节点 %s 触发异常错误失败: [%s] %s, 请求ID=%s, 代理=%s", attempt, maxRetries, model, ve.Kind, ve.Message, reqID, nodes.GetNodeName(proxyURI))
				break retryLoop
			}
			log.Printf("[Vertex] [StreamChat] (Attempt %d/%d) 节点 %s 触发异常错误将重试: [%s] %s, 请求ID=%s, 代理=%s", attempt, maxRetries, model, ve.Kind, ve.Message, reqID, nodes.GetNodeName(proxyURI))
			sess.Close()
			newSess, e := c.net.CreateSession(sessionTimeoutFromContext(ctx, 180), proxyURI, reqID)
			if e != nil {
				yield(StreamChunk{Err: NewInternalError("recreate session: " + e.Error(), nil)})
				return
			}
			sess = newSess
			attempt++
			if err := sleepCtx(ctx, backoff(attempt)); err != nil {
				break retryLoop
			}

		default:
			lastError = NewInternalError(attemptErr.Error(), nil)
			break retryLoop
		}
	}

	if lastError != nil {
		if contentYielded {
			log.Printf("[Vertex] [StreamChat] 内容已输出后发生错误: [%s] %s, 请求ID=%s, 代理=%s", lastError.Kind, lastError.Message, reqID, nodes.GetNodeName(proxyURI))
		}
		yield(StreamChunk{Err: lastError})
	}
}

// executeStreamingAttempt 执行单次流式请求：发请求 → 增量扫描 JSON → 提取 chunk → 过滤 finishReason。
//
// emit 回调把清洗后的 Gemini chunk 推给上层；
// emit 始终返回 true；客户端断开由 ctx 取消触发 read 报错，scanStream 干净结束。
// ctx 绑定 to 上游流连接：ctx 取消时 Body.Read 报错，scanStream 干净结束（返回 nil，不 panic）。
func (c *VertexAIClient) executeStreamingAttempt(ctx context.Context, sess *transport.Session, model string, req *transform.GeminiRequest, recaptchaToken string, _ bool, emit func(*transform.GeminiChunk) bool) error {
	reqID := RequestIDFromContext(ctx)
	log.Printf("[Vertex] [executeStreamingAttempt] 准备发送流式请求: 模型=%s, 请求ID=%s, 代理=%s", model, reqID, nodes.GetNodeName(sess.ProxyURI))
	cfg := c.cfg
	newBody := buildTypedRequestPayload(model, req, recaptchaToken, cfg)
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

	// ═══ 流式包间空闲超时监控（前置版：覆盖 DoStream 等待阶段） ═══
	streamReqCtx, cancelStreamReq := context.WithCancel(ctx)
	defer cancelStreamReq()

	preTimeout := max(time.Duration(cfg.StreamIdleTimeoutSeconds()*2)*time.Second, minPreStreamTimeout)
	postTimeout := max(time.Duration(cfg.StreamIdleTimeoutSeconds())*time.Second, minPostStreamTimeout)

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
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
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
			case <-done:
				return
			}
		}
	}()
	// ═══════════════════════════════════════════════════════════════

	sr, err := sess.DoStream(streamReqCtx, "POST", c.getBatchGraphqlURL(), header, reader)
	if err != nil {
		if idleTriggered.Load() {
			return NewNetworkError(ErrStreamIdleTimeout)
		}
		return classifyNetworkError(err)
	}

	srRef.Store(sr)
	if idleTriggered.Load() {
		sr.Close()
		return NewNetworkError(ErrStreamIdleTimeout)
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
	var seenFinish bool

	emitSink := emit
	if cfg.DebugMode() {
		emitSink = func(ch *transform.GeminiChunk) bool {
			log.Printf("[DEBUG] [Stream] 转发帧, 请求ID=%s, 节点=%s", reqID, nodes.GetNodeName(sess.ProxyURI))
			return emit(ch)
		}
	}

	scanErr := scanStream(ctx, sr.Body, func(obj map[string]any) (stop bool, err error) {
		if cfg.DebugMode() {
			log.Printf("[DEBUG] [Stream] 上游帧摘要: %s, 请求ID=%s, 节点=%s", summarizeUpstreamObject(obj), reqID, nodes.GetNodeName(sess.ProxyURI))
		}
		return processStreamingObject(obj, emitSink, &seenFinish)
	}, touchActivity)

	if scanErr != nil && cfg.DebugMode() && !errors.Is(scanErr, context.Canceled) {
		debugReq, _ := json.Marshal(newBody)
		log.Printf("[DEBUG] [StreamChat] 扫描流数据报错! error: %v", scanErr)
		log.Printf("[DEBUG] [StreamChat] 完整请求体: %s", string(debugReq))
	}

	if idleTriggered.Load() {
		return NewNetworkError(ErrStreamIdleTimeout)
	}

	if errors.Is(scanErr, context.Canceled) {
		return scanErr
	}

	if scanErr != nil {
		return classifyNetworkError(scanErr)
	}

	return nil
}

// isEmptyResponseError 判断 error 是否为空响应错误（NewEmptyResponseError 构造）。
func isEmptyResponseError(err error) bool {
	var ve *VertexError
	if errors.As(err, &ve) {
		return ve.Code == 502 && ve.Kind == "network" && strings.Contains(ve.Message, "empty response")
	}
	return false
}
