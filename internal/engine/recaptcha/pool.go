package recaptcha

import (
	"context"
	"fmt"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/infra/config"
	"github.com/bsfdsagfadg/vertex/internal/infra/transport"
	"github.com/bsfdsagfadg/vertex/internal/node/exitpool"
)

// NodeSource 是 reCAPTCHA 抓取对出口节点池的最小消费契约（生产实现 *exitpool.Manager）。
// 类型位引用 []exitpool.Node 为 RFC 记录的显式豁免：候选结构体跨域只读传递。
type NodeSource interface {
	SelectForParallel(k int, debugMode bool) []exitpool.Node
	NodeName(rawURI string) string
}

type TokenPool struct {
	net    *transport.NetworkClient
	cfg    config.ConfigProvider
	source NodeSource // 出口节点池消费端口；nil 时跳过候选轮询，仅走直连抓取
	fetch  func(proxyURI string) (string, error)
}

// fallbackPollWidth 返回 rT 兜底轮询的出口节点采样宽度：与竞速并发规格对齐
// （parallel_pool_size，配置层已钳制上限）。cfg 缺失或规格非法（<=0）时
// 退回历史固定值 5，保证零配置路径行为不变。
func fallbackPollWidth(cfg config.ConfigProvider) int {
	if cfg == nil {
		return 5
	}
	if size := cfg.ParallelPoolSize(); size > 0 {
		return size
	}
	return 5
}

func NewTokenPool(net *transport.NetworkClient, cfg config.ConfigProvider, source NodeSource) *TokenPool {
	return &TokenPool{net: net, cfg: cfg, source: source}
}

// NewTokenPoolCustom creates a token pool with a custom fetch function.
// Used for testing.
func NewTokenPoolCustom(fetch func(proxyURI string) (string, error)) *TokenPool {
	return &TokenPool{fetch: fetch}
}

// FetchTokenWithSession 在既有 Session 上执行 anchor→reload 校验
// （admin 节点测速路径的消费入口，委托共享核心实现）。
func (p *TokenPool) FetchTokenWithSession(ctx context.Context, sess *transport.Session) (string, error) {
	return FetchRecaptchaTokenWithSession(ctx, sess)
}

// GetTokenShared 每次调用都实时抓取一份独立的 reCAPTCHA token。
//
// 注意：不使用 singleflight 折叠并发——reCAPTCHA Enterprise token 是单次有效
// （single-use），并发请求必须各自持有独立 token，否则复用同一份会触发
// Google 侧验证失败（Failed to verify action / 429）。每个请求独立抓取，
// 由 15s 超时与前置池/出口节点降级路径兜底。
//
// ctx 参与生命周期管理：入口哨兵直接拒绝已取消的调用；15s 抓取预算自 ctx 派生
// （实际预算为 min(请求剩余时限, 15s)），请求死亡则抓取当场中止，杜绝幽灵抓取。
func (p *TokenPool) GetTokenShared(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if p.fetch != nil {
		return p.fetch("")
	}

	debugMode := false
	tryEntry := true
	if p.cfg != nil {
		debugMode = p.cfg.DebugMode()
		tryEntry = p.cfg.RecaptchaTryEntryOrDirect()
	}

	fetchCtx, fetchCancel := context.WithTimeout(ctx, 15*time.Second)
	defer fetchCancel()

	if tryEntry {
		tok, err := p.fetchToken(fetchCtx, "", debugMode)
		if err == nil && tok != "" {
			return tok, nil
		}
	}

	// Fallback or disabled entry: poll healthy candidate nodes.
	// 兜底轮询宽度与竞速并发规格对齐（parallel_pool_size），不再写死：
	// 竞速开多少候选，rT 降级采样面就铺多宽。
	var cands []exitpool.Node
	if p.source != nil {
		cands = p.source.SelectForParallel(fallbackPollWidth(p.cfg), debugMode)
	}
	var lastErr error
	for _, cand := range cands {
		tok, err := p.fetchToken(fetchCtx, cand.RawURI, debugMode)
		if err == nil && tok != "" {
			return tok, nil
		}
		lastErr = err
	}

	// If tryEntry was false or cands was empty, attempt direct fetch as last resort if not tried yet
	if !tryEntry {
		tok, err := p.fetchToken(fetchCtx, "", debugMode)
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
