package transport

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
)

type nodeBox struct {
	box      *box.Box
	outbound adapterOutbound
	stopOnce sync.Once
}

type singDialer struct {
	cfg config.ConfigProvider
}

func NewSingDialer(cfg config.ConfigProvider) *singDialer {
	return &singDialer{cfg: cfg}
}

func (d *singDialer) CreateDialer(uri string, reqID string) (func(ctx context.Context, network, addr string) (net.Conn, error), func(), error) {
	log.Printf("[Transport] 请求ID=%s 触发代理初始化: %s", reqID, nodes.GetNodeName(uri))

	nb, err := d.newBoxForURI(uri)
	if err != nil {
		return nil, nil, fmt.Errorf("create node box: %w", err)
	}
	return makeBoxDialFunc(nb.outbound), func() { nb.stopOnce.Do(func() { nb.box.Close() }) }, nil
}

type dialerOptionsSetter interface {
	TakeDialerOptions() option.DialerOptions
	ReplaceDialerOptions(option.DialerOptions)
}

func setOutboundDetour(o *option.Outbound, tag string) {
	s, ok := o.Options.(dialerOptionsSetter)
	if !ok {
		return
	}
	opts := s.TakeDialerOptions()
	opts.Detour = tag
	s.ReplaceDialerOptions(opts)
}

func (d *singDialer) newBoxForURI(uri string) (*nodeBox, error) {
	var outbounds []option.Outbound

	entryTag := ""
	if entry := d.cfg.ProxyURL(); entry != "" &&
		(strings.HasPrefix(entry, "socks5://") ||
			strings.HasPrefix(entry, "socks5h://") ||
			strings.HasPrefix(entry, "socks://") ||
			strings.HasPrefix(entry, "http://") ||
			strings.HasPrefix(entry, "https://")) {

		entryOb, err := buildOutbound(entry)
		if err != nil {
			return nil, fmt.Errorf("build entry proxy: %w", err)
		}
		entryOb.Tag = "entry-proxy"
		outbounds = append(outbounds, entryOb)
		entryTag = "entry-proxy"
	}

	node, err := buildOutbound(uri)
	if err != nil {
		return nil, fmt.Errorf("build outbound: %w", err)
	}
	node.Tag = "default"
	if entryTag != "" {
		setOutboundDetour(&node, entryTag)
	}
	outbounds = append(outbounds, node)

	newBox, err := box.New(box.Options{
		Context: include.Context(context.Background()),
		Options: option.Options{
			Log:       &option.LogOptions{Disabled: true},
			Outbounds: outbounds,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("box.New: %w", err)
	}
	if err := newBox.Start(); err != nil {
		newBox.Close()
		return nil, fmt.Errorf("box.Start: %w", err)
	}

	o, ok := newBox.Outbound().Outbound("default")
	if !ok {
		newBox.Close()
		return nil, fmt.Errorf("outbound %q not found", "default")
	}

	return &nodeBox{
		box:      newBox,
		outbound: o,
	}, nil
}

func (d *singDialer) RemoveDialer(uri string) {}

func (d *singDialer) StopAll() {}

func makeBoxDialFunc(outbound adapterOutbound) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		destination := M.ParseSocksaddr(addr)
		return outbound.DialContext(ctx, network, destination)
	}
}

type adapterOutbound interface {
	DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error)
	ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error)
}
