package transport

import (
	"context"
	"io"
	"net"
)

type ProxyDialer interface {
	CreateDialer(uri string, reqID string) (func(ctx context.Context, network, addr string) (net.Conn, error), func(), error)
	StopAll()
	EntryProxySocksAddr() string
	SyncEntryProxy(uri string) error
	TestEntryProxy(uri string) (func(ctx context.Context, network, addr string) (net.Conn, error), func(), error)
	ValidateEntryProxy(uri string) (io.Closer, string, error)
	AdoptEntryProxy(uri string, candidate io.Closer, socksAddr string) error
}

type ProxyDialerConfig struct {
	EntryProxy string
}
