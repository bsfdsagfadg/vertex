package vertex

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/engine/recaptcha"
	"github.com/bsfdsagfadg/vertex/internal/engine/transform"
	"github.com/bsfdsagfadg/vertex/internal/infra/config"
	"github.com/bsfdsagfadg/vertex/internal/infra/jsonx"
	"github.com/bsfdsagfadg/vertex/internal/infra/transport"
)

const (
	anonBaseURL      = "https://cloudconsole-pa.clients6.google.com"
	batchGraphqlPath = "/v3/entityServices/AiplatformEntityService/schemas/AIPLATFORM_GRAPHQL:batchGraphql"
	anonAPIKey       = "AIzaSyCI-zsRP85UVOi0DjtiCwWBwQ1djDy741g"
)

// RequestIDKey 是 context 中存储 reqID 的键类型。
type RequestIDKey struct{}

// RequestIDFromContext 取请求上下文里的 request-id（无则空串）。
func RequestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(RequestIDKey{}).(string); ok {
		return v
	}
	return ""
}

type VertexAIClient struct {
	net  *transport.NetworkClient
	pool *recaptcha.TokenPool
	cfg  config.ConfigProvider
	// nodes 是出口节点池消费端口（候选选取与健康态记账）；零值时由方法内防御归一。
	nodes NodePool

	// batchURL 非空时覆盖 batchGraphql 端点（测试注入用），空串走动态计算。
	batchURL string
}

// NewVertexAIClient 装配客户端：pool 为 nil 时按 cfg+nodePool 自建 reCAPTCHA 池，
// nodePool 为 nil 时归一为 nopNodePool（引擎走单节点降级路径）。
func NewVertexAIClient(cfg config.ConfigProvider, net *transport.NetworkClient,
	pool *recaptcha.TokenPool, nodePool NodePool,
) *VertexAIClient {
	if pool == nil {
		pool = recaptcha.NewTokenPool(net, cfg, nodePool)
	}
	if nodePool == nil {
		nodePool = nopNodePool{}
	}
	return &VertexAIClient{
		net:   net,
		pool:  pool,
		cfg:   cfg,
		nodes: nodePool,
	}
}

// nodePool 返回节点池消费端口；零值客户端（测试直构 struct 字面量）防御归一为 nopNodePool。
func (c *VertexAIClient) nodePool() NodePool {
	if c.nodes == nil {
		return nopNodePool{}
	}
	return c.nodes
}

// prepareCandidate 并行执行候选启动前置准备：出口建联与 rT 抓取互不依赖
// （rT 抓取内部自建临时会话，从不复用候选连接），关键路径由 T建联+T取rT
// 收敛为 max(T建联, T取rT)。裁决语义见 joinPrepared。
//
// 取消语义无条件最优先：join 后若 ctx 已取消（含取消发生在两任务执行期间），
// 无论建联/抓取结果如何一律丢弃并上浮取消错误——防止请求死亡时的资源类错误
// 被误判为 infra/FailFast 扭曲竞速终局；失败路径上已建成的会话就地关闭，
// 临时 sing-box 实例绝不泄漏。
func (c *VertexAIClient) prepareCandidate(ctx context.Context, proxyURI string) (*transport.Session, string, *VertexError) {
	sess, tok, sessErr, tokErr := runPrepareTasks(
		func() (*transport.Session, error) {
			return c.net.CreateSession(sessionTimeoutFromContext(ctx, 180), proxyURI, RequestIDFromContext(ctx))
		},
		func() (string, error) {
			return c.pool.GetTokenShared(ctx)
		},
	)

	if err := ctx.Err(); err != nil {
		if sess != nil {
			sess.Close()
		}
		return nil, "", NewContextError(err)
	}
	return joinPrepared(sess, sessErr, tok, tokErr)
}

// runPrepareTasks 并发调度两组互不依赖的前置任务并等待双双完成。
// 只负责并发编排与汇合，不做任何错误取舍（裁决见 joinPrepared）；
// 独立成函数以便以受控桩做确定性的并行性测试。
func runPrepareTasks(
	sessFn func() (*transport.Session, error),
	tokFn func() (string, error),
) (sess *transport.Session, tok string, sessErr error, tokErr error) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		sess, sessErr = sessFn()
	}()
	go func() {
		defer wg.Done()
		tok, tokErr = tokFn()
	}()
	wg.Wait()
	return
}

// joinPrepared 对并行完成的结果做错误裁决（纯函数，矩阵可穷举单测）：
//  1. rT 失败或为空 → RecaptchaUnavailableError（infra：流式下触发 FailFast 尽早终局；
//     双失败时同样由 rT 错误胜出，省去无谓候选消耗）；
//  2. 仅会话失败 → InternalError（internal：仅该候选出局，其余候选照常竞争）；
//  3. 全部成功 → 会话与 token 原样移交调用方（Close 责任随之转移）。
//
// 任一失败路径上已建成的会话在返回前负责关闭。
func joinPrepared(sess *transport.Session, sessErr error, tok string, tokErr error) (*transport.Session, string, *VertexError) {
	if tokErr != nil || tok == "" {
		if sess != nil {
			sess.Close()
		}
		return nil, "", NewRecaptchaUnavailableError("Could not fetch recaptcha token", tokErr)
	}
	if sessErr != nil {
		return nil, "", NewInternalError("create session: "+sessErr.Error(), nil)
	}
	return sess, tok, nil
}

