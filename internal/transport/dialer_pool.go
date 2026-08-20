package transport

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/component/proxydialer"
	"github.com/metacubex/mihomo/constant"
)

// ProxyInstanceKey uniquely identifies a proxy route including optional first-hop entry.
type ProxyInstanceKey struct {
	ProxyURI string `json:"proxy_uri"`
	EntryURI string `json:"entry_uri"`
}

// NewProxyInstanceKey creates a structured proxy instance key with trimmed URIs.
func NewProxyInstanceKey(proxyURI string, entryURIs ...string) ProxyInstanceKey {
	entryURI := ""
	if len(entryURIs) > 0 {
		entryURI = strings.TrimSpace(entryURIs[0])
	}
	return ProxyInstanceKey{
		ProxyURI: strings.TrimSpace(proxyURI),
		EntryURI: entryURI,
	}
}

// CanonicalKey returns the normalized deduplicated string cache key.
func (k ProxyInstanceKey) CanonicalKey() string {
	proxyID := proxyIdentity(k.ProxyURI)
	entryID := proxyIdentity(k.EntryURI)
	if entryID == "" {
		return proxyID
	}
	return entryID + "\x00" + proxyID
}

// IsSelfReferencing checks if the entry proxy is the same as the target proxy.
func (k ProxyInstanceKey) IsSelfReferencing() bool {
	if k.EntryURI == "" || k.ProxyURI == "" {
		return false
	}
	return proxyIdentity(k.EntryURI) == proxyIdentity(k.ProxyURI)
}

// MatchesURI checks if the given URI is involved in either hop of this key.
func (k ProxyInstanceKey) MatchesURI(uri string) bool {
	id := proxyIdentity(uri)
	return proxyIdentity(k.ProxyURI) == id || proxyIdentity(k.EntryURI) == id
}

type proxyEntry struct {
	proxy        constant.Proxy
	dependencies []constant.Proxy
	key          ProxyInstanceKey
	proxyURI     string
	entryURI     string
	lastUsedAt   time.Time
	refCount     int64
	closed       bool
}

type initTask struct {
	done     chan struct{}
	err      error
	canceled bool
	key      ProxyInstanceKey
	proxyURI string
	entryURI string
}

type proxyBuilder func(map[string]any, ...adapter.ProxyOption) (constant.Proxy, error)

// ProxyDialerPool manages the lifecycle, caching, singleflight initialization,
// reference counting, and garbage collection of Mihomo proxy adapters.
type ProxyDialerPool struct {
	mu           sync.RWMutex
	proxies      map[string]*proxyEntry
	inFlight     map[string]*initTask
	nameResolver func(uri string) string
	builder      proxyBuilder
	closed       bool
	gcStop       chan struct{}
	gcRunning    atomic.Bool
}

// NewProxyDialerPool creates a new isolated ProxyDialerPool instance.
func NewProxyDialerPool(builder proxyBuilder) *ProxyDialerPool {
	if builder == nil {
		builder = func(mapping map[string]any, options ...adapter.ProxyOption) (constant.Proxy, error) {
			return adapter.ParseProxy(mapping, options...)
		}
	}
	return &ProxyDialerPool{
		proxies:  make(map[string]*proxyEntry),
		inFlight: make(map[string]*initTask),
		builder:  builder,
	}
}

// SetNameResolver configures a custom name resolver for log messages.
func (p *ProxyDialerPool) SetNameResolver(resolver func(uri string) string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nameResolver = resolver
}

func (p *ProxyDialerPool) displayName(uri string) string {
	p.mu.RLock()
	resolver := p.nameResolver
	p.mu.RUnlock()
	if resolver != nil {
		if name := resolver(uri); name != "" && name != "Unknown" {
			return name
		}
	}
	for _, candidate := range config.ListProxyCandidates() {
		if proxyIdentity(candidate.RawURI) == proxyIdentity(uri) && strings.TrimSpace(candidate.Name) != "" {
			return candidate.Name
		}
	}
	return "Unknown"
}

// GetDialer retrieves or initializes a proxy dialer for the given route key.
func (p *ProxyDialerPool) GetDialer(ctx context.Context, key ProxyInstanceKey, reqID string, debugMode bool) (func(ctx context.Context, network, addr string) (net.Conn, error), error) {
	return p.GetDialerWithBuilder(ctx, key, reqID, debugMode, p.builder)
}

