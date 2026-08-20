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
	"time"

	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/component/proxydialer"
	"github.com/metacubex/mihomo/constant"
)

type proxyInfo struct {
	proxy        constant.Proxy
	dependencies []constant.Proxy
	proxyURI     string
	entryURI     string
	lastUsedAt   time.Time
	closed       bool
}

type proxyInitState struct {
	done     chan struct{}
	err      error
	canceled bool
	proxyURI string
	entryURI string
}

type proxyBuilder func(map[string]any, ...adapter.ProxyOption) (constant.Proxy, error)

type ProxyManager struct {
	mu       sync.RWMutex
	proxies  map[string]*proxyInfo
	pending  map[string]*proxyInitState
	resolver func(uri string) string
}

func NewProxyManager(resolver func(uri string) string) *ProxyManager {
	return &ProxyManager{proxies: make(map[string]*proxyInfo), pending: make(map[string]*proxyInitState), resolver: resolver}
}

var defaultProxyManager = NewProxyManager(nil) //nolint:gochecknoglobals // compatibility entry points only

// Compatibility aliases are retained for package tests and legacy callers.
var (
	proxyMap     = defaultProxyManager.proxies //nolint:gochecknoglobals
	proxyInitMap = defaultProxyManager.pending //nolint:gochecknoglobals
	proxyMutex   = &defaultProxyManager.mu     //nolint:gochecknoglobals
)

func getOrStartProxyDialer(uri string, reqID string, debugMode bool, entryURIs ...string) (func(ctx context.Context, network, addr string) (net.Conn, error), error) {
	return defaultProxyManager.getOrStartProxyDialerWithBuilder(uri, reqID, debugMode, func(mapping map[string]any, options ...adapter.ProxyOption) (constant.Proxy, error) {
		return adapter.ParseProxy(mapping, options...)
	}, entryURIs...)
}

// ValidateProxyURI verifies that the URI can construct a mihomo proxy in the current build.
func ValidateProxyURI(uri string) error {
	proxy, dependencies, err := buildMihomoProxy(uri, "", func(mapping map[string]any, options ...adapter.ProxyOption) (constant.Proxy, error) {
		return adapter.ParseProxy(mapping, options...)
	})
	if err != nil {
		return err
	}
	closeMihomoProxies(proxy, dependencies)
	return nil
}

func getOrStartProxyDialerWithBuilder(uri string, reqID string, debugMode bool, builder proxyBuilder, entryURIs ...string) (func(ctx context.Context, network, addr string) (net.Conn, error), error) {
	return defaultProxyManager.getOrStartProxyDialerWithBuilder(uri, reqID, debugMode, builder, entryURIs...)
}

func (m *ProxyManager) getOrStartProxyDialer(uri string, reqID string, debugMode bool, entryURIs ...string) (func(ctx context.Context, network, addr string) (net.Conn, error), error) {
	return m.getOrStartProxyDialerWithBuilder(uri, reqID, debugMode, func(mapping map[string]any, options ...adapter.ProxyOption) (constant.Proxy, error) {
		return adapter.ParseProxy(mapping, options...)
	}, entryURIs...)
}

