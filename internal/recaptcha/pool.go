package recaptcha

import (
	"context"

	"github.com/bsfdsagfadg/vertex/internal/transport"
)

type TokenPool struct {
	fetch        func(context.Context, string) (string, error)
	defaultProxy func() string
}

func NewTokenPool(net *transport.NetworkClient, defaultProxy func() string, debugMode bool) *TokenPool {
	return &TokenPool{
		fetch: func(ctx context.Context, proxyURI string) (string, error) {
			return FetchRecaptchaToken(ctx, net, proxyURI, debugMode)
		},
		defaultProxy: defaultProxy,
	}
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
	proxyURI := ""
	if p.defaultProxy != nil {
		proxyURI = p.defaultProxy()
	}
	return p.fetch(ctx, proxyURI)
}

func (p *TokenPool) GetTokenWithProxyContext(ctx context.Context, proxyURI string) (string, error) {
	// 该方法的 proxyURI 是调用方已经决定好的实际出口；空值明确表示直连，
	// 不能回退到启动时或当前全局代理，否则 token 与业务请求可能使用不同出口 IP。
	return p.fetch(ctx, proxyURI)
}
