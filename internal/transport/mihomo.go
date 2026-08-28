package transport

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
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
	activeRefs   int
}

const maxProxyCacheEntries = 512

type proxyInitState struct {
	done     chan struct{}
	err      error
	canceled bool
	proxyURI string
	entryURI string
}

type proxyBuilder func(map[string]any, ...adapter.ProxyOption) (constant.Proxy, error)

var (
	//nolint:gochecknoglobals // Internal proxy connection cache
	proxyMap = make(map[string]*proxyInfo)
	//nolint:gochecknoglobals // Coordinates first use of the same proxy URI
	proxyInitMap = make(map[string]*proxyInitState)
	//nolint:gochecknoglobals // Internal proxy connection cache
	proxyMutex sync.RWMutex
)

func getOrStartProxyDialer(uri string, reqID string, debugMode bool, entryURIs ...string) (func(ctx context.Context, network, addr string) (net.Conn, error), error) {
	return getOrStartProxyDialerContext(context.Background(), uri, reqID, debugMode, entryURIs...)
}

func getOrStartProxyDialerContext(ctx context.Context, uri string, reqID string, debugMode bool, entryURIs ...string) (func(context.Context, string, string) (net.Conn, error), error) {
	return getOrStartProxyDialerWithBuilderContext(ctx, uri, reqID, debugMode, func(mapping map[string]any, options ...adapter.ProxyOption) (constant.Proxy, error) {
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
	return getOrStartProxyDialerWithBuilderContext(context.Background(), uri, reqID, debugMode, builder, entryURIs...)
}

func getOrStartProxyDialerWithBuilderContext(ctx context.Context, uri string, reqID string, debugMode bool, builder proxyBuilder, entryURIs ...string) (func(ctx context.Context, network, addr string) (net.Conn, error), error) {
	entryURI := ""
	if len(entryURIs) > 0 {
		entryURI = strings.TrimSpace(entryURIs[0])
	}
	if entryURI != "" && proxyIdentity(entryURI) == proxyIdentity(uri) {
		return nil, fmt.Errorf("入口代理不能与候选节点相同")
	}
	cacheKey := proxyCacheKey(uri, entryURI)
	proxyMutex.Lock()
	if info, ok := proxyMap[cacheKey]; ok && !info.closed {
		info.lastUsedAt = time.Now()
		p := info.proxy
		proxyMutex.Unlock()
		return makeTrackedDialer(p, info, debugMode), nil
	}
	if pending, ok := proxyInitMap[cacheKey]; ok {
		proxyMutex.Unlock()
		select {
		case <-pending.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(30 * time.Second):
			return nil, errors.New("proxy initialization timed out")
		}
		if pending.err != nil {
			return nil, pending.err
		}
		return getOrStartProxyDialerWithBuilderContext(ctx, uri, reqID, debugMode, builder, entryURI)
	}
	pending := &proxyInitState{done: make(chan struct{}), proxyURI: uri, entryURI: entryURI}
	proxyInitMap[cacheKey] = pending
	proxyMutex.Unlock()

	log.Printf("[Transport] 请求ID=%s 触发代理初始化: %s", reqID, proxyDisplayName(uri))
	proxy, dependencies, initErr := buildMihomoProxy(uri, entryURI, builder)

	proxyMutex.Lock()
	current, ownsInit := proxyInitMap[cacheKey]
	if !ownsInit || current != pending || pending.canceled {
		if initErr == nil {
			initErr = errors.New("proxy initialization canceled")
		}
		pending.err = initErr
		if ownsInit && current == pending {
			delete(proxyInitMap, cacheKey)
		}
		close(pending.done)
		proxyMutex.Unlock()
		closeMihomoProxies(proxy, dependencies)
		return nil, initErr
	}
	if initErr != nil {
		pending.err = initErr
		delete(proxyInitMap, cacheKey)
		close(pending.done)
		proxyMutex.Unlock()
		return nil, initErr
	}

	proxyMap[cacheKey] = &proxyInfo{
		proxy: proxy, dependencies: dependencies, proxyURI: uri, entryURI: entryURI, lastUsedAt: time.Now(),
	}
	info := proxyMap[cacheKey]
	var evicted []*proxyInfo
	if len(proxyMap) > maxProxyCacheEntries {
		evicted = evictInactiveLRULocked(len(proxyMap) - maxProxyCacheEntries)
	}
	delete(proxyInitMap, cacheKey)
	close(pending.done)
	proxyMutex.Unlock()
	for _, item := range evicted {
		closeMihomoProxies(item.proxy, item.dependencies)
	}
	return makeTrackedDialer(proxy, info, debugMode), nil
}

// evictInactiveLRULocked removes the least recently used inactive entries.
// proxyMutex must be held by the caller.
func evictInactiveLRULocked(limit int) []*proxyInfo {
	if limit <= 0 {
		return nil
	}
	type candidate struct {
		key  string
		info *proxyInfo
	}
	candidates := make([]candidate, 0, len(proxyMap))
	for key, info := range proxyMap {
		if info.closed || info.activeRefs > 0 {
			continue
		}
		candidates = append(candidates, candidate{key: key, info: info})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].info.lastUsedAt.Before(candidates[j].info.lastUsedAt)
	})
	if limit > len(candidates) {
		limit = len(candidates)
	}
	evicted := make([]*proxyInfo, 0, limit)
	for _, item := range candidates[:limit] {
		item.info.closed = true
		delete(proxyMap, item.key)
		evicted = append(evicted, item.info)
	}
	return evicted
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
	if normalized, err := config.NormalizeProxyURI(uri); err == nil {
		return normalized
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
	return makeTrackedDialer(p, nil, debugMode)
}

type trackedConn struct {
	net.Conn
	info *proxyInfo
	once sync.Once
}

func (c *trackedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() {
		proxyMutex.Lock()
		if c.info.activeRefs > 0 {
			c.info.activeRefs--
		}
		proxyMutex.Unlock()
	})
	return err
}

