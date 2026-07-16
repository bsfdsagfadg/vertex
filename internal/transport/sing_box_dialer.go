package transport

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
)

type boxKey struct {
	entry string
	node  string
}

type boxCloser interface {
	Close() error
}

type nodeBox struct {
	box      boxCloser
	outbound adapterOutbound
	closed   atomic.Bool
}

type singDialer struct {
	cfg        config.ConfigProvider
	boxCache   map[boxKey]*nodeBox
	cacheMu    sync.Mutex
	lastEntry  string
	boxBuilder func(uri string) (*nodeBox, error)
}

func NewSingDialer(cfg config.ConfigProvider) *singDialer {
	d := &singDialer{
		cfg:      cfg,
		boxCache: make(map[boxKey]*nodeBox),
	}
	d.boxBuilder = d.newBoxForURI
	return d
}

func (d *singDialer) CreateDialer(uri string, reqID string) (func(ctx context.Context, network, addr string) (net.Conn, error), func(), error) {
	log.Printf("[Transport] 请求ID=%s 触发代理初始化: %s", reqID, nodes.GetNodeName(uri))

	nb, err := d.box(uri)
	if err != nil {
		return nil, nil, fmt.Errorf("create node box: %w", err)
	}
	return makeBoxDialFunc(nb), func() {}, nil
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
	if entry := d.cfg.ProxyURL(); entry != "" {
		if normalizeURI(entry) == normalizeURI(uri) {
			log.Printf("[Transport] 前置代理 URI 与节点 URI 相同，跳过前置代理 entry-proxy 防止自引用")
		} else {
			entryOb, err := buildOutbound(entry)
			if err != nil {
				return nil, fmt.Errorf("全局前置代理构建失败: %w", err)
			}
			entryOb.Tag = "entry-proxy"
			outbounds = append(outbounds, entryOb)
			entryTag = "entry-proxy"
		}
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

func normalizeURI(uri string) string {
	uri = strings.TrimSpace(uri)
	uri = strings.TrimRight(uri, "/")
	return uri
}

func (d *singDialer) box(uri string) (*nodeBox, error) {
	for {
		entry := d.cfg.ProxyURL()
		key := boxKey{entry: entry, node: uri}

		d.cacheMu.Lock()
		if entry != d.lastEntry {
			closers := d.flushCacheUnsafe()
			d.lastEntry = entry
			d.cacheMu.Unlock()
			for _, c := range closers {
				c.Close()
			}
			continue
		}
		if nb, ok := d.boxCache[key]; ok {
			d.cacheMu.Unlock()
			return nb, nil
		}
		d.cacheMu.Unlock()

		nb, err := d.boxBuilder(uri)
		if err != nil {
			return nil, err
		}

		d.cacheMu.Lock()
		currentEntry := d.cfg.ProxyURL()
		if currentEntry != d.lastEntry {
			closers := d.flushCacheUnsafe()
			d.lastEntry = currentEntry
			closers = append(closers, nb.box)
			d.cacheMu.Unlock()
			for _, c := range closers {
				c.Close()
			}
			continue
		}

		currentKey := boxKey{entry: currentEntry, node: uri}
		if existing, ok := d.boxCache[currentKey]; ok {
			d.cacheMu.Unlock()
			nb.box.Close()
			return existing, nil
		}
		d.boxCache[currentKey] = nb
		d.cacheMu.Unlock()
		return nb, nil
	}
}

func (d *singDialer) flushCacheUnsafe() []boxCloser {
	var closers []boxCloser
	for key, nb := range d.boxCache {
		if !nb.closed.Load() {
			closers = append(closers, nb.box)
			nb.closed.Store(true)
		}
		delete(d.boxCache, key)
	}
	return closers
}

func (d *singDialer) RemoveDialer(uri string) {
	d.cacheMu.Lock()
	var closers []boxCloser
	for key, nb := range d.boxCache {
		if key.node == uri {
			if !nb.closed.Load() {
				closers = append(closers, nb.box)
				nb.closed.Store(true)
			}
			delete(d.boxCache, key)
		}
	}
	d.cacheMu.Unlock()
	for _, c := range closers {
		c.Close()
	}
}

func (d *singDialer) StopAll() {
	d.cacheMu.Lock()
	closers := d.flushCacheUnsafe()
	d.cacheMu.Unlock()
	for _, c := range closers {
		c.Close()
	}
}

func makeBoxDialFunc(nb *nodeBox) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if nb.closed.Load() {
			return nil, fmt.Errorf("node box closed")
		}
		destination := M.ParseSocksaddr(addr)
		return nb.outbound.DialContext(ctx, network, destination)
	}
}

type adapterOutbound interface {
	DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error)
	ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error)
}
