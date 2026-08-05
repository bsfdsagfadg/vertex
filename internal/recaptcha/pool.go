package recaptcha

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

type TokenPool struct {
	net       *transport.NetworkClient
	cfg       config.ConfigProvider
	fetch     func(proxyURI string) (string, error) // optional override for testing
	mu        sync.RWMutex
	cachedTok string
	cachedAt  time.Time
	sfGroup   singleflight.Group
}

func NewTokenPool(net *transport.NetworkClient, cfg config.ConfigProvider) *TokenPool {
	return &TokenPool{net: net, cfg: cfg}
}

// NewTokenPoolCustom creates a token pool with a custom fetch function.
// Used for testing.
func NewTokenPoolCustom(fetch func(proxyURI string) (string, error)) *TokenPool {
	return &TokenPool{fetch: fetch}
}

func (p *TokenPool) GetToken(ctx context.Context) (string, error) {
	return p.GetTokenShared(ctx)
}

// Deprecated: GetTokenWithProxy delegates to GetTokenShared and ignores proxyURI.
// Use GetTokenShared instead.
func (p *TokenPool) GetTokenWithProxy(ctx context.Context, proxyURI string) (string, error) {
	return p.GetTokenShared(ctx)
}

func (p *TokenPool) Invalidate() {
	p.mu.Lock()
	p.cachedTok = ""
	p.cachedAt = time.Time{}
	p.mu.Unlock()
}

func (p *TokenPool) GetTokenShared(ctx context.Context) (string, error) {
	p.mu.RLock()
	if p.cachedTok != "" && time.Since(p.cachedAt) < 30*time.Second {
		tok := p.cachedTok
		p.mu.RUnlock()
		return tok, nil
	}
	p.mu.RUnlock()

	v, err, _ := p.sfGroup.Do("get_token", func() (any, error) {
		p.mu.RLock()
		if p.cachedTok != "" && time.Since(p.cachedAt) < 30*time.Second {
			tok := p.cachedTok
			p.mu.RUnlock()
			return tok, nil
		}
		p.mu.RUnlock()

		if p.fetch != nil {
			tok, err := p.fetch("")
			if err != nil {
				return "", err
			}
			p.mu.Lock()
			p.cachedTok = tok
			p.cachedAt = time.Now()
			p.mu.Unlock()
			return tok, nil
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
				p.mu.Lock()
				p.cachedTok = tok
				p.cachedAt = time.Now()
				p.mu.Unlock()
				return tok, nil
			}
		}

		// Fallback or disabled entry: poll healthy candidate nodes
		cands := nodes.SelectForParallel(5, debugMode)
		var lastErr error
		for _, cand := range cands {
			tok, err := FetchRecaptchaToken(fetchCtx, p.net, cand.RawURI, debugMode)
			if err == nil && tok != "" {
				p.mu.Lock()
				p.cachedTok = tok
				p.cachedAt = time.Now()
				p.mu.Unlock()
				return tok, nil
			}
			lastErr = err
		}

		// If tryEntry was false or cands was empty, attempt direct fetch as last resort if not tried yet
		if !tryEntry {
			tok, err := FetchRecaptchaToken(fetchCtx, p.net, "", debugMode)
			if err == nil && tok != "" {
				p.mu.Lock()
				p.cachedTok = tok
				p.cachedAt = time.Now()
				p.mu.Unlock()
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
	})

	if err != nil {
		return "", err
	}
	tok, _ := v.(string)
	return tok, nil
}