func (m *ProxyManager) getOrStartProxyDialerWithBuilder(uri string, reqID string, debugMode bool, builder proxyBuilder, entryURIs ...string) (func(ctx context.Context, network, addr string) (net.Conn, error), error) {
	entryURI := ""
	if len(entryURIs) > 0 {
		entryURI = strings.TrimSpace(entryURIs[0])
	}
	if entryURI != "" && proxyIdentity(entryURI) == proxyIdentity(uri) {
		return nil, fmt.Errorf("入口代理不能与候选节点相同")
	}
	cacheKey := proxyCacheKey(uri, entryURI)
	m.mu.Lock()
	if info, ok := m.proxies[cacheKey]; ok && !info.closed {
		info.lastUsedAt = time.Now()
		p := info.proxy
		m.mu.Unlock()
		return makeDialer(p, debugMode), nil
	}
	if pending, ok := m.pending[cacheKey]; ok {
		m.mu.Unlock()
		<-pending.done
		if pending.err != nil {
			return nil, pending.err
		}
		return m.getOrStartProxyDialerWithBuilder(uri, reqID, debugMode, builder, entryURI)
	}
	pending := &proxyInitState{done: make(chan struct{}), proxyURI: uri, entryURI: entryURI}
	m.pending[cacheKey] = pending
	m.mu.Unlock()

	log.Printf("[Transport] 请求ID=%s 触发代理初始化: %s", reqID, m.proxyDisplayName(uri))
	proxy, dependencies, initErr := buildMihomoProxy(uri, entryURI, builder)

	m.mu.Lock()
	current, ownsInit := m.pending[cacheKey]
	if !ownsInit || current != pending || pending.canceled {
		if initErr == nil {
			initErr = errors.New("proxy initialization canceled")
		}
		pending.err = initErr
		if ownsInit && current == pending {
			delete(m.pending, cacheKey)
		}
		close(pending.done)
		m.mu.Unlock()
		closeMihomoProxies(proxy, dependencies)
		return nil, initErr
	}
	if initErr != nil {
		pending.err = initErr
		delete(m.pending, cacheKey)
		close(pending.done)
		m.mu.Unlock()
		return nil, initErr
	}

	m.proxies[cacheKey] = &proxyInfo{
		proxy: proxy, dependencies: dependencies, proxyURI: uri, entryURI: entryURI, lastUsedAt: time.Now(),
	}
	delete(m.pending, cacheKey)
	close(pending.done)
	m.mu.Unlock()
	return makeDialer(proxy, debugMode), nil
}

func proxyCacheKey(uri, entryURI string) string {
	proxyID := proxyIdentity(uri)
	entryID := proxyIdentity(entryURI)
	if entryID == "" {
		return proxyID
	}
	return entryID + "\x00" + proxyID
}

func proxyIdentity(uri string) string {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return ""
	}
	if identity, err := ProxyIdentity(uri); err == nil {
		return identity.SemanticFingerprint
	}
	return strings.SplitN(uri, "#", 2)[0]
}

func buildMihomoProxy(uri, entryURI string, builder proxyBuilder) (proxy constant.Proxy, dependencies []constant.Proxy, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			closeMihomoProxies(proxy, dependencies)
			proxy = nil
			dependencies = nil
			err = fmt.Errorf("parse proxy panic: %v", recovered)
		}
	}()
	outMap, err := ParseURI(uri)
	if err != nil {
		return nil, nil, fmt.Errorf("parse URI: %w", err)
	}

	if entryURI == "" {
		proxy, err = builder(outMap)
		if err != nil {
			return nil, nil, fmt.Errorf("parse proxy: %w", err)
		}
		return proxy, nil, nil
	}
	if proxyIdentity(entryURI) == proxyIdentity(uri) {
		return nil, nil, fmt.Errorf("入口代理不能与候选节点相同")
	}

	entryMap, err := ParseURI(entryURI)
	if err != nil {
		return nil, nil, fmt.Errorf("parse entry proxy URI: %w", err)
	}
	entryProxy, err := builder(entryMap)
	if err != nil {
		return nil, nil, fmt.Errorf("parse entry proxy: %w", err)
	}
	dependencies = []constant.Proxy{entryProxy}
	proxy, err = builder(outMap, adapter.WithDialerForAPI(proxydialer.New(entryProxy, true)))
	if err != nil {
		closeMihomoProxies(nil, dependencies)
		return nil, nil, fmt.Errorf("parse proxy chain: %w", err)
	}
	return proxy, dependencies, nil
}

func makeDialer(p constant.Proxy, debugMode bool) func(ctx context.Context, network, addr string) (net.Conn, error) {
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
			// 若是因为上下文取消导致拨号中止，属于并发竞速中的正常现象，直接退出，不打印误报
			if ctx.Err() != nil || errors.Is(err, context.Canceled) {
				return nil, fmt.Errorf("error: %w", err)

			}
			if debugMode {
				log.Printf("[Transport] Mihomo 拨号失败 [%s:%d]: %v", host, portInt, err)
			}
			return nil, fmt.Errorf("error: %w", err)

		}

		return conn, nil
	}
}

// RemoveProxy 主动清理代理实例 (响应面板删除节点)
func RemoveProxy(uri string) {
	defaultProxyManager.RemoveProxy(uri)
}

