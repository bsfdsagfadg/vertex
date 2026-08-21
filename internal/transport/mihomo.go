package transport

import (
	"context"
	"net"
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/constant"
)

// DefaultPool is the global singleton ProxyDialerPool.
var DefaultPool = NewProxyDialerPool(nil) //nolint:gochecknoglobals


func getOrStartProxyDialer(uri string, reqID string, debugMode bool, entryURIs ...string) (func(ctx context.Context, network, addr string) (net.Conn, error), error) {
	key := NewProxyInstanceKey(uri, entryURIs...)
	return DefaultPool.GetDialer(context.Background(), key, reqID, debugMode)
}

func getOrStartProxyDialerWithBuilder(uri string, reqID string, debugMode bool, builder proxyBuilder, entryURIs ...string) (func(ctx context.Context, network, addr string) (net.Conn, error), error) {
	key := NewProxyInstanceKey(uri, entryURIs...)
	return DefaultPool.GetDialerWithBuilder(context.Background(), key, reqID, debugMode, builder)
}

// ValidateProxyURI verifies that the URI can construct a mihomo proxy in the current build.
func ValidateProxyURI(uri string) error {
	key := NewProxyInstanceKey(uri)
	proxy, dependencies, err := DefaultPool.buildProxy(key, func(mapping map[string]any, options ...adapter.ProxyOption) (constant.Proxy, error) {
		return adapter.ParseProxy(mapping, options...)
	})
	if err != nil {
		return err
	}
	closeMihomoProxies(proxy, dependencies)
	return nil
}

func proxyCacheKey(uri, entryURI string) string {
	return NewProxyInstanceKey(uri, entryURI).CanonicalKey()
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

// RemoveProxy 主动清理代理实例 (响应面板删除节点)
func RemoveProxy(uri string) {
	DefaultPool.Remove(uri)
}

// StartProxyGC 启动后台空闲实例垃圾回收 (每隔 interval 扫描，超时 maxIdle 回收)
func StartProxyGC(interval, maxIdle time.Duration) {
	DefaultPool.StartGC(interval, maxIdle)
}

// SetProxyNameResolver sets the external name resolver to avoid import cycles.
func SetProxyNameResolver(resolver func(uri string) string) {
	DefaultPool.SetNameResolver(resolver)
}

// StopAllProxies 程序优雅退出时清理全部实例
func StopAllProxies() {
	DefaultPool.StopAll()
}
