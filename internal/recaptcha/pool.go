package recaptcha

import (
	"context"
	"fmt"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
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
// Used for testing.
func NewTokenPoolCustom(fetch func(proxyURI string) (string, error)) *TokenPool {
	return &TokenPool{fetch: fetch}
}

// Invalidate 为兼容保留的空操作：30 秒全局缓存已移除，
// 每次 GetTokenShared 都会实时抓取最新 token，无需主动失效。
func (p *TokenPool) Invalidate() {}

// GetTokenShared 每次调用都实时抓取一份独立的 reCAPTCHA token。
//
// 注意：不使用 singleflight 折叠并发——reCAPTCHA Enterprise token 是单次有效
// （single-use），并发请求必须各自持有独立 token，否则复用同一份会触发
// Google 侧验证失败（Failed to verify action / 429）。每个请求独立抓取，
// 由 15s 超时与前置池/出口节点降级路径兜底。
func (p *TokenPool) GetTokenShared(_ context.Context) (string, error) {
	if p.fetch != nil {
		return p.fetch("")
	}

	debugMode := false
	tryEntry := true
	if p.cfg != nil {
		debugMode = p.cfg.DebugMode()
		tryEntry = p.cfg.RecaptchaTryEntryOrDirect()
	}

	fetchCtx, fetchCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer fetchCancel()

	if tryEntry {
		tok, err := FetchRecaptchaToken(fetchCtx, p.net, "", debugMode)
		if err == nil && tok != "" {
			return tok, nil
		}
	}

	// Fallback or disabled entry: poll healthy candidate nodes
	cands := nodes.SelectForParallel(5, debugMode)
	var lastErr error
	for _, cand := range cands {
		tok, err := FetchRecaptchaToken(fetchCtx, p.net, cand.RawURI, debugMode)
		if err == nil && tok != "" {
			return tok, nil
		}
		lastErr = err
	}

	// If tryEntry was false or cands was empty, attempt direct fetch as last resort if not tried yet
	if !tryEntry {
		tok, err := FetchRecaptchaToken(fetchCtx, p.net, "", debugMode)
		if err == nil && tok != "" {
			return tok, nil
		}
		if err != nil {
			lastErr = err
		}
	}

	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("failed to fetch recaptcha token")
}