func (m *ProxyManager) RemoveProxy(uri string) {
	type proxySet struct {
		proxy        constant.Proxy
		dependencies []constant.Proxy
	}
	var proxies []proxySet
	m.mu.Lock()
	for _, pending := range m.pending {
		if proxyIdentity(pending.proxyURI) == proxyIdentity(uri) || proxyIdentity(pending.entryURI) == proxyIdentity(uri) {
			pending.canceled = true
		}
	}
	for key, info := range m.proxies {
		if proxyIdentity(info.proxyURI) == proxyIdentity(uri) || proxyIdentity(info.entryURI) == proxyIdentity(uri) {
			if !info.closed {
				info.closed = true
				proxies = append(proxies, proxySet{proxy: info.proxy, dependencies: info.dependencies})
			}
			delete(m.proxies, key)
		}
	}
	m.mu.Unlock()
	for _, item := range proxies {
		closeMihomoProxies(item.proxy, item.dependencies)
	}
	if len(proxies) > 0 {
		log.Printf("[Transport] 代理节点已清理释放: %s", m.proxyDisplayName(uri))
	}
}

// StartProxyGC 启动后台空闲实例垃圾回收 (每隔 interval 扫描，超时 maxIdle 回收)
func StartProxyGC(interval, maxIdle time.Duration) {
	StartProxyGCWithContext(context.Background(), interval, maxIdle)
}

// StartProxyGCWithContext starts a lifecycle-owned idle proxy collector.
func StartProxyGCWithContext(ctx context.Context, interval, maxIdle time.Duration) <-chan struct{} {
	return defaultProxyManager.StartGC(ctx, interval, maxIdle)
}

func (m *ProxyManager) StartGC(ctx context.Context, interval, maxIdle time.Duration) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				log.Printf("StartProxyGC panic: %v\n%s", r, debug.Stack())
			}
		}()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.cleanupIdleProxies(maxIdle)
			}
		}
	}()
	return done
}

func cleanupIdleProxies(maxIdle time.Duration) {
	defaultProxyManager.cleanupIdleProxies(maxIdle)
}

func (m *ProxyManager) cleanupIdleProxies(maxIdle time.Duration) {
	type idleProxy struct {
		uri          string
		proxy        constant.Proxy
		dependencies []constant.Proxy
	}
	var idle []idleProxy
	m.mu.Lock()
	now := time.Now()
	for cacheKey, info := range m.proxies {
		if now.Sub(info.lastUsedAt) > maxIdle {
			if !info.closed {
				info.closed = true
				idle = append(idle, idleProxy{uri: info.proxyURI, proxy: info.proxy, dependencies: info.dependencies})
			}
			delete(m.proxies, cacheKey)
		}
	}
	m.mu.Unlock()
	for _, item := range idle {
		closeMihomoProxies(item.proxy, item.dependencies)
		log.Printf("[Transport] 空闲代理已清理释放: %s", m.proxyDisplayName(item.uri))
	}
}

// SetProxyNameResolver sets the external name resolver to avoid import cycles.
func SetProxyNameResolver(resolver func(uri string) string) {
	defaultProxyManager.SetNameResolver(resolver)
}

func (m *ProxyManager) SetNameResolver(resolver func(uri string) string) {
	m.mu.Lock()
	m.resolver = resolver
	m.mu.Unlock()
}

func proxyDisplayName(uri string) string { return defaultProxyManager.proxyDisplayName(uri) }

func (m *ProxyManager) proxyDisplayName(uri string) string {
	m.mu.RLock()
	resolver := m.resolver
	m.mu.RUnlock()
	if resolver != nil {
		if name := resolver(uri); name != "" && name != "Unknown" {
			return name
		}
	}
	return "Unknown"
}

// StopAllProxies 程序优雅退出时清理全部实例
func StopAllProxies() {
	defaultProxyManager.Close()
}

func (m *ProxyManager) Close() {
	type proxySet struct {
		proxy        constant.Proxy
		dependencies []constant.Proxy
	}
	var proxies []proxySet
	m.mu.Lock()
	for _, pending := range m.pending {
		pending.canceled = true
	}
	for _, info := range m.proxies {
		if !info.closed {
			info.closed = true
			proxies = append(proxies, proxySet{proxy: info.proxy, dependencies: info.dependencies})
		}
	}
	for key := range m.proxies {
		delete(m.proxies, key)
	}
	m.mu.Unlock()
	for _, item := range proxies {
		closeMihomoProxies(item.proxy, item.dependencies)
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
