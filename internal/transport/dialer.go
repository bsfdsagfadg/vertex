package transport

import (
	"context"
	"net"
)

type ProxyDialer interface {
	CreateDialer(uri string, reqID string) (func(ctx context.Context, network, addr string) (net.Conn, error), func(), error)
	StopAll()
	// GetNextEntrySocksAddr 按请求轮询返回前置代理池中一个 SOCKS5 回环地址；池为空返回 ""。
	GetNextEntrySocksAddr() string
	// SyncEntryPool 从 entrynodes 加载可选前置节点并增量同步轮询池。
	SyncEntryPool() error
	TestEntryProxy(uri string) (func(ctx context.Context, network, addr string) (net.Conn, error), func(), error)
}
