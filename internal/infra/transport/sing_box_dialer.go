package transport

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/infra/config"
	"github.com/bsfdsagfadg/vertex/internal/node/entrypool"
	"github.com/sagernet/sing-box"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
	M "github.com/sagernet/sing/common/metadata"
)

type boxCloser interface {
	Close() error
}

// NodeNamer 是拨号器对出口节点友好名查询的消费契约（生产实现 *exitpool.Manager）。
type NodeNamer interface {
	NodeName(rawURI string) string
}

// EntrySource 是拨号器对前置节点池的同步源消费契约（生产实现 *entrypool.EntryManager）。
// 类型位引用 []entrypool.Node 为 AGENTS.md 铁律 #10 记录的显式豁免（R1 唯一豁免）：
// 候选结构体跨域只读传递，无行为依赖。
type EntrySource interface {
	SelectableNodes() []entrypool.Node
}

// nopNodeNamer 是 Namer 未注入时的防御性空实现（日志归属退化为空串）。
type nopNodeNamer struct{}

func (nopNodeNamer) NodeName(string) string { return "" }

// DialerDeps 聚合拨号器的跨域成品依赖，由 main 装配链显式注入；
// Cache 为 nil 时内部自建空缓存（等价纯解析路径），Namer/Entries 为 nil 时按空实现降级。
type DialerDeps struct {
	Cache   *IRCache
	Namer   NodeNamer
	Entries EntrySource
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
		log.Printf("[Transport] 警告: 出站协议 %q (tag=%q) 未实现 dialerOptionsSetter，无法设置 detour 前置代理，将绕过前置代理直连", o.Type, o.Tag)
		return
	}
	opts := s.TakeDialerOptions()
	opts.Detour = tag
	s.ReplaceDialerOptions(opts)
}

// entryBoxInstance 是池中一个运行中的前置代理（entry proxy）sing-box 实例。
type entryBoxInstance struct {
	uri       string
	box       *box.Box
	socksAddr string
}

// entryBoxPoolManager 管理多个常驻前置代理 sing-box 实例。
//
// 每个实例监听 127.0.0.1 上的随机端口，提供 SOCKS5 回环通道；
// GetNextEntrySocksAddr 基于稳定的 order 顺序 + 原子计数实现 Round-Robin 轮询；
// 池为空时返回 ""，上层透明降级为直连（Direct）。
type entryBoxPoolManager struct {
	deps      DialerDeps
	mu        sync.RWMutex
	instances map[string]*entryBoxInstance // key=normalizeURI(raw_uri)
	order     []string                     // 稳定顺序的 socksAddr 列表（与 instances 同步重建）
	rrIndex   atomic.Uint64
	stopped   bool
}

func newEntryBoxPoolManager(deps DialerDeps) *entryBoxPoolManager {
	return &entryBoxPoolManager{deps: deps}
}

