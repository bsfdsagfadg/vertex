package transport

import (
	"context"
	"net"
)

type ProxyDialer interface {
	CreateDialer(uri string, reqID string) (func(ctx context.Context, network, addr string) (net.Conn, error), func(), error)
	RemoveDialer(uri string)
	StopAll()
}

type ProxyDialerConfig struct {
	EntryProxy string
}
