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

// ErrStreamIncomplete 是流在未收到任何真实 finishReason 前即结束的哨兵错误
// （上游干净 EOF 但未按匿名端点契约给出终帧，或元数据后直接收尾）。
// 交付层遇此错误必须向客户端透传"截断"语义，严禁补发 FinishReason=STOP 冒充完整生成。
var ErrStreamIncomplete = errors.New("stream ended without finish reason")

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
func (c *VertexAIClient) StreamChat(ctx context.Context, model string, req *transform.GeminiRequest, yield func(StreamChunk) bool, strategy transform.ModelStrategy) {
	if strategy == nil {
		strategy = transform.NewModelFamilyRouter().For(model)
	}
	// L1 透传层：单候选一次尝试，原样上报真实错误（不重试、不分类、不转换）。
	// 每轮独立建连 + 独立取 token；首帧已交付后断流时对错误标 Truncated（Committed 语义）。
	op := func(ctx context.Context, proxyURI string) <-chan StreamChunk {
		ch := make(chan StreamChunk, 64)
		go func() {
			defer close(ch)
			sess, err := c.net.CreateSession(sessionTimeoutFromContext(ctx, 180), proxyURI, RequestIDFromContext(ctx))
			if err != nil {
				ch <- StreamChunk{Err: NewInternalError("create session: "+err.Error(), nil)}
				return
			}
			defer sess.Close()
			tok, err := c.pool.GetTokenShared(ctx)
			if err != nil || tok == "" {
				ch <- StreamChunk{Err: NewRecaptchaUnavailableError("Could not fetch recaptcha token", err)}
				return
			}
			var contentYielded bool
			emit := func(chunk *transform.GeminiChunk) bool {
				if strategy.IsValidChunk(chunk) {
					contentYielded = true
				}
				select {
				case ch <- StreamChunk{Data: chunk}:
					return true
				case <-ctx.Done():
					return false
				}
			}
			attemptErr := withRTFirstTryCompensation(ctx, func() error {
				return c.executeStreamingAttempt(ctx, sess, model, req, tok, emit, strategy)
			})
			if attemptErr == nil {
				if !contentYielded {
					ch <- StreamChunk{Err: NewEmptyResponseError("Upstream returned empty response (no content)", nil)}
				}
				return
			}
			ve := NormalizeError(attemptErr)
			if contentYielded {
				ve.WithTruncated()
			}
			ch <- StreamChunk{Err: ve}
		}()
		return ch
	}
	StreamParallel(ctx, c.cfg, model, op, yield, strategy)
}

