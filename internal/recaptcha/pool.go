package recaptcha

import (
	"context"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

type TokenPool struct {
	net   *transport.NetworkClient
	cfg   config.ConfigProvider
	fetch func(proxyURI string) (string, error) // optional override for testing
}

func NewTokenPool(net *transport.NetworkClient, cfg config.ConfigProvider) *TokenPool {
	return &TokenPool{net: net, cfg: cfg}
}

// NewTokenPoolCustom creates a token pool with a custom fetch function.
// Used for testing; will be replaced by DI in phase 3/4.
func NewTokenPoolCustom(fetch func(proxyURI string) (string, error)) *TokenPool {
	return &TokenPool{fetch: fetch}
}

func (p *TokenPool) GetToken(ctx context.Context) (string, error) {
	if p.fetch != nil {
		return p.fetch("")
	}
	return FetchRecaptchaToken(ctx, p.net, "", p.cfg.DebugMode())
}

func (p *TokenPool) GetTokenWithProxy(ctx context.Context, proxyURI string) (string, error) {
	if p.fetch != nil {
		return p.fetch(proxyURI)
	}
	if proxyURI == "" {
		return p.GetToken(ctx)
	}
	return FetchRecaptchaToken(ctx, p.net, proxyURI, p.cfg.DebugMode())
}