// GetDialerWithBuilder retrieves or initializes a proxy dialer with an explicit builder function.
func (p *ProxyDialerPool) GetDialerWithBuilder(ctx context.Context, key ProxyInstanceKey, reqID string, debugMode bool, customBuilder proxyBuilder) (func(ctx context.Context, network, addr string) (net.Conn, error), error) {
	if key.IsSelfReferencing() {
		return nil, fmt.Errorf("入口代理不能与候选节点相同")
	}
	cacheKey := key.CanonicalKey()

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, errors.New("proxy dialer pool is closed")
	}

	if entry, ok := p.proxies[cacheKey]; ok && !entry.closed {
		entry.lastUsedAt = time.Now()
		proxyObj := entry.proxy
		p.mu.Unlock()
		return makeDialer(proxyObj, debugMode), nil
	}

	if pending, ok := p.inFlight[cacheKey]; ok {
		p.mu.Unlock()
		select {
		case <-pending.done:
			if pending.err != nil {
				return nil, pending.err
			}
			return p.GetDialerWithBuilder(ctx, key, reqID, debugMode, customBuilder)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	pending := &initTask{
		done:     make(chan struct{}),
		key:      key,
		proxyURI: key.ProxyURI,
		entryURI: key.EntryURI,
	}
	p.inFlight[cacheKey] = pending
	p.mu.Unlock()

	log.Printf("[Transport] 请求ID=%s 触发代理初始化: %s", reqID, p.displayName(key.ProxyURI))
	proxyObj, deps, initErr := p.buildProxy(key, customBuilder)

	p.mu.Lock()
	current, ownsInit := p.inFlight[cacheKey]
	if !ownsInit || current != pending || pending.canceled {
		if initErr == nil {
			initErr = errors.New("proxy initialization canceled")
		}
		pending.err = initErr
		if ownsInit && current == pending {
			delete(p.inFlight, cacheKey)
		}
		close(pending.done)
		p.mu.Unlock()
		closeMihomoProxies(proxyObj, deps)
		return nil, initErr
	}

	if initErr != nil {
		pending.err = initErr
		delete(p.inFlight, cacheKey)
		close(pending.done)
		p.mu.Unlock()
		return nil, initErr
	}

	p.proxies[cacheKey] = &proxyEntry{
		proxy:        proxyObj,
		dependencies: deps,
		key:          key,
		proxyURI:     key.ProxyURI,
		entryURI:     key.EntryURI,
		lastUsedAt:   time.Now(),
		refCount:     0,
		closed:       false,
	}
	delete(p.inFlight, cacheKey)
	close(pending.done)
	p.mu.Unlock()

	return makeDialer(proxyObj, debugMode), nil
}

func (p *ProxyDialerPool) buildProxy(key ProxyInstanceKey, builder proxyBuilder) (proxy constant.Proxy, dependencies []constant.Proxy, err error) {
	if builder == nil {
		builder = p.builder
	}
	if builder == nil {
		builder = func(mapping map[string]any, options ...adapter.ProxyOption) (constant.Proxy, error) {
			return adapter.ParseProxy(mapping, options...)
		}
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			closeMihomoProxies(proxy, dependencies)
			proxy = nil
			dependencies = nil
			err = fmt.Errorf("parse proxy panic: %v", recovered)
		}
	}()

	if key.EntryURI == "" {
		outMap, parseErr := ParseURI(key.ProxyURI)
		if parseErr != nil {
			return nil, nil, fmt.Errorf("parse URI: %w", parseErr)
		}
		proxy, err = builder(outMap)
		if err != nil {
			return nil, nil, fmt.Errorf("parse proxy: %w", err)
		}
		return proxy, nil, nil
	}

	if key.IsSelfReferencing() {
		return nil, nil, fmt.Errorf("入口代理不能与候选节点相同")
	}

	entryMap, err := ParseURI(key.EntryURI)
	if err != nil {
		return nil, nil, &EntryProxyError{EntryURI: key.EntryURI, Err: fmt.Errorf("parse entry proxy URI: %w", err)}
	}
	entryProxy, err := builder(entryMap)
	if err != nil {
		return nil, nil, &EntryProxyError{EntryURI: key.EntryURI, Err: fmt.Errorf("parse entry proxy: %w", err)}
	}
	dependencies = []constant.Proxy{entryProxy}

	outMap, err := ParseURI(key.ProxyURI)
	if err != nil {
		closeMihomoProxies(nil, dependencies)
		return nil, nil, fmt.Errorf("parse proxy: %w", err)
	}

	proxy, err = builder(outMap, adapter.WithDialerForAPI(proxydialer.New(entryProxy, true)))
	if err != nil {
		closeMihomoProxies(nil, dependencies)
		return nil, nil, fmt.Errorf("parse proxy chain: %w", err)
	}
	return proxy, dependencies, nil
}

