package recaptcha

import (
	"context"
	"fmt"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

// recaptchaFetchTimeout 单次 RT 获取链（含降级轮询）的总超时上限。
const recaptchaFetchTimeout = 15 * time.Second

type TokenPool struct {
	fetch        func(context.Context, string) (string, error)
	defaultProxy func() string
	// tryEntry 决定 RT 获取策略：true（默认）时优先经全局入口代理/直连抓取；
	// false 或失败时顺次轮询健康候选节点（第二跳）。nil 视为开启。
	tryEntry func() bool
	// selectNodes 返回健康候选节点列表（第二跳轮询源），测试可注入固定列表。
	selectNodes func() []nodes.Node
	// directOnly 为测试注入标记：Custom 构造器直通 fetch，跳过降级链。
	directOnly bool
}

func NewTokenPool(net *transport.NetworkClient, defaultProxy func() string, debugMode bool, tryEntry ...func() bool) *TokenPool {
	pool := &TokenPool{
		fetch: func(ctx context.Context, proxyURI string) (string, error) {
			return FetchRecaptchaToken(ctx, net, proxyURI, debugMode)
		},
		defaultProxy: defaultProxy,
		selectNodes: func() []nodes.Node {
			return nodes.SelectForParallel(5, 5, false, false)
		},
	}
	if len(tryEntry) > 0 {
		pool.tryEntry = tryEntry[0]
	}
	return pool
}

// NewTokenPoolCustom creates a token pool with a custom fetch function.
// Used for testing; will be replaced by DI in phase 3/4.
func NewTokenPoolCustom(fetch func(proxyURI string) (string, error)) *TokenPool {
	return &TokenPool{
		fetch:      func(_ context.Context, proxyURI string) (string, error) { return fetch(proxyURI) },
		directOnly: true,
	}
}

// NewTokenPoolCustomContext 创建可观察请求取消的测试 TokenPool。
func NewTokenPoolCustomContext(fetch func(context.Context, string) (string, error)) *TokenPool {
	return &TokenPool{fetch: fetch, directOnly: true}
}

// Start 为生命周期钩子，当前实现为纯懒加载：token 仅在首次 GetToken 调用时按需获取。
// 不启动后台预取 goroutine，避免用户在未发送 chat 请求时产生无意义的 TLS 拨号开销。
// 历史版本（commit eac2847）曾使用后台 ticker 维持热缓存以消除首条请求同步获取延迟，
// 但对于不发送 chat 请求的用户完全浪费，且首条请求可能赶上缓存过期仍需同步获取，弊大于利。
// 若在极低延迟场景下需要预取，可在此处按需追加后台填充逻辑。
func (p *TokenPool) Start() {}

func (p *TokenPool) Stop() {}

func (p *TokenPool) Stats() (size, fill int) {
	return 0, 0
}

func (p *TokenPool) GetToken() (string, error) {
	return p.GetTokenContext(context.Background())
}

func (p *TokenPool) GetTokenWithProxy(proxyURI string) (string, error) {
	return p.GetTokenWithProxyContext(context.Background(), proxyURI)
}

func (p *TokenPool) GetTokenContext(ctx context.Context) (string, error) {
	return p.get(ctx, "")
}

// GetTokenWithProxyContext 按获取策略抓取 reCAPTCHA Token：
//
//	开启"优先前置/直连"时经全局入口代理/直连直接抓取（与业务请求出口解耦）；
//	关闭或失败时顺次轮询健康候选节点；全部失败且 proxyURI 非空时最后尝试该出口。
//
// 测试注入（directOnly）时保持直通语义：直接调用 fetch 并按 proxyURI 原样透传。
func (p *TokenPool) GetTokenWithProxyContext(ctx context.Context, proxyURI string) (string, error) {
	return p.get(ctx, proxyURI)
}

func (p *TokenPool) get(ctx context.Context, proxyURI string) (string, error) {
	if p.directOnly {
		return p.fetch(ctx, proxyURI)
	}
	return p.fetchChain(ctx, proxyURI)
}

// fetchChain 按策略顺序尝试获取 RT，任一环节成功即返回；全部失败返回首个有效错误。
//
//	1. tryEntry 开启：直接经全局入口代理（cfg.ProxyURL）/直连抓取；
//	2. 轮询健康候选节点（第二跳），失败顺次尝试下一个；
//	3. 入口未尝试过（开关关闭）时，最后兜底直连一次；
//	4. proxyURI 非空（调用方指定出口）且上面全部失败时，最后用它再试。
func (p *TokenPool) fetchChain(ctx context.Context, proxyURI string) (string, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, recaptchaFetchTimeout)
	defer cancel()

	var lastErr error
	entryTried := false

	if p.tryEntry == nil || p.tryEntry() {
		entryTried = true
		entryProxy := ""
		if p.defaultProxy != nil {
			entryProxy = p.defaultProxy()
		}
		if tok, err := p.fetch(fetchCtx, entryProxy); err == nil && tok != "" {
			return tok, nil
		} else if err != nil {
			lastErr = err
		}
	}

	// 健康候选节点轮询（SelectForParallel 已按健康/空闲/最近使用排序）。
	cands := p.selectNodes()
	for _, cand := range cands {
		if tok, err := p.fetch(fetchCtx, cand.RawURI); err == nil && tok != "" {
			return tok, nil
		} else if err != nil {
			lastErr = err
		}
	}

	// 开关关闭且入口从未尝试：最后兜底直连一次。
	if !entryTried {
		if tok, err := p.fetch(fetchCtx, ""); err == nil && tok != "" {
			return tok, nil
		} else if err != nil {
			lastErr = err
		}
	}

	// 调用方指定出口兜底（与已尝试的入口代理去重）。
	if proxyURI != "" && proxyURI != p.entryProxyValue() {
		if tok, err := p.fetch(fetchCtx, proxyURI); err == nil && tok != "" {
			return tok, nil
		} else if err != nil {
			lastErr = err
		}
	}

	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("failed to fetch recaptcha token")
}

func (p *TokenPool) entryProxyValue() string {
	if p.defaultProxy != nil {
		return p.defaultProxy()
	}
	return ""
}
