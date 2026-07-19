package transport

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/json/badoption"
)

type boxCloser interface {
	Close() error
}

type nodeBox struct {
	box      boxCloser
	outbound adapterOutbound
	closed   atomic.Bool
}

type adapterOutbound interface {
	DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error)
	ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error)
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

// entryBoxManager manages the single resident first-hop (entry proxy) sing-box instance.
type entryBoxManager struct {
	mu        sync.Mutex
	entryURI  string
	box       *box.Box
	socksAddr string
	stopped   bool
}

func (m *entryBoxManager) Addr() string {
	addr, _ := m.addrAndURI()
	return addr
}

func (m *entryBoxManager) currentURI() string {
	_, uri := m.addrAndURI()
	return uri
}

// addrAndURI 在单次加锁下同时返回 socksAddr 和 entryURI。
func (m *entryBoxManager) addrAndURI() (string, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped || m.box == nil {
		return "", m.entryURI
	}
	return m.socksAddr, m.entryURI
}

// sync 同步全局前置代理 URI。
//   - uri==""：关闭旧实例并置空。
//   - uri==currentURI 且 box 存活：复用。
//   - 其他：ValidateEntryProxy 隔离构建候选，成功后 AdoptEntryProxy 瞬间替换。
//     验证失败时保留旧实例并返回错误（不静默降级为直连）。
func (m *entryBoxManager) sync(uri string) error {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return fmt.Errorf("entry proxy manager已停止")
	}

	normalized := normalizeURI(uri)
	if normalized == m.entryURI && m.box != nil {
		m.mu.Unlock()
		return nil
	}

	if uri == "" {
		if m.box != nil {
			m.box.Close()
			m.box = nil
			m.socksAddr = ""
			m.entryURI = ""
		}
		m.mu.Unlock()
		return nil
	}

	m.mu.Unlock()

	newBox, newAddr, err := startEntryBox(uri)
	if err != nil {
		return fmt.Errorf("全局前置代理启动失败: %w", err)
	}

	return m.AdoptEntryProxy(uri, newBox, newAddr)
}

// ValidateEntryProxy 在不触碰常驻实例的前提下，隔离构建并启动一个候选 entry box。
// 调用方拥有返回的 box 所有权，未采纳前必须自行关闭。
func (m *entryBoxManager) ValidateEntryProxy(uri string) (*box.Box, string, error) {
	return startEntryBox(uri)
}

// AdoptEntryProxy 安装已验证的候选实例为活动常驻实例。
// 锁内关闭旧常驻实例、安装新实例并记录规范化 URI。
// 如果管理器已停止则关闭候选并返回错误。
func (m *entryBoxManager) AdoptEntryProxy(uri string, newBox *box.Box, socksAddr string) error {
	normalized := normalizeURI(uri)

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.stopped {
		_ = newBox.Close()
		return fmt.Errorf("entry proxy manager已停止")
	}

	if normalized == m.entryURI && m.box != nil {
		_ = newBox.Close()
		return nil
	}

	if m.box != nil {
		m.box.Close()
	}
	m.box = newBox
	m.socksAddr = socksAddr
	m.entryURI = normalized
	log.Printf("[Transport] 全局前置代理已就绪, 回环 SOCKS 地址: %s", socksAddr)
	return nil
}

func (m *entryBoxManager) stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = true
	if m.box != nil {
		m.box.Close()
		m.box = nil
	}
	m.socksAddr = ""
	m.entryURI = ""
}

const (
	entryMaxEphemeralAttempts = 10
	secondHopDialTimeout      = 15 * time.Second
)

func startEntryBox(uri string) (*box.Box, string, error) {
	entryOb, err := buildOutbound(uri)
	if err != nil {
		return nil, "", fmt.Errorf("build entry outbound: %w", err)
	}
	entryOb.Tag = "entry-out"

	loopback := badoption.Addr(netip.MustParseAddr("127.0.0.1"))
	baseOpts := box.Options{
		Context: include.Context(context.Background()),
		Options: option.Options{
			Log:       &option.LogOptions{Disabled: true},
			Inbounds:  nil,
			Outbounds: []option.Outbound{entryOb},
			Route:     &option.RouteOptions{Final: "entry-out"},
		},
	}

	for attempt := 0; attempt < entryMaxEphemeralAttempts; attempt++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, "", fmt.Errorf("entry proxy: 临时端口监听失败: %w", err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		_ = ln.Close()

		inboundOpts := baseOpts
		inboundOpts.Options.Inbounds = []option.Inbound{
			{
				Type: C.TypeSOCKS,
				Tag:  "entry-socks",
				Options: &option.SocksInboundOptions{
					ListenOptions: option.ListenOptions{
						Listen:     &loopback,
						ListenPort: uint16(port),
					},
				},
			},
		}

		b, err := box.New(inboundOpts)
		if err != nil {
			return nil, "", fmt.Errorf("entry box.New: %w", err)
		}
		if err := b.Start(); err != nil {
			b.Close()
			errStr := err.Error()
			if strings.Contains(errStr, "in use") ||
				strings.Contains(errStr, "address already in use") ||
				strings.Contains(errStr, "bind") {
				continue
			}
			return nil, "", fmt.Errorf("entry box.Start: %w", err)
		}
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		return b, addr, nil
	}
	return nil, "", fmt.Errorf("entry proxy: 尝试了%d个临时端口后仍找不到可用端口", entryMaxEphemeralAttempts)
}