func makeTrackedDialer(p constant.Proxy, info *proxyInfo, debugMode bool) func(ctx context.Context, network, addr string) (net.Conn, error) {
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

		if info != nil {
			proxyMutex.Lock()
			if info.closed {
				proxyMutex.Unlock()
				return nil, errors.New("proxy cache entry closed")
			}
			info.activeRefs++
			proxyMutex.Unlock()
		}
		conn, err := p.DialContext(ctx, metadata)
		if err != nil {
			if info != nil {
				proxyMutex.Lock()
				if info.activeRefs > 0 {
					info.activeRefs--
				}
				proxyMutex.Unlock()
			}
			// 若是因为上下文取消导致拨号中止，属于并发竞速中的正常现象，直接退出，不打印误报
			if ctx.Err() != nil || errors.Is(err, context.Canceled) {
				return nil, fmt.Errorf("error: %w", err)

			}
			if debugMode {
				log.Printf("[Transport] Mihomo 拨号失败 [%s:%d]: %v", host, portInt, err)
			}
			return nil, fmt.Errorf("error: %w", err)

		}

		if info != nil {
			return &trackedConn{Conn: conn, info: info}, nil
		}
		return conn, nil
	}
}

// RemoveProxy 主动清理代理实例 (响应面板删除节点)
func RemoveProxy(uri string) {
	type proxySet struct {
		proxy        constant.Proxy
		dependencies []constant.Proxy
	}
	var proxies []proxySet
	proxyMutex.Lock()
	for _, pending := range proxyInitMap {
		if proxyIdentity(pending.proxyURI) == proxyIdentity(uri) || proxyIdentity(pending.entryURI) == proxyIdentity(uri) {
			pending.canceled = true
		}
	}
	for key, info := range proxyMap {
		if proxyIdentity(info.proxyURI) == proxyIdentity(uri) || proxyIdentity(info.entryURI) == proxyIdentity(uri) {
			if !info.closed {
				info.closed = true
				proxies = append(proxies, proxySet{proxy: info.proxy, dependencies: info.dependencies})
			}
			delete(proxyMap, key)
		}
	}
	proxyMutex.Unlock()
	for _, item := range proxies {
		closeMihomoProxies(item.proxy, item.dependencies)
	}
	if len(proxies) > 0 {
		log.Printf("[Transport] 代理节点已清理释放: %s", proxyDisplayName(uri))
	}
}

// StartProxyGC 启动后台空闲实例垃圾回收 (每隔 interval 扫描，超时 maxIdle 回收)
func StartProxyGC(interval, maxIdle time.Duration) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("StartProxyGC panic: %v\n%s", r, debug.Stack())
			}
		}()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			cleanupIdleProxies(maxIdle)
		}
	}()
}

func cleanupIdleProxies(maxIdle time.Duration) {
	type idleProxy struct {
		uri          string
		proxy        constant.Proxy
		dependencies []constant.Proxy
	}
	var idle []idleProxy
	proxyMutex.Lock()
	now := time.Now()
	for cacheKey, info := range proxyMap {
		if now.Sub(info.lastUsedAt) > maxIdle && info.activeRefs == 0 {
			if !info.closed {
				info.closed = true
				idle = append(idle, idleProxy{uri: info.proxyURI, proxy: info.proxy, dependencies: info.dependencies})
			}
			delete(proxyMap, cacheKey)
		}
	}
	proxyMutex.Unlock()
	for _, item := range idle {
		closeMihomoProxies(item.proxy, item.dependencies)
		log.Printf("[Transport] 空闲代理已清理释放: %s", proxyDisplayName(item.uri))
	}
}

func proxyDisplayName(uri string) string {
	if name := nodes.GetNodeName(uri); name != "Unknown" {
		return name
	}
	for _, candidate := range config.ListProxyCandidates() {
		if proxyIdentity(candidate.RawURI) == proxyIdentity(uri) && strings.TrimSpace(candidate.Name) != "" {
			return candidate.Name
		}
	}
	return "Unknown"
}

// StopAllProxies 程序优雅退出时清理全部实例
func StopAllProxies() {
	type proxySet struct {
		proxy        constant.Proxy
		dependencies []constant.Proxy
	}
	var proxies []proxySet
	proxyMutex.Lock()
	for _, pending := range proxyInitMap {
		pending.canceled = true
	}
	for _, info := range proxyMap {
		if !info.closed {
			info.closed = true
			proxies = append(proxies, proxySet{proxy: info.proxy, dependencies: info.dependencies})
		}
	}
	proxyMap = make(map[string]*proxyInfo)
	proxyMutex.Unlock()
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
