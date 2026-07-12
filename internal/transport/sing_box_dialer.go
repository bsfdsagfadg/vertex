package transport

import (
	"context"
	"fmt"
	"log"
	"net"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
)

type nodeBox struct {
	box      *box.Box
	outbound adapterOutbound
	lastUsed time.Time
	stopOnce sync.Once
}

type singDialer struct {
	mu     sync.RWMutex
	nodes  map[string]*nodeBox
	cfg    ProxyDialerConfig
	ticker *time.Ticker
	stopCh chan struct{}
}

func NewSingDialer(cfg ProxyDialerConfig) *singDialer {
	d := &singDialer{
		nodes:  make(map[string]*nodeBox),
		cfg:    cfg,
		stopCh: make(chan struct{}),
	}
	if cfg.GCInterval > 0 {
		d.ticker = time.NewTicker(cfg.GCInterval)
		go d.gcLoop()
	}
	return d
}

func (d *singDialer) gcLoop() {
	for {
		select {
		case <-d.stopCh:
			return
		case <-d.ticker.C:
			d.safeCleanup()
		}
	}
}

func (d *singDialer) safeCleanup() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[singDialer] gcLoop panic: %v\n%s", r, debug.Stack())
		}
	}()
	d.cleanupIdleURIs()
}

func (d *singDialer) CreateDialer(uri string, reqID string) (func(ctx context.Context, network, addr string) (net.Conn, error), error) {
	d.mu.RLock()
	if nb, ok := d.nodes[uri]; ok {
		nb.lastUsed = time.Now()
		d.mu.RUnlock()
		return makeBoxDialFunc(nb.outbound), nil
	}
	d.mu.RUnlock()

	log.Printf("[Transport] 请求ID=%s 触发代理初始化: %s", reqID, nodes.GetNodeName(uri))

	d.mu.Lock()
	if nb, ok := d.nodes[uri]; ok {
		nb.lastUsed = time.Now()
		d.mu.Unlock()
		return makeBoxDialFunc(nb.outbound), nil
	}

	nb, err := d.newBoxForURI(uri)
	if err != nil {
		d.mu.Unlock()
		return nil, fmt.Errorf("create node box: %w", err)
	}

	d.nodes[uri] = nb
	d.mu.Unlock()
	return makeBoxDialFunc(nb.outbound), nil
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
	if entry := d.cfg.EntryProxy; entry != "" &&
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
		lastUsed: time.Now(),
	}, nil
}

func (d *singDialer) RemoveDialer(uri string) {
	d.mu.Lock()
	nb, ok := d.nodes[uri]
	if !ok {
		d.mu.Unlock()
		return
	}
	delete(d.nodes, uri)
	d.mu.Unlock()

	nb.stopOnce.Do(func() {
		_ = nb.box.Close()
		log.Printf("[Transport] 代理节点已移除: %s", nodes.GetNodeName(uri))
	})
}

func (d *singDialer) cleanupIdleURIs() {
	if d.cfg.MaxIdle <= 0 {
		return
	}
	d.mu.Lock()
	now := time.Now()
	var idle []*nodeBox
	for uri, nb := range d.nodes {
		if now.Sub(nb.lastUsed) > d.cfg.MaxIdle {
			idle = append(idle, nb)
			delete(d.nodes, uri)
			log.Printf("[Transport] 节点空闲超时移除: %s", nodes.GetNodeName(uri))
		}
	}
	d.mu.Unlock()

	for _, nb := range idle {
		nb.stopOnce.Do(func() {
			_ = nb.box.Close()
		})
	}
}

func (d *singDialer) StopAll() {
	if d.ticker != nil {
		d.ticker.Stop()
	}
	close(d.stopCh)

	d.mu.Lock()
	boxes := make([]*nodeBox, 0, len(d.nodes))
	for _, nb := range d.nodes {
		boxes = append(boxes, nb)
	}
	d.nodes = make(map[string]*nodeBox)
	d.mu.Unlock()

	for _, nb := range boxes {
		nb.stopOnce.Do(func() {
			_ = nb.box.Close()
		})
	}
}

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
