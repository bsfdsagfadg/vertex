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

type proxyInitState struct {
	done     chan struct{}
	err      error
	canceled bool
}

type proxyBuilder func(map[string]any) (constant.Proxy, error)

var (
	//nolint:gochecknoglobals // Internal proxy connection cache
	proxyMap = make(map[string]*proxyInfo)
	//nolint:gochecknoglobals // Coordinates first use of the same proxy URI
	proxyInitMap = make(map[string]*proxyInitState)
	//nolint:gochecknoglobals // Internal proxy connection cache
	proxyMutex sync.RWMutex
)

func getOrStartProxyDialer(uri string, reqID string, debugMode bool) (func(ctx context.Context, network, addr string) (net.Conn, error), error) {
	return getOrStartProxyDialerWithBuilder(uri, reqID, debugMode, func(mapping map[string]any) (constant.Proxy, error) {
		return adapter.ParseProxy(mapping)
	})
}

func getOrStartProxyDialerWithBuilder(uri string, reqID string, debugMode bool, builder proxyBuilder) (func(ctx context.Context, network, addr string) (net.Conn, error), error) {
	proxyMutex.Lock()
	if info, ok := proxyMap[uri]; ok && !info.closed {
		info.lastUsedAt = time.Now()
		p := info.proxy
		proxyMutex.Unlock()
		return makeDialer(p, debugMode), nil
	}
	if pending, ok := proxyInitMap[uri]; ok {
		proxyMutex.Unlock()
		<-pending.done
		if pending.err != nil {
			return nil, pending.err
		}
		return getOrStartProxyDialerWithBuilder(uri, reqID, debugMode, builder)
	}
	pending := &proxyInitState{done: make(chan struct{})}
	proxyInitMap[uri] = pending
	proxyMutex.Unlock()

	log.Printf("[Transport] 请求ID=%s 触发代理初始化: %s", reqID, nodes.GetNodeName(uri))
	proxy, initErr := buildMihomoProxy(uri, builder)

	proxyMutex.Lock()
	current, ownsInit := proxyInitMap[uri]
	if !ownsInit || current != pending || pending.canceled {
		if initErr == nil {
			initErr = errors.New("proxy initialization canceled")
		}
		pending.err = initErr
		if ownsInit && current == pending {
			delete(proxyInitMap, uri)
		}
		close(pending.done)
		proxyMutex.Unlock()
		closeMihomoProxy(proxy)
		return nil, initErr
	}
	if initErr != nil {
		pending.err = initErr
		delete(proxyInitMap, uri)
		close(pending.done)
		proxyMutex.Unlock()
		return nil, initErr
	}

	proxyMap[uri] = &proxyInfo{proxy: proxy, lastUsedAt: time.Now()} //nolint:exhaustruct
	delete(proxyInitMap, uri)
	close(pending.done)
	proxyMutex.Unlock()
	return makeDialer(proxy, debugMode), nil
}

func buildMihomoProxy(uri string, builder proxyBuilder) (proxy constant.Proxy, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("parse proxy panic: %v", recovered)
		}
	}()
	outMap, err := ParseURI(uri)
	if err != nil {
		return nil, fmt.Errorf("parse URI: %w", err)
	}

	proxy, err = builder(outMap)
	if err != nil {
		return nil, fmt.Errorf("parse proxy: %w", err)
	}
	return proxy, nil
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
	var proxy constant.Proxy
	proxyMutex.Lock()
	if pending, ok := proxyInitMap[uri]; ok {
		pending.canceled = true
	}
	if info, ok := proxyMap[uri]; ok {
		if !info.closed {
			info.closed = true
			proxy = info.proxy
		}
		delete(proxyMap, uri)
	}
	proxyMutex.Unlock()
	if proxy != nil {
		closeMihomoProxy(proxy)
		log.Printf("[Transport] 代理节点已清理释放: %s", nodes.GetNodeName(uri))
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
		uri   string
		proxy constant.Proxy
	}
	var idle []idleProxy
	proxyMutex.Lock()
	now := time.Now()
	for uri, info := range proxyMap {
		if now.Sub(info.lastUsedAt) > maxIdle {
			if !info.closed {
				info.closed = true
				idle = append(idle, idleProxy{uri: uri, proxy: info.proxy})
			}
			delete(proxyMap, uri)
		}
	}
	proxyMutex.Unlock()
	for _, item := range idle {
		closeMihomoProxy(item.proxy)
		log.Printf("[Transport] 空闲代理已清理释放: %s", nodes.GetNodeName(item.uri))
	}
}

// StopAllProxies 程序优雅退出时清理全部实例
func StopAllProxies() {
	var proxies []constant.Proxy
	proxyMutex.Lock()
	for _, pending := range proxyInitMap {
		pending.canceled = true
	}
	for _, info := range proxyMap {
		if !info.closed {
			info.closed = true
			proxies = append(proxies, info.proxy)
		}
	}
	proxyMap = make(map[string]*proxyInfo)
	proxyMutex.Unlock()
	for _, proxy := range proxies {
		closeMihomoProxy(proxy)
	}
}

func closeMihomoProxy(proxy constant.Proxy) {
	if closer, ok := proxy.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
}