// rebuildOrderLocked 依据当前 instances 按 key 排序重建稳定的 socksAddr 顺序列表。
// 必须在持有 m.mu 锁时调用。
func (m *entryBoxPoolManager) rebuildOrderLocked() {
	keys := make([]string, 0, len(m.instances))
	for k := range m.instances {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	order := make([]string, 0, len(keys))
	for _, k := range keys {
		order = append(order, m.instances[k].socksAddr)
	}
	m.order = order
}

// SyncEntryPool 从 entrynodes 加载可选前置节点，增量同步实例池：
//   - 已删除/被禁用的节点：关闭并移除对应实例。
//   - 新增/重新启用的节点：增量启动新实例。
//
// 单个节点启动失败仅记录日志并跳过，不影响其余节点（透明降级）。
func (m *entryBoxPoolManager) SyncEntryPool() error {
	var selectable []entrypool.Node
	if m.deps.Entries != nil {
		selectable = m.deps.Entries.SelectableNodes()
	}
	desired := make(map[string]string, len(selectable)) // normalizeURI(raw) -> raw
	for _, n := range selectable {
		desired[normalizeURI(n.RawURI)] = n.RawURI
	}

	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return fmt.Errorf("entry proxy pool已停止")
	}
	if m.instances == nil {
		m.instances = make(map[string]*entryBoxInstance)
	}
	var toClose []*entryBoxInstance
	for key, inst := range m.instances {
		if _, ok := desired[key]; !ok {
			toClose = append(toClose, inst)
			delete(m.instances, key)
		}
	}
	m.rebuildOrderLocked()
	var toStart []string
	for key := range desired {
		if _, ok := m.instances[key]; !ok {
			toStart = append(toStart, key)
		}
	}
	m.mu.Unlock()

	// 关闭已失效实例（锁外执行，避免阻塞拨号）。
	for _, inst := range toClose {
		log.Printf("[Transport] 关闭已失效的前置代理回环实例: %s (%s)", RedactURI(inst.uri), inst.socksAddr)
		_ = inst.box.Close()
	}

	// 增量启动新实例。
	for _, key := range toStart {
		rawURI := desired[key]
		newBox, newAddr, err := m.startEntryBox(rawURI)
		if err != nil {
			log.Printf("[Transport] 前置代理启动失败，跳过: %s (%v)", RedactURI(rawURI), err)
			continue
		}
		m.mu.Lock()
		if m.stopped {
			m.mu.Unlock()
			_ = newBox.Close()
			break
		}
		if m.instances == nil {
			m.instances = make(map[string]*entryBoxInstance)
		}
		if _, exists := m.instances[key]; exists {
			m.mu.Unlock()
			_ = newBox.Close()
			continue
		}
		m.instances[key] = &entryBoxInstance{uri: rawURI, box: newBox, socksAddr: newAddr}
		m.rebuildOrderLocked()
		m.mu.Unlock()
		log.Printf("[Transport] 前置代理回环实例已就绪: %s (%s)", RedactURI(rawURI), newAddr)
	}
	return nil
}

// GetNextEntrySocksAddr 按 Round-Robin 返回一个运行中的前置代理 SOCKS5 回环地址。
// 池为空或已停止时返回 ""（上层据此透明降级直连）。
func (m *entryBoxPoolManager) GetNextEntrySocksAddr() string {
	m.mu.RLock()
	if m.stopped || len(m.order) == 0 {
		m.mu.RUnlock()
		return ""
	}
	addrs := append([]string(nil), m.order...)
	m.mu.RUnlock()

	idx := (m.rrIndex.Add(1) - 1) % uint64(len(addrs))
	return addrs[idx]
}

// socksAddrForURI 返回指定 URI 实例的回环地址；不存在时返回 ""。
// 用于自引用场景：第二跳 URI 恰为池内前置代理时直接经回环拨号。
func (m *entryBoxPoolManager) socksAddrForURI(uri string) string {
	key := normalizeURI(uri)
	m.mu.RLock()
	defer m.mu.RUnlock()
	if inst, ok := m.instances[key]; ok {
		return inst.socksAddr
	}
	return ""
}

func (m *entryBoxPoolManager) stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = true
	for _, inst := range m.instances {
		_ = inst.box.Close()
	}
	m.instances = make(map[string]*entryBoxInstance)
	m.order = nil
}

// RedactURI 仅保留 scheme://host:port，隐藏 URI 中的凭据信息。
// 供 transport 与 admin API 共用，保证日志脱敏逻辑一致。
func RedactURI(raw string) string {
	before, after, found := strings.Cut(raw, "://")
	if !found {
		return raw
	}
	atIdx := strings.Index(after, "@")
	if atIdx == -1 {
		return raw
	}
	return before + "://" + after[atIdx+1:]
}

const (
	entryMaxEphemeralAttempts = 10
	secondHopDialTimeout      = 15 * time.Second
)

func (m *entryBoxPoolManager) startEntryBox(uri string) (*box.Box, string, error) {
	n, err := parseForDial(m.deps.Cache, uri)
	if err != nil {
		return nil, "", fmt.Errorf("parse entry node: %w", err)
	}
	if !n.Supported {
		return nil, "", fmt.Errorf("unsupported: %s", n.UnsupportedReason)
	}
	entryOb, err := buildOutboundFromNode(n)
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
			DNS:       dnsOptionsForNode(n),
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
	deps       DialerDeps
	entry      *entryBoxPoolManager
	boxBuilder func(uri string) (*nodeBox, error)
}

func NewSingDialer(cfg config.ConfigProvider, deps DialerDeps) *singDialer {
	if deps.Cache == nil {
		deps.Cache = NewIRCache()
	}
	if deps.Namer == nil {
		deps.Namer = nopNodeNamer{}
	}
	d := &singDialer{
		cfg:   cfg,
		deps:  deps,
		entry: newEntryBoxPoolManager(deps),
	}
	d.boxBuilder = d.newSecondHopBox
	return d
}