type singDialer struct {
	cfg        config.ConfigProvider
	entry      *entryBoxManager
	boxBuilder func(uri string) (*nodeBox, error)
}

func NewSingDialer(cfg config.ConfigProvider) *singDialer {
	d := &singDialer{
		cfg:   cfg,
		entry: &entryBoxManager{},
	}
	d.boxBuilder = d.newSecondHopBox
	return d
}

func (d *singDialer) EntryProxySocksAddr() string {
	return d.entry.Addr()
}

func (d *singDialer) SyncEntryProxy(uri string) error {
	return d.entry.sync(uri)
}

func (d *singDialer) CreateDialer(uri string, reqID string) (func(ctx context.Context, network, addr string) (net.Conn, error), func(), error) {
	// 自引用守卫：第二跳 URI 与全局前置一致时，直接经回环 SOCKS 拨号，不自建第二跳 box。
	// 使用 addrAndURI 单次加锁读取两个字段，防止 AdoptEntryProxy 并发修改导致 socksAddr 与 URI 不一致。
	if socksAddr, currentURI := d.entry.addrAndURI(); socksAddr != "" && normalizeURI(uri) == currentURI {
		log.Printf("[Transport] 请求ID=%s 第二跳 URI 与全局前置一致, 直接经回环", reqID)
		return socks5DialFunc(socksAddr), func() {}, nil
	}

	log.Printf("[Transport] 请求ID=%s 触发第二跳代理初始化: %s", reqID, nodes.GetNodeName(uri))

	nb, err := d.boxBuilder(uri)
	if err != nil {
		return nil, nil, fmt.Errorf("create second-hop box: %w", err)
	}
	return makeBoxDialFunc(nb), func() {
		if !nb.closed.Load() {
			nb.closed.Store(true)
			_ = nb.box.Close()
		}
	}, nil
}

func (d *singDialer) newSecondHopBox(uri string) (*nodeBox, error) {
	node, err := buildOutbound(uri)
	if err != nil {
		return nil, fmt.Errorf("build outbound: %w", err)
	}
	node.Tag = "default"

	var outbounds []option.Outbound

	if socksAddr := d.entry.Addr(); socksAddr != "" {
		host, portStr, _ := net.SplitHostPort(socksAddr)
		portNum, _ := strconv.Atoi(portStr)
		socksOut := option.Outbound{
			Type: C.TypeSOCKS,
			Tag:  "entry-socks-out",
			Options: &option.SOCKSOutboundOptions{
				ServerOptions: option.ServerOptions{
					Server:     host,
					ServerPort: uint16(portNum),
				},
			},
		}
		setOutboundDetour(&node, "entry-socks-out")
		outbounds = append(outbounds, socksOut)
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
		_ = newBox.Close()
		return nil, fmt.Errorf("box.Start: %w", err)
	}

	o, ok := newBox.Outbound().Outbound("default")
	if !ok {
		_ = newBox.Close()
		return nil, fmt.Errorf("outbound %q not found", "default")
	}

	return &nodeBox{
		box:      newBox,
		outbound: o,
	}, nil
}

