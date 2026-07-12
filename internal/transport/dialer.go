package transport

import (
	"context"
	"net"
	"time"
)

type ProxyDialer interface {
	CreateDialer(uri string, reqID string) (func(ctx context.Context, network, addr string) (net.Conn, error), error)
	RemoveDialer(uri string)
	StopAll()
}

type ProxyDialerConfig struct {
	GCInterval time.Duration
	MaxIdle    time.Duration
}