// getBatchGraphqlURL 每次动态读取配置密钥（cfg 经 SIGHUP 缓存失效热重载），
// 未配置时回落匿名 key；batchURL 被测试注入覆盖时原样返回。
func (c *VertexAIClient) getBatchGraphqlURL() string {
	if c.batchURL != "" {
		return c.batchURL
	}
	key := c.cfg.VertexAPIKey()
	if key == "" {
		key = anonAPIKey
	}
	return anonBaseURL + batchGraphqlPath + "?key=" + key + "&prettyPrint=false"
}

type ParseResultTyped struct {
	Candidates     []*transform.Candidate
	PromptFeedback *transform.PromptFeedback
	UsageMetadata  *transform.UsageMetadata
	ModelVersion   string
	HasError       bool
	ErrorMessage   string
}

func (c *VertexAIClient) buildCompleteResponseTyped(r *ParseResultTyped) (*transform.GeminiResponse, error) {
	if r.HasError {
		return nil, NewInternalError("upstream parse error: "+r.ErrorMessage, nil)
	}
	resp := &transform.GeminiResponse{
		Candidates:     r.Candidates,
		PromptFeedback: r.PromptFeedback,
		UsageMetadata:  r.UsageMetadata,
		ModelVersion:   r.ModelVersion,
	}
	if len(resp.Candidates) == 0 && resp.PromptFeedback == nil {
		return nil, NewEmptyResponseError("Upstream returned empty response (no content)", nil)
	}
	return resp, nil
}

func collectChunksToParseResultTyped(chunks []*transform.GeminiChunk) *ParseResultTyped {
	s := &ParseResultTyped{}
	candsMap := map[int]*transform.Candidate{}

	for _, chunk := range chunks {
		if chunk == nil {
			continue
		}
		for _, cand := range chunk.Candidates {
			if cand == nil {
				continue
			}
			idx := cand.Index
			existing, ok := candsMap[idx]
			if !ok {
				cCopy := *cand
				if cCopy.Content == nil {
					cCopy.Content = &transform.Content{Role: "model"}
				}
				candsMap[idx] = &cCopy
			} else {
				if cand.FinishReason != "" {
					existing.FinishReason = cand.FinishReason
				}
				if cand.Content != nil && len(cand.Content.Parts) > 0 {
					if existing.Content == nil {
						existing.Content = &transform.Content{Role: "model"}
					}
					existing.Content.Parts = append(existing.Content.Parts, cand.Content.Parts...)
				}
			}
		}
		if chunk.PromptFeedback != nil && s.PromptFeedback == nil {
			s.PromptFeedback = chunk.PromptFeedback
		}
		if chunk.UsageMetadata != nil {
			s.UsageMetadata = chunk.UsageMetadata
		}
		if chunk.ModelVersion != "" {
			s.ModelVersion = chunk.ModelVersion
		}
	}

	var idxs []int
	for idx := range candsMap {
		idxs = append(idxs, idx)
	}
	sort.Ints(idxs)

	for _, idx := range idxs {
		c := candsMap[idx]
		if c.Content != nil && len(c.Content.Parts) > 0 {
			c.Content.Parts = mergeStreamPartsTyped(c.Content.Parts)
		}
		s.Candidates = append(s.Candidates, c)
	}
	return s
}

func mergeStreamPartsTyped(parts []transform.Part) []transform.Part {
	if len(parts) == 0 {
		return parts
	}
	merged := make([]transform.Part, 0, len(parts))
	var current *transform.Part

	for _, p := range parts {
		if p.Text == "" {
			merged = append(merged, p)
			current = nil
			continue
		}
		if current != nil && current.Thought == p.Thought && current.Text != "" {
			current.Text += p.Text
			if p.ThoughtSignature != "" && current.ThoughtSignature == "" {
				current.ThoughtSignature = p.ThoughtSignature
			}
		} else {
			pCopy := p
			merged = append(merged, pCopy)
			current = &merged[len(merged)-1]
		}
	}
	return merged
}

func shallowCopy(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func asVertexError(err error) *VertexError {
	var ve *VertexError
	if errors.As(err, &ve) {
		return ve
	}
	return nil
}

// NormalizeError 将任意 error 统一归一化为 *VertexError。
//
// 1. 若 err 已是 *VertexError，直接返回避免双重包装；
// 2. 若包含 context.Canceled / context.DeadlineExceeded，包装为保留 cause 的 ContextError；
// 3. 若为 net.Error，包装为 502 NetworkError；
// 4. 其他未知错误包装为 500 InternalError。
func NormalizeError(err error) *VertexError {
	if err == nil {
		return nil
	}
	var ve *VertexError
	if errors.As(err, &ve) {
		return ve
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return NewContextError(err)
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return NewNetworkError(err)
	}

	return NewInternalError(err.Error(), err)
}

// classifyNetworkError 将网络原生 error 统一包装为 *VertexError（内部复用 NormalizeError）。
func classifyNetworkError(err error) *VertexError {
	return NormalizeError(err)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err() //nolint:wrapcheck
	case <-t.C:
		return nil
	}
}

func mapToGeminiChunk(m map[string]any) *transform.GeminiChunk {
	if m == nil {
		return &transform.GeminiChunk{}
	}
	b, err := jsonx.Marshal(m)
	if err != nil {
		return &transform.GeminiChunk{}
	}
	var chunk transform.GeminiChunk
	if err := json.Unmarshal(b, &chunk); err != nil {
		return &transform.GeminiChunk{}
	}
	return &chunk
}