// GetNextEntrySocksAddr 按请求轮询返回一个前置代理 SOCKS5 回环地址。
func (d *singDialer) GetNextEntrySocksAddr() string {
	return d.entry.GetNextEntrySocksAddr()
}

// SyncEntryPool 同步前置代理轮询池（从 entrynodes 加载可选节点）。
func (d *singDialer) SyncEntryPool() error {
	return d.entry.SyncEntryPool()
}

// nodeName 返回出口节点友好名；Namer 未注入（零值/测试直构）时退化为空串。
func (d *singDialer) nodeName(uri string) string {
	if d.deps.Namer == nil {
		return ""
	}
	return d.deps.Namer.NodeName(uri)
}

// parseForDial 拨号期解析入口：Cache 未注入时退化为无缓存纯解析
// （零值 singDialer/entryBoxPoolManager 直构路径的防御）。
func parseForDial(c *IRCache, uri string) (*ParsedNode, error) {
	if c == nil {
		return ParseURI(uri)
	}
	return c.GetOrParse(uri)
}

func (d *singDialer) CreateDialer(uri string, reqID string) (func(ctx context.Context, network, addr string) (net.Conn, error), func(), error) {
	// 自引用守卫：第二跳 URI 恰为池内前置代理时，直接经该实例回环 SOCKS 拨号，
	// 不自建第二跳 box。
	if socksAddr := d.entry.socksAddrForURI(uri); socksAddr != "" {
		log.Printf("[Transport] 请求ID=%s 第二跳 URI 命中前置代理池, 直接经回环", reqID)
		return socks5DialFunc(socksAddr), func() {}, nil
	}

	log.Printf("[Transport] 请求ID=%s 触发第二跳代理初始化: %s", reqID, d.nodeName(uri))

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
	n, err := parseForDial(d.deps.Cache, uri)
	if err != nil {
		return nil, fmt.Errorf("parse outbound node: %w", err)
	}
	if !n.Supported {
		return nil, fmt.Errorf("unsupported: %s", n.UnsupportedReason)
	}
	node, err := buildOutboundFromNode(n)
	if err != nil {
		return nil, fmt.Errorf("build outbound: %w", err)
	}
	node.Tag = "default"

	var outbounds []option.Outbound

	if socksAddr := d.entry.GetNextEntrySocksAddr(); socksAddr != "" {
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
			DNS:       dnsOptionsForNode(n),
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
	n, err := parseForDial(d.deps.Cache, uri)
	if err != nil {
		return nil, nil, fmt.Errorf("parse candidate node: %w", err)
	}
	if !n.Supported {
		return nil, nil, fmt.Errorf("unsupported: %s", n.UnsupportedReason)
	}
	entryOb, err := buildOutboundFromNode(n)
	if err != nil {
		return nil, nil, fmt.Errorf("build candidate outbound: %w", err)
	}
	entryOb.Tag = "default"

	newBox, err := box.New(box.Options{
		Context: include.Context(context.Background()),
		Options: option.Options{
			Log:       &option.LogOptions{Disabled: true},
			Outbounds: []option.Outbound{entryOb},
			DNS:       dnsOptionsForNode(n),
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
			nb.closed.Store(true)
			nb.box.Close()
			go func() {
				// 清理 goroutine 必须存活到拨号返回：dialCtx 由 ctx 派生，父 ctx 取消会
				// 同步取消 dialCtx，DialContext 若尊重 context 会在取消后迅速返回，
				// 此处即可收到 ch 并关闭 conn。若此处监听 ctx.Done() 提前退出，
				// 后台拨号稍后返回的 conn 将永远无人关闭（socket 泄漏）。
				timer := time.NewTimer(10 * time.Second) // 终极兜底，防 transport 不响应 context
				defer timer.Stop()
				select {
				case r := <-ch:
					if r.conn != nil {
						_ = r.conn.Close()
					}
				case <-timer.C:
				}
			}()
			return nil, fmt.Errorf("dial timeout after %v", secondHopDialTimeout)
		}
	}
}

func normalizeURI(uri string) string {
	uri = strings.TrimSpace(uri)
	uri = strings.TrimRight(uri, "/")
	return uri
}