// Remove cleans up and cancels any proxies matching the given URI.
func (p *ProxyDialerPool) Remove(uri string) {
	var toClose []proxyEntry
	p.mu.Lock()
	for _, pending := range p.inFlight {
		if pending.key.MatchesURI(uri) {
			pending.canceled = true
		}
	}
	for key, entry := range p.proxies {
		if entry.key.MatchesURI(uri) {
			if !entry.closed {
				entry.closed = true
				toClose = append(toClose, *entry)
			}
			delete(p.proxies, key)
		}
	}
	p.mu.Unlock()

	for _, item := range toClose {
		closeMihomoProxies(item.proxy, item.dependencies)
	}
	if len(toClose) > 0 {
		log.Printf("[Transport] 代理节点已清理释放: %s", p.displayName(uri))
	}
}

// CleanupIdle purges expired idle proxy instances.
func (p *ProxyDialerPool) CleanupIdle(maxIdle time.Duration) {
	var idle []proxyEntry
	p.mu.Lock()
	now := time.Now()
	for cacheKey, entry := range p.proxies {
		if now.Sub(entry.lastUsedAt) > maxIdle && entry.refCount <= 0 {
			if !entry.closed {
				entry.closed = true
				idle = append(idle, *entry)
			}
			delete(p.proxies, cacheKey)
		}
	}
	p.mu.Unlock()

	for _, item := range idle {
		closeMihomoProxies(item.proxy, item.dependencies)
		log.Printf("[Transport] 空闲代理已清理释放: %s", p.displayName(item.key.ProxyURI))
	}
}

// StartGC launches a background goroutine to clean up idle proxies.
func (p *ProxyDialerPool) StartGC(interval, maxIdle time.Duration) {
	if !p.gcRunning.CompareAndSwap(false, true) {
		return
	}
	p.gcStop = make(chan struct{})
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("ProxyDialerPool StartGC panic: %v\n%s", r, debug.Stack())
			}
			p.gcRunning.Store(false)
		}()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				p.CleanupIdle(maxIdle)
			case <-p.gcStop:
				return
			}
		}
	}()
}

// StopGC stops the background garbage collection goroutine.
func (p *ProxyDialerPool) StopGC() {
	if p.gcRunning.CompareAndSwap(true, false) {
		if p.gcStop != nil {
			close(p.gcStop)
		}
	}
}

// StopAll gracefully tears down all proxy instances and stops GC.
func (p *ProxyDialerPool) StopAll() {
	p.StopGC()
	var toClose []proxyEntry
	p.mu.Lock()
	for _, pending := range p.inFlight {
		pending.canceled = true
	}
	for _, entry := range p.proxies {
		if !entry.closed {
			entry.closed = true
			toClose = append(toClose, *entry)
		}
	}
	clear(p.proxies)
	p.mu.Unlock()

	for _, item := range toClose {
		closeMihomoProxies(item.proxy, item.dependencies)
	}
}

func makeDialer(p constant.Proxy, debugMode bool) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("split host port: %w", err)
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
				return nil, fmt.Errorf("dial context canceled: %w", err)
			}
			if debugMode {
				log.Printf("[Transport] Mihomo 拨号失败 [%s:%d]: %v", host, portInt, err)
			}
			return nil, fmt.Errorf("mihomo dial: %w", err)
		}

		return conn, nil
	}
}

func closeMihomoProxies(proxy constant.Proxy, dependencies []constant.Proxy) {
	closeMihomoProxy(proxy)
	for _, dependency := range dependencies {
		closeMihomoProxy(dependency)
	}
}

func closeMihomoProxy(proxy constant.Proxy) {
	if proxy == nil {
		return
	}
	if closer, ok := proxy.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
}