// executeStreamingAttempt 执行单次流式请求：发请求 → 增量扫描 JSON → 提取 chunk → 过滤 finishReason。
//
// emit 回调把清洗后的 Gemini chunk 推给上层；
// emit 始终返回 true；客户端断开由 ctx 取消触发 read 报错，scanStream 干净结束。
// ctx 绑定 to 上游流连接：ctx 取消时 Body.Read 报错，scanStream 干净结束（返回 nil，不 panic）。
func (c *VertexAIClient) executeStreamingAttempt(ctx context.Context, sess *transport.Session, model string, req *transform.GeminiRequest, recaptchaToken string, emit func(*transform.GeminiChunk) bool, strategy transform.ModelStrategy) error {
	reqID := RequestIDFromContext(ctx)
	log.Printf("[Vertex] [executeStreamingAttempt] 准备发送流式请求: 模型=%s, 请求ID=%s, 代理=%s", model, reqID, nodes.GetNodeName(sess.ProxyURI))
	cfg := c.cfg
	if strategy == nil {
		strategy = transform.NewModelFamilyRouter().For(model)
	}
	newBody := buildTypedRequestPayload(model, req, recaptchaToken, cfg)
	// 上游请求 payload 序列化到 spool 缓冲（大媒体自动落盘）。流式：请求体在 DoStream 发送期被读取，
	// 缓冲存活到本函数返回（整个流消费完）后由 defer Close 删除临时文件。
	buf, err := spool.EncodeJSON(newBody)
	if err != nil {
		return NewInternalError("marshal payload: "+err.Error(), nil)
	}
	defer func() { _ = buf.Close() }()
	reader, err := buf.Reader()
	if err != nil {
		return NewInternalError("spool reader: "+err.Error(), nil)
	}
	header := transport.XHRHeaders(
		"application/json", "*/*",
		"https://console.cloud.google.com", "https://console.cloud.google.com/", "cross-site",
	)

	// ═══ 流式包间空闲超时监控（前置版：覆盖 DoStream 等待阶段） ═══
	streamReqCtx, cancelStreamReq := context.WithCancel(ctx)
	defer cancelStreamReq()

	preTimeout, postTimeout := strategy.CalculateIdleTimeouts(cfg.StreamIdleTimeoutSeconds())

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
						if sr := srRef.Load(); sr != nil {
							sr.Abort()
						}
					}
					return
				}
			case <-ctx.Done():
				cancelStreamReq()
				if sr := srRef.Load(); sr != nil {
					sr.Abort()
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
		sr.Abort()
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
			debugBody, _ := json.Marshal(newBody.Variables)
			log.Printf("[Vertex] [Stream] 收到 400 Bad Request, Variables Payload: %s", string(debugBody))
			log.Printf("[Vertex] [Stream] 上游 400 原始回复: %s", errText)
		}

		if sr.StatusCode == 401 || sr.StatusCode == 403 ||
			strings.Contains(errText, "Failed to verify action") ||
			strings.Contains(errText, "The caller does not have permission") {
			return NewAuthenticationError("Authentication/Recaptcha failed: "+errText, nil)
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

	scanErr := scanStream(streamReqCtx, sr.Body, func(raw []byte) (stop bool, err error) {
		if cfg.DebugMode() {
			log.Printf("[DEBUG] [Stream] 上游帧摘要: %s, 请求ID=%s, 节点=%s", summarizeUpstreamObject(parseJSONObject(raw)), reqID, nodes.GetNodeName(sess.ProxyURI))
		}
		return processStreamingObject(raw, emitSink, &seenFinish)
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

// isAuthVerifyFail 判定 auth 错误是否为 rT 验证时序失败（"Failed to verify action" 或
// "The caller does not have permission"）。这类失败在同一 token 静置 500ms 后重试一次
// 即可成功（rT 时序补偿机制），其余 auth 错误（token 彻底失效）交由预算循环换 token 承接。
func isAuthVerifyFail(ve *VertexError) bool {
	if ve == nil || ve.Kind != "auth" {
		return false
	}
	return strings.Contains(ve.Message, "Failed to verify action") ||
		strings.Contains(ve.Message, "The caller does not have permission")
}

// withRTFirstTryCompensation 是全局唯一的 rT 第一发补偿机制：
// 每个真实请求的第一发必被上游 "Failed to verify action" 拒绝，必须以同一 token
// 重发第二发才成功（与等待时长无关，保留旧版 500ms 间隔）。换新 token 同样首轮必败，
// 故该机制无法由预算循环承接（预算每轮均为新 token），只能附着在"发起真实请求"的
// 公共动作上，流式/非流式两个 L1 入口共用同一实现。
func withRTFirstTryCompensation(ctx context.Context, attempt func() error) error {
	err := attempt()
	if err == nil || !isAuthVerifyFail(NormalizeError(err)) {
		return err
	}
	if sleepCtx(ctx, 500*time.Millisecond) != nil {
		return err
	}
	return attempt()
}

// isEmptyResponseError 判断 error 是否为空响应错误（NewEmptyResponseError 构造）。
func isEmptyResponseError(err error) bool {
	var ve *VertexError
	if errors.As(err, &ve) {
		return ve.Code == 502 && ve.Kind == "network" && strings.Contains(ve.Message, "empty response")
	}
	return false
}
