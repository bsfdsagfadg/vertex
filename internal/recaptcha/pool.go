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
	fetchRoute   func(context.Context, transport.Route) (string, error)
	defaultProxy func(context.Context) string
	routes       func(context.Context) []string
}

type NodeRuntime interface {
	Select(k, topK int, stickyBonus bool, reserve bool) []nodes.Node
	NodeName(uri string) string
}

func NewTokenPool(net *transport.NetworkClient, cfg config.ConfigProvider, runtimes ...NodeRuntime) *TokenPool {
	var runtime NodeRuntime
	if len(runtimes) > 0 {
		runtime = runtimes[0]
	}
	versionCache := NewVersionCache()
	return &TokenPool{
		fetch: func(ctx context.Context, proxyURI string) (string, error) {
			return fetchRecaptchaTokenWithNamer(ctx, net, transport.Route{RequestNodeURI: proxyURI}, config.FromContext(ctx, cfg).DebugMode(), nodeNamer(runtime), versionCache)
		},
		fetchRoute: func(ctx context.Context, route transport.Route) (string, error) {
			return fetchRecaptchaTokenWithNamer(ctx, net, route, config.FromContext(ctx, cfg).DebugMode(), nodeNamer(runtime), versionCache)
		},
		routes: func(ctx context.Context) []string {
			return sharedTokenRoutes(config.FromContext(ctx, cfg), runtime)
		},
	}
}

func nodeNamer(runtime NodeRuntime) func(string) string {
	if runtime != nil {
		return runtime.NodeName
	}
	return nodes.GetNodeName
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

// GetTokenWithRouteContext obtains a token over the exact route used by the
// model request. It never falls back to shared route racing.
func (p *TokenPool) GetTokenWithRouteContext(ctx context.Context, route transport.Route) (string, error) {
	if p.fetchRoute != nil {
		return p.fetchRoute(ctx, route)
	}
	if route.GlobalProxyURI != "" {
		return "", fmt.Errorf("route-aware recaptcha fetch is unavailable")
	}
	return p.fetch(ctx, route.RequestNodeURI)
}

// GetTokenSharedContext 每次调用都会独立获取一份 token，不缓存，也不使用 singleflight。
// 最多五条路线立即并发执行完整 reCAPTCHA 流程，首个成功结果胜出并取消其余请求。
func (p *TokenPool) GetTokenSharedContext(ctx context.Context) (string, error) {
	routes := []string(nil)
	if p.routes != nil {
		routes = p.routes(ctx)
	}
	if len(routes) == 0 {
		proxyURI := ""
		if p.defaultProxy != nil {
			proxyURI = p.defaultProxy(ctx)
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

func sharedTokenRoutes(cfg config.ConfigProvider, runtimes ...NodeRuntime) []string {
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
		// 为最后的直连路线预留一个名额；所有已选路线仍会同时启动。
		candidateSlots := sharedTokenConcurrency - len(routes) - 1
		var candidates []nodes.Node
		if len(runtimes) > 0 && runtimes[0] != nil {
			candidates = runtimes[0].Select(max(0, candidateSlots), cfg.ParallelNodeTopK(), false, false)
		} else {
			candidates = nodes.SelectForParallel(max(0, candidateSlots), cfg.ParallelNodeTopK(), cfg.DebugMode(), false)
		}
		for _, candidate := range candidates {
			add(candidate.RawURI)
		}
	}
	// 直连与全局代理、健康节点一起参加竞速；达到五路上限时不再追加。
	add("")
	return routes
}