func (d *singDialer) TestEntryProxy(uri string) (func(ctx context.Context, network, addr string) (net.Conn, error), func(), error) {
	entryOb, err := buildOutbound(uri)
	if err != nil {
		return nil, nil, fmt.Errorf("build candidate outbound: %w", err)
	}
	entryOb.Tag = "default"

	newBox, err := box.New(box.Options{
		Context: include.Context(context.Background()),
		Options: option.Options{
			Log:       &option.LogOptions{Disabled: true},
			Outbounds: []option.Outbound{entryOb},
		},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("candidate box.New: %w", err)
	}
	if err := newBox.Start(); err != nil {
		_ = newBox.Close()
		return nil, nil, fmt.Errorf("candidate box.Start: %w", err)
	}

	o, ok := newBox.Outbound().Outbound("default")
	if !ok {
		_ = newBox.Close()
		return nil, nil, fmt.Errorf("candidate outbound not found")
	}

	nb := &nodeBox{box: newBox, outbound: o}
	return makeBoxDialFunc(nb), func() {
		if !nb.closed.Load() {
			nb.closed.Store(true)
			_ = nb.box.Close()
		}
	}, nil
}

func (d *singDialer) ValidateEntryProxy(uri string) (io.Closer, string, error) {
	return d.entry.ValidateEntryProxy(uri)
}

func (d *singDialer) AdoptEntryProxy(uri string, candidate io.Closer, socksAddr string) error {
	b, ok := candidate.(*box.Box)
	if !ok {
		_ = candidate.Close()
		return fmt.Errorf("AdoptEntryProxy: invalid candidate type")
	}
	return d.entry.AdoptEntryProxy(uri, b, socksAddr)
}

func (d *singDialer) StopAll() {
	d.entry.stop()
}

func makeBoxDialFunc(nb *nodeBox) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if nb.closed.Load() {
			return nil, fmt.Errorf("node box closed")
		}
		dialCtx, cancel := context.WithTimeout(ctx, secondHopDialTimeout)
		defer cancel()
		destination := M.ParseSocksaddr(addr)

		type dialResult struct {
			conn net.Conn
			err  error
		}
		ch := make(chan dialResult, 1)
		go func() {
			conn, err := nb.outbound.DialContext(dialCtx, network, destination)
			ch <- dialResult{conn, err}
		}()

		select {
		case r := <-ch:
			return r.conn, r.err
		case <-dialCtx.Done():
			nb.box.Close()
			return nil, fmt.Errorf("dial timeout after %v", secondHopDialTimeout)
		}
	}
}

func normalizeURI(uri string) string {
	uri = strings.TrimSpace(uri)
	uri = strings.TrimRight(uri, "/")
	return uri
}

// socks5DialFunc 返回一个直连指定 SOCKS5 地址的拨号函数。
// 用于自引用场景：第二跳 URI 与全局前置一致时，直接经回环 SOCKS 拨号。
func socks5DialFunc(socksAddr string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialCtx, cancel := context.WithTimeout(ctx, secondHopDialTimeout)
		defer cancel()
		conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", socksAddr)
		if err != nil {
			return nil, fmt.Errorf("socks5 dial to %s: %w", socksAddr, err)
		}
		if deadline, ok := dialCtx.Deadline(); ok {
			conn.SetDeadline(deadline)
		}

		// SOCKS5 方法协商：无认证
		if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
			conn.Close()
			return nil, fmt.Errorf("socks5 handshake write: %w", err)
		}
		buf := make([]byte, 2)
		if _, err := io.ReadFull(conn, buf); err != nil {
			conn.Close()
			return nil, fmt.Errorf("socks5 handshake read: %w", err)
		}
		if buf[0] != 5 || buf[1] != 0 {
			conn.Close()
			return nil, fmt.Errorf("socks5 handshake rejected: ver=%d method=%d", buf[0], buf[1])
		}

		// CONNECT 请求
		host, portStr, err := net.SplitHostPort(addr)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("socks5 target parse: %w", err)
		}
		portNum, _ := strconv.Atoi(portStr)

		var atyp byte
		var addrBytes []byte
		if ip := net.ParseIP(host); ip != nil {
			if ip4 := ip.To4(); ip4 != nil {
				atyp = 1
				addrBytes = ip4
			} else {
				atyp = 4
				addrBytes = ip.To16()
			}
		} else {
			atyp = 3
			addrBytes = []byte(host)
		}

		req := []byte{5, 1, 0, atyp}
		if atyp == 3 {
			req = append(req, byte(len(addrBytes)))
		}
		req = append(req, addrBytes...)
		req = append(req, byte(portNum>>8), byte(portNum))

		if _, err := conn.Write(req); err != nil {
			conn.Close()
			return nil, fmt.Errorf("socks5 connect write: %w", err)
		}

		resp := make([]byte, 4)
		if _, err := io.ReadFull(conn, resp); err != nil {
			conn.Close()
			return nil, fmt.Errorf("socks5 connect read: %w", err)
		}
		if resp[1] != 0 {
			conn.Close()
			return nil, fmt.Errorf("socks5 connect failed: code=%d", resp[1])
		}

		// 读剩余的 BND.ADDR（最多 256 字节）以完成握手
		restLen := 0
		switch resp[3] {
		case 1:
			restLen = 4
		case 3:
			domainLenBuf := make([]byte, 1)
			if _, err := io.ReadFull(conn, domainLenBuf); err != nil {
				conn.Close()
				return nil, fmt.Errorf("socks5 response domain length read: %w", err)
			}
			n := int(domainLenBuf[0])
			restLen = n
			if restLen > 256 {
				conn.Close()
				return nil, fmt.Errorf("socks5 response domain too long")
			}
		case 4:
			restLen = 16
		}
		if restLen > 0 {
			rest := make([]byte, restLen+2)
			if _, err := io.ReadFull(conn, rest); err != nil {
				conn.Close()
				return nil, fmt.Errorf("socks5 response read: %w", err)
			}
		}

		return conn, nil
	}
}
