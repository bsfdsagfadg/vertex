package recaptcha

import (
	"context"
	"errors"
	"fmt"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

const sharedTokenConcurrency = 5

type TokenPool struct {
	fetch        func(context.Context, string) (string, error)
	defaultProxy func() string
	routes       func() []string
	primary      func() []string
	fallback     func() []string
}

func NewTokenPool(net *transport.NetworkClient, cfg config.ConfigProvider) *TokenPool {
	p := &TokenPool{
		fetch: func(ctx context.Context, proxyURI string) (string, error) {
			return FetchRecaptchaToken(ctx, net, proxyURI, cfg.DebugMode())
		},
		defaultProxy: cfg.ProxyURL,
		routes:       func() []string { return sharedTokenRoutes(cfg) },
	}
	if cfg.RecaptchaTryEntryOrDirect() {
		p.primary = func() []string {
			out := []string{}
			for _, e := range config.SelectEntryProxySequence(1, cfg) {
				out = append(out, e)
			}
			out = append(out, "")
			return out
		}
		p.fallback = func() []string {
			out := []string{}
			for _, n := range nodes.SelectForParallel(cfg.ParallelPoolSize(), cfg.ParallelNodeTopK(), cfg.DebugMode(), false) {
				out = append(out, n.RawURI)
			}
			return out
		}
	}
	return p
}

// NewTokenPoolCustom creates a token pool with a custom fetch function.
// Used for testing; will be replaced by DI in phase 3/4.
func NewTokenPoolCustom(fetch func(proxyURI string) (string, error)) *TokenPool {
	return &TokenPool{fetch: func(_ context.Context, proxyURI string) (string, error) { return fetch(proxyURI) }}
}

// NewTokenPoolCustomContext 创建可观察请求取消的测试 TokenPool。
func NewTokenPoolCustomContext(fetch func(context.Context, string) (string, error)) *TokenPool {
	return &TokenPool{fetch: fetch}
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
	return p.GetTokenSharedContext(ctx)
}

func (p *TokenPool) GetTokenWithProxyContext(ctx context.Context, proxyURI string) (string, error) {
	// 该方法的 proxyURI 是调用方已经决定好的实际出口；空值明确表示直连，
	// 不能回退到启动时或当前全局代理，否则 token 与业务请求可能使用不同出口 IP。
	return p.fetch(ctx, proxyURI)
}

// GetTokenSharedContext 每次调用都会独立获取一份 token，不缓存，也不使用 singleflight。
// 最多五条路线立即并发执行完整 reCAPTCHA 流程，首个成功结果胜出并取消其余请求。
func (p *TokenPool) GetTokenSharedContext(ctx context.Context) (string, error) {
	if p.primary != nil {
		if token, err := p.fetchRoutes(ctx, p.primary()); err == nil {
			return token, nil
		}
		if token, err := p.fetchRoutes(ctx, p.fallback()); err == nil {
			return token, nil
		}
		return "", fmt.Errorf("all recaptcha token routes failed")
	}
	routes := []string(nil)
	if p.routes != nil {
		routes = p.routes()
	}
	if len(routes) == 0 {
		proxyURI := ""
		if p.defaultProxy != nil {
			proxyURI = p.defaultProxy()
		}
		routes = []string{proxyURI}
	}
	if len(routes) > sharedTokenConcurrency {
		routes = routes[:sharedTokenConcurrency]
	}

	raceCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		token string
		err   error
	}
	results := make(chan result, len(routes))
	for _, route := range routes {
		route := route
		go func() {
			token, err := p.fetch(raceCtx, route)
			if err == nil && token == "" {
				err = errors.New("empty recaptcha token")
			}
			results <- result{token: token, err: err}
		}()
	}

	var firstErr error
	for range routes {
		result := <-results
		if result.err == nil {
			cancel()
			return result.token, nil
		}
		if firstErr == nil && !errors.Is(result.err, context.Canceled) {
			firstErr = result.err
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if firstErr == nil {
		firstErr = fmt.Errorf("all recaptcha token routes failed")
	}
	return "", firstErr
}

func (p *TokenPool) fetchRoutes(ctx context.Context, routes []string) (string, error) {
	if len(routes) == 0 {
		return "", errors.New("no recaptcha routes")
	}
	var first error
	for _, route := range routes {
		token, err := p.fetch(ctx, route)
		if err == nil && token != "" {
			return token, nil
		}
		if err != nil && !errors.Is(err, context.Canceled) && first == nil {
			first = err
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
	}
	if first == nil {
		first = errors.New("all recaptcha token routes failed")
	}
	return "", first
}

func sharedTokenRoutes(cfg config.ConfigProvider) []string {
	routes := make([]string, 0, sharedTokenConcurrency)
	seen := make(map[string]struct{}, sharedTokenConcurrency)
	add := func(route string) {
		if len(routes) >= sharedTokenConcurrency {
			return
		}
		if _, exists := seen[route]; exists {
			return
		}
		seen[route] = struct{}{}
		routes = append(routes, route)
	}

	if cfg != nil {
		// C 分支传空 proxy 时由 NetworkClient 选择一个入口代理；当前传输层要求
		// 显式指定路线，因此在这里先取一个入口代理参与同一轮竞速。
		for _, entry := range config.SelectEntryProxySequence(1, cfg) {
			add(entry)
		}
		if proxyURI := cfg.ProxyURL(); proxyURI != "" {
			add(proxyURI)
		}
		// 为最后的直连路线预留一个名额；所有已选路线仍会同时启动。
		candidateSlots := sharedTokenConcurrency - len(routes) - 1
		for _, candidate := range nodes.SelectForParallel(max(0, candidateSlots), cfg.ParallelNodeTopK(), cfg.DebugMode(), false) {
			add(candidate.RawURI)
		}
	}
	// 直连与全局代理、健康节点一起参加竞速；达到五路上限时不再追加。
	add("")
	return routes
}
