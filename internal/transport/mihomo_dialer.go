package transport

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/constant"
)

type proxyInfo struct {
	proxy      constant.Proxy
	lastUsedAt time.Time
	closed     bool
}

type mihomoDialer struct {
	proxies map[string]*proxyInfo
	mu      sync.RWMutex
	cfg     ProxyDialerConfig
	ticker  *time.Ticker
	stopCh  chan struct{}
}

func NewMihomoDialer(cfg ProxyDialerConfig) ProxyDialer {
	d := &mihomoDialer{
		proxies: make(map[string]*proxyInfo),
		cfg:     cfg,
		stopCh:  make(chan struct{}),
	}
	if cfg.GCInterval > 0 {
		d.ticker = time.NewTicker(cfg.GCInterval)
		go d.gcLoop()
	}
	return d
}

func (d *mihomoDialer) gcLoop() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("StartProxyGC panic: %v\n%s", r, debug.Stack())
		}
	}()
	for {
		select {
		case <-d.stopCh:
			return
		case <-d.ticker.C:
			d.cleanupIdleProxies()
		}
	}
}

func (d *mihomoDialer) cleanupIdleProxies() {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	for uri, info := range d.proxies {
		if now.Sub(info.lastUsedAt) > d.cfg.MaxIdle {
			if !info.closed {
				info.closed = true
				if closer, ok := info.proxy.(interface{ Close() error }); ok {
					_ = closer.Close()
				}
				log.Printf("[Transport] 空闲代理已清理释放: %s", nodes.GetNodeName(uri))
			}
			delete(d.proxies, uri)
		}
	}
}

func (d *mihomoDialer) CreateDialer(uri string, reqID string) (func(ctx context.Context, network, addr string) (net.Conn, error), error) {
	d.mu.Lock()
	if info, ok := d.proxies[uri]; ok && !info.closed {
		info.lastUsedAt = time.Now()
		p := info.proxy
		d.mu.Unlock()
		return makeDialer(p), nil
	}
	d.mu.Unlock()

	log.Printf("[Transport] 请求ID=%s 触发代理初始化: %s", reqID, nodes.GetNodeName(uri))

	outMap, err := ParseURI(uri)
	if err != nil {
		return nil, fmt.Errorf("parse URI: %w", err)
	}

	proxy, err := adapter.ParseProxy(outMap)
	if err != nil {
		return nil, fmt.Errorf("parse proxy: %w", err)
	}

	d.mu.Lock()
	if old, ok := d.proxies[uri]; ok && !old.closed {
		old.closed = true
		if closer, ok := old.proxy.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	}
	d.proxies[uri] = &proxyInfo{proxy: proxy, lastUsedAt: time.Now()} //nolint:exhaustruct
	d.mu.Unlock()

	return makeDialer(proxy), nil
}

func makeDialer(p constant.Proxy) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("error: %w", err)

		}

		portInt, _ := strconv.Atoi(port)

		metadata := &constant.Metadata{ //nolint:exhaustruct
			NetWork: constant.TCP,
			Type:    constant.HTTP,
			Host:    host,
			DstPort: uint16(portInt),
		}

		conn, err := p.DialContext(ctx, metadata)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) {
				return nil, fmt.Errorf("error: %w", err)

			}
			return nil, fmt.Errorf("error: %w", err)

		}

		return conn, nil
	}
}

func (d *mihomoDialer) RemoveDialer(uri string) {
	d.mu.Lock()
	if info, ok := d.proxies[uri]; ok {
		if !info.closed {
			info.closed = true
			if closer, ok := info.proxy.(interface{ Close() error }); ok {
				_ = closer.Close()
			}
			log.Printf("[Transport] 代理节点已清理释放: %s", nodes.GetNodeName(uri))
		}
		delete(d.proxies, uri)
	}
	d.mu.Unlock()
}

func (d *mihomoDialer) StopAll() {
	if d.ticker != nil {
		d.ticker.Stop()
	}
	close(d.stopCh)

	d.mu.Lock()
	defer d.mu.Unlock()
	for _, info := range d.proxies {
		if !info.closed {
			info.closed = true
			if closer, ok := info.proxy.(interface{ Close() error }); ok {
				_ = closer.Close()
			}
		}
	}
	d.proxies = make(map[string]*proxyInfo)
}