package transport

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
)

type fakeCloser struct {
	closeCount atomic.Int64
}

func (f *fakeCloser) Close() error {
	f.closeCount.Add(1)
	return nil
}

type fakeOutbound struct{}

func (fakeOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return nil, &fakeTimeoutError{"dial not available in test"}
}

func (fakeOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, &fakeTimeoutError{"listen not available in test"}
}

type fakeTimeoutError struct{ msg string }

func (e *fakeTimeoutError) Error() string   { return e.msg }
func (e *fakeTimeoutError) Timeout() bool   { return true }
func (e *fakeTimeoutError) Temporary() bool { return true }

type fakeCfg struct{}

func newFakeCfg() *fakeCfg {
	return &fakeCfg{}
}

func (c *fakeCfg) DefaultImageSize() string                    { return "1K" }
func (c *fakeCfg) DefaultThinkingLevel() string                { return "自动" }
func (c *fakeCfg) PortAPI() int                                { panic("unexpected") }
func (c *fakeCfg) MaxRetries() int                             { panic("unexpected") }
func (c *fakeCfg) AdminPassword() string                       { panic("unexpected") }
func (c *fakeCfg) DebugPprof() bool                            { panic("unexpected") }
func (c *fakeCfg) DebugMode() bool                             { panic("unexpected") }
func (c *fakeCfg) TrailingModelFixEnabled() bool               { panic("unexpected") }
func (c *fakeCfg) TrailingFixModels() []string                 { panic("unexpected") }
func (c *fakeCfg) DropMaxTokens() bool                         { panic("unexpected") }
func (c *fakeCfg) AggregateStream() bool                       { panic("unexpected") }
func (c *fakeCfg) MaxN() int                                   { panic("unexpected") }
func (c *fakeCfg) MaxRequestMB() int                           { panic("unexpected") }
func (c *fakeCfg) RequestTimeoutSeconds() int                  { panic("unexpected") }
func (c *fakeCfg) MaxSpillMB() int                             { panic("unexpected") }
func (c *fakeCfg) VertexAPIKey() string                        { panic("unexpected") }
func (c *fakeCfg) CountTokensQuerySignature() string           { panic("unexpected") }
func (c *fakeCfg) SafetySettings() map[string]string           { panic("unexpected") }
func (c *fakeCfg) ParallelPoolEnabled() bool                   { panic("unexpected") }
func (c *fakeCfg) ParallelPoolRetryEnabled() bool              { panic("unexpected") }
func (c *fakeCfg) ParallelPoolSize() int                       { panic("unexpected") }
func (c *fakeCfg) ParallelPoolDelayDynamic() bool              { panic("unexpected") }
func (c *fakeCfg) ActiveNodeURI() string                       { panic("unexpected") }
func (c *fakeCfg) BackgroundImage() string                     { panic("unexpected") }
func (c *fakeCfg) FontSize() string                            { panic("unexpected") }
func (c *fakeCfg) FontColorType() string                       { panic("unexpected") }
func (c *fakeCfg) FontColor() string                           { panic("unexpected") }
func (c *fakeCfg) CustomBgPresets() []string                   { panic("unexpected") }
func (c *fakeCfg) AutoRefreshLogs() bool                       { panic("unexpected") }
func (c *fakeCfg) TelemetryEnabled() *bool                     { panic("unexpected") }
func (c *fakeCfg) BaseModels() []string                        { panic("unexpected") }
func (c *fakeCfg) AliasMap() map[string]string                 { panic("unexpected") }
func (c *fakeCfg) ModelsWithFakeVariants() []string            { panic("unexpected") }
func (c *fakeCfg) FakePrefixes() []string                      { panic("unexpected") }
func (c *fakeCfg) ResolveModelName(string) string              { panic("unexpected") }
func (c *fakeCfg) ConfigDir() string                           { panic("unexpected") }
func (c *fakeCfg) ConfigPath() string                          { panic("unexpected") }
func (c *fakeCfg) DefaultResponseModalities() string           { return "图文" }
func (c *fakeCfg) StreamIdleTimeoutSeconds() int               { return 30 }
func (c *fakeCfg) RecaptchaTryEntryOrDirect() bool             { return true }

type fakeBuilder struct {
	count    atomic.Int64
	preBuild func()
}

func (b *fakeBuilder) build(uri string) (*nodeBox, error) {
	b.count.Add(1)
	if b.preBuild != nil {
		b.preBuild()
	}
	return &nodeBox{
		box:      &fakeCloser{},
		outbound: fakeOutbound{},
	}, nil
}

func TestCreateDialer_ReturnsUniqueBoxes(t *testing.T) {
	cfg := newFakeCfg()
	builder := &fakeBuilder{}
	d := &singDialer{
		cfg:        cfg,
		entry:      newEntryBoxPoolManager(),
		boxBuilder: builder.build,
	}

	_, cleanup1, err := d.CreateDialer("socks5://node1:1080", "test-1")
	if err != nil {
		t.Fatalf("CreateDialer: %v", err)
	}
	_, cleanup2, err := d.CreateDialer("socks5://node2:1080", "test-2")
	if err != nil {
		t.Fatalf("CreateDialer second: %v", err)
	}

	if builder.count.Load() != 2 {
		t.Fatalf("builder called %d times, want 2 (each CreateDialer gets its own box)", builder.count.Load())
	}

	cleanup1()
	cleanup2()
}

func TestCreateDialer_CleanupClosesBox(t *testing.T) {
	fc := &fakeCloser{}
	d := &singDialer{
		cfg:   newFakeCfg(),
		entry: newEntryBoxPoolManager(),
		boxBuilder: func(uri string) (*nodeBox, error) {
			return &nodeBox{box: fc, outbound: fakeOutbound{}}, nil
		},
	}

	_, cleanup, err := d.CreateDialer("socks5://node:1080", "test")
	if err != nil {
		t.Fatalf("CreateDialer: %v", err)
	}

	cleanup()

	if fc.closeCount.Load() == 0 {
		t.Fatal("cleanup should close the node box")
	}
}

func TestCreateDialer_NoCache(t *testing.T) {
	cfg := newFakeCfg()
	builder := &fakeBuilder{}
	d := &singDialer{
		cfg:        cfg,
		entry:      newEntryBoxPoolManager(),
		boxBuilder: builder.build,
	}

	_, cleanup1, _ := d.CreateDialer("socks5://node:1080", "test-1")
	_, cleanup2, _ := d.CreateDialer("socks5://node:1080", "test-2")

	if builder.count.Load() != 2 {
		t.Fatalf("builder called %d times, want 2 (no caching)", builder.count.Load())
	}

	cleanup1()
	cleanup2()
}

func TestCreateDialer_CleanupIdempotent(t *testing.T) {
	fc := &fakeCloser{}
	d := &singDialer{
		cfg:   newFakeCfg(),
		entry: newEntryBoxPoolManager(),
		boxBuilder: func(uri string) (*nodeBox, error) {
			return &nodeBox{box: fc, outbound: fakeOutbound{}}, nil
		},
	}

	_, cleanup, err := d.CreateDialer("socks5://node:1080", "test")
	if err != nil {
		t.Fatalf("CreateDialer: %v", err)
	}

	cleanup()
	cleanup()
}

func TestCreateDialer_BuilderError(t *testing.T) {
	cfg := newFakeCfg()
	d := &singDialer{
		cfg:   cfg,
		entry: newEntryBoxPoolManager(),
		boxBuilder: func(uri string) (*nodeBox, error) {
			return nil, errors.New("build failed")
		},
	}

	_, _, err := d.CreateDialer("socks5://node:1080", "test")
	if err == nil {
		t.Fatal("expected error from builder, got nil")
	}
	if !strings.Contains(err.Error(), "build failed") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestEntryProxyAddr_EmptyByDefault(t *testing.T) {
	m := newEntryBoxPoolManager()
	if addr := m.GetNextEntrySocksAddr(); addr != "" {
		t.Fatalf("expected empty addr before sync, got %q", addr)
	}
}

func TestEntryProxySync_EmptyIsOK(t *testing.T) {
	m := newEntryBoxPoolManager()
	if err := m.SyncEntryPool(); err != nil {
		t.Fatalf("sync empty should succeed: %v", err)
	}
}

func TestEntryProxySync_StoppedReturnsError(t *testing.T) {
	m := newEntryBoxPoolManager()
	m.stop()
	if err := m.SyncEntryPool(); err == nil {
		t.Fatal("sync after stop should return error")
	}
	if addr := m.GetNextEntrySocksAddr(); addr != "" {
		t.Fatalf("GetNextEntrySocksAddr after stop should be empty, got %q", addr)
	}
}

func TestStopAll_ClearsEntryProxy(t *testing.T) {
	box_, err := box.New(box.Options{
		Context: include.Context(context.Background()),
		Options: option.Options{
			Log: &option.LogOptions{Disabled: true},
		},
	})
	if err != nil {
		t.Fatalf("create box: %v", err)
	}
	d := &singDialer{
		cfg:   newFakeCfg(),
		entry: newEntryBoxPoolManager(),
	}
	if err := d.entry.adoptForTest("socks5://entry:1080", box_, "127.0.0.1:11080"); err != nil {
		t.Fatalf("adoptForTest: %v", err)
	}
	if addr := d.entry.GetNextEntrySocksAddr(); addr == "" {
		t.Fatal("expected non-empty addr before StopAll")
	}

	d.StopAll()

	if addr := d.entry.GetNextEntrySocksAddr(); addr != "" {
		t.Fatal("socksAddr should be cleared after StopAll")
	}
}

func TestConcurrentCreateDialer(t *testing.T) {
	cfg := newFakeCfg()
	d := &singDialer{
		cfg:        cfg,
		entry:      newEntryBoxPoolManager(),
		boxBuilder: func(uri string) (*nodeBox, error) {
			return &nodeBox{box: &fakeCloser{}, outbound: fakeOutbound{}}, nil
		},
	}

	const goroutines = 20
	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, cleanup, err := d.CreateDialer("socks5://node:1080", "concurrent")
			if err != nil {
				t.Errorf("CreateDialer: %v", err)
				return
			}
			cleanup()
		}()
	}
	wg.Wait()
}

func TestTestEntryProxy_Basic(t *testing.T) {
	cfg := newFakeCfg()
	d := &singDialer{
		cfg:        cfg,
		entry:      newEntryBoxPoolManager(),
		boxBuilder: func(uri string) (*nodeBox, error) {
			return &nodeBox{box: &fakeCloser{}, outbound: fakeOutbound{}}, nil
		},
	}

	_, cleanup, err := d.TestEntryProxy("socks5://candidate:1080")
	if err != nil {
		t.Fatalf("TestEntryProxy: %v", err)
	}
	cleanup()
}

func TestCreateSession_NilDialer_EmptySecondHop_Direct(t *testing.T) {
	nc := NewNetworkClient(nil)
	sess, err := nc.CreateSession(1, "", "t")
	if err != nil {
		t.Fatalf("CreateSession with nil dialer should succeed: %v", err)
	}
	if sess == nil {
		t.Fatal("CreateSession returned nil session")
	}
	sess.Close()
}

func TestCreateDialer_SecondHopEqualsEntry_UsesLoopback(t *testing.T) {
	builderCount := atomic.Int64{}
	entryURI := "socks5://entry:1080"

	// Create a minimal entry box so that the pool has a non-empty address.
	bareBox, err := box.New(box.Options{
		Context: include.Context(context.Background()),
		Options: option.Options{
			Log: &option.LogOptions{Disabled: true},
		},
	})
	if err != nil {
		t.Fatalf("create bare box: %v", err)
	}
	defer bareBox.Close()

	d := &singDialer{
		cfg:   newFakeCfg(),
		entry: newEntryBoxPoolManager(),
		boxBuilder: func(uri string) (*nodeBox, error) {
			builderCount.Add(1)
			return &nodeBox{box: &fakeCloser{}, outbound: fakeOutbound{}}, nil
		},
	}
	if err := d.entry.adoptForTest(entryURI, bareBox, "127.0.0.1:9999"); err != nil {
		t.Fatalf("adoptForTest: %v", err)
	}

	dialCtx, cleanup, err := d.CreateDialer(entryURI, "test")
	if err != nil {
		t.Fatalf("CreateDialer: %v", err)
	}
	if builderCount.Load() != 0 {
		t.Fatalf("builder called %d times, want 0 (self-reference should skip builder)", builderCount.Load())
	}
	if dialCtx == nil {
		t.Fatal("dialCtx should not be nil")
	}

	cleanup() // must not panic
	cleanup() // must be idempotent
}

func TestEntryProxyPool_GetNextRoundRobin(t *testing.T) {
	m := newEntryBoxPoolManager()
	for i := range 3 {
		b, err := box.New(box.Options{
			Context: include.Context(context.Background()),
			Options: option.Options{
				Log: &option.LogOptions{Disabled: true},
			},
		})
		if err != nil {
			t.Fatalf("create box %d: %v", i, err)
		}
		if err := m.adoptForTest("socks5://box"+strconv.Itoa(i)+":1080", b, "127.0.0.1:1000"+strconv.Itoa(i)); err != nil {
			t.Fatalf("adoptForTest: %v", err)
		}
	}

	seen := map[string]bool{}
	for range 6 {
		addr := m.GetNextEntrySocksAddr()
		if addr == "" {
			t.Fatal("GetNextEntrySocksAddr returned empty with populated pool")
		}
		seen[addr] = true
	}
	if len(seen) != 3 {
		t.Fatalf("round-robin should cover all 3 addrs, got %d: %#v", len(seen), seen)
	}
}

func TestEntryProxySync_ConcurrentEmptyDoesNotPanic(t *testing.T) {
	m := newEntryBoxPoolManager()
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.SyncEntryPool()
		}()
	}
	wg.Wait()
	if addr := m.GetNextEntrySocksAddr(); addr != "" {
		t.Fatalf("GetNextEntrySocksAddr() = %q, want empty", addr)
	}
}

func TestEntryProxyAdopt_ClosesOldInstantly(t *testing.T) {
	oldBox, err := box.New(box.Options{
		Context: include.Context(context.Background()),
		Options: option.Options{
			Log: &option.LogOptions{Disabled: true},
		},
	})
	if err != nil {
		t.Fatalf("create old box: %v", err)
	}

	newBox, err := box.New(box.Options{
		Context: include.Context(context.Background()),
		Options: option.Options{
			Log: &option.LogOptions{Disabled: true},
		},
	})
	if err != nil {
		oldBox.Close()
		t.Fatalf("create new box: %v", err)
	}

	m := newEntryBoxPoolManager()
	// 先预置同名旧实例（socks5://old:1080），随后用新 URI 采纳，验证同名替换关闭旧实例。
	// 这里统一用 "socks5://old:1080" 作为同一键，分别 adopt 两版实例，检查后者生效前者被关闭。
	m.mu.Lock()
	if m.instances == nil {
		m.instances = make(map[string]*entryBoxInstance)
	}
	m.instances["socks5://old:1080"] = &entryBoxInstance{uri: "socks5://old:1080", box: oldBox, socksAddr: "127.0.0.1:9999"}
	m.mu.Unlock()

	oldRef := oldBox
	if err := m.adoptForTest("socks5://old:1080", newBox, "127.0.0.1:10000"); err != nil {
		t.Fatalf("adoptForTest: %v", err)
	}

	// old box should be closed
	if err := oldRef.Close(); err == nil {
		t.Error("old box should have been closed by Adopt")
	}

	// socksAddr should reflect new address
	if addr := m.socksAddrForURI("socks5://old:1080"); addr != "127.0.0.1:10000" {
		t.Fatalf("socksAddrForURI() = %q, want 127.0.0.1:10000", addr)
	}
}

func TestStartEntryBox_UsesEphemeralPort(t *testing.T) {
	box, addr, err := startEntryBox("socks5://127.0.0.1:1080")
	if err != nil {
		t.Fatalf("startEntryBox: %v", err)
	}
	defer box.Close()

	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", addr, err)
	}
	port, _ := strconv.Atoi(portStr)
	if port <= 1024 {
		t.Fatalf("port %d <= 1024, want > 1024 (ephemeral range)", port)
	}
	if port >= 11080 && port <= 11280 {
		t.Fatalf("port %d is in the old fixed range (11080-11280), want ephemeral", port)
	}
}

func TestEntryProxySync_ConcurrentDifferentURIs(t *testing.T) {
	m := newEntryBoxPoolManager()
	var wg sync.WaitGroup
	for i := range 5 {
		wg.Add(1)
		uri := "socks5://box" + strconv.Itoa(i) + ":1080"
		go func() {
			defer wg.Done()
			b, err := box.New(box.Options{
				Context: include.Context(context.Background()),
				Options: option.Options{
					Log: &option.LogOptions{Disabled: true},
				},
			})
			if err != nil {
				t.Errorf("create candidate box: %v", err)
				return
			}
			if err := m.adoptForTest(uri, b, "127.0.0.1:1000"+strconv.Itoa(i)); err != nil {
				_ = b.Close()
				t.Errorf("adoptForTest: %v", err)
			}
		}()
	}
	wg.Wait()

	// 并发采纳不同 URI 应使池保留全部 5 个实例
	m.mu.RLock()
	count := len(m.instances)
	m.mu.RUnlock()
	if count != 5 {
		t.Fatalf("instances count = %d, want 5", count)
	}

	seen := map[string]bool{}
	for range 10 {
		addr := m.GetNextEntrySocksAddr()
		if addr == "" {
			t.Fatal("round-robin should not return empty")
		}
		seen[addr] = true
	}
	if len(seen) != 5 {
		m.mu.RLock()
		var keys []string
		for k, inst := range m.instances {
			keys = append(keys, k+"="+inst.socksAddr)
		}
		m.mu.RUnlock()
		t.Fatalf("round-robin should cover all 5 addrs, got %d; seen=%#v; instances=%#v", len(seen), seen, keys)
	}
}

// countingDialer implements ProxyDialer and counts CreateDialer invocations.
type countingDialer struct {
	createCount atomic.Int64
}

func (d *countingDialer) CreateDialer(uri string, reqID string) (func(ctx context.Context, network, addr string) (net.Conn, error), func(), error) {
	d.createCount.Add(1)
	var dialer net.Dialer
	return dialer.DialContext, func() {}, nil
}


func (d *countingDialer) StopAll()                  {}
func (d *countingDialer) GetNextEntrySocksAddr() string { return "" }
func (d *countingDialer) SyncEntryPool() error      { return nil }
func (d *countingDialer) TestEntryProxy(uri string) (func(ctx context.Context, network, addr string) (net.Conn, error), func(), error) {
	var dialer net.Dialer
	return dialer.DialContext, func() {}, nil
}

func TestInjectSecondHop_AlwaysViaTempBox(t *testing.T) {
	dialer := &countingDialer{}
	nc := NewNetworkClient(dialer)

	// secondHop non-empty → must call CreateDialer
	sess, err := nc.CreateSession(10, "http://example.com:8080", "t")
	if err != nil {
		t.Fatalf("CreateSession with http second-hop: %v", err)
	}
	if dialer.createCount.Load() != 1 {
		t.Fatalf("CreateDialer called %d times, want 1", dialer.createCount.Load())
	}
	sess.Close()

	// secondHop empty → must NOT call CreateDialer
	sess2, err := nc.CreateSession(10, "", "t2")
	if err != nil {
		t.Fatalf("CreateSession with empty second-hop: %v", err)
	}
	if dialer.createCount.Load() != 1 {
		t.Fatalf("CreateDialer called %d times, want still 1 (empty second-hop should skip CreateDialer)", dialer.createCount.Load())
	}
	sess2.Close()
}

func TestTestEntryProxy_IsolatedFromResident(t *testing.T) {
	// set up a resident entry box
	residentBox, err := box.New(box.Options{
		Context: include.Context(context.Background()),
		Options: option.Options{
			Log: &option.LogOptions{Disabled: true},
		},
	})
	if err != nil {
		t.Fatalf("create resident box: %v", err)
	}
	defer residentBox.Close()

	d := &singDialer{
		cfg:   newFakeCfg(),
		entry: newEntryBoxPoolManager(),
		boxBuilder: func(uri string) (*nodeBox, error) {
			return &nodeBox{box: &fakeCloser{}, outbound: fakeOutbound{}}, nil
		},
	}
	if err := d.entry.adoptForTest("socks5://resident:1080", residentBox, "127.0.0.1:9999"); err != nil {
		t.Fatalf("adoptForTest: %v", err)
	}

	originalAddr := d.entry.GetNextEntrySocksAddr()

	// TestEntryProxy with a different URI should not affect resident
	dialCtx, cleanup, err := d.TestEntryProxy("socks5://candidate:1080")
	if err != nil {
		t.Fatalf("TestEntryProxy: %v", err)
	}
	if dialCtx == nil {
		t.Fatal("TestEntryProxy returned nil dialCtx")
	}

	// resident state unchanged
	if addr := d.entry.GetNextEntrySocksAddr(); addr != originalAddr {
		t.Fatalf("resident socksAddr changed: %q -> %q", originalAddr, addr)
	}

	cleanup() // candidate cleanup should not close resident

	// resident still alive — Close should succeed (first close)
	if err := residentBox.Close(); err != nil {
		t.Fatalf("resident box should still be alive, but Close returned: %v", err)
	}
	// second close should return os.ErrClosed (box.go guard)
	if err := residentBox.Close(); err == nil {
		t.Error("second Close should return error (already closed)")
	}
}

// cleanupTracker 记录 cleanup 调用次数。
type cleanupTracker struct {
	count atomic.Int64
}

func TestCreateSession_RetryPattern_CloseRecreate(t *testing.T) {
	ct := &cleanupTracker{}
	dialer := &countingDialer{}
	dialer.createCount.Store(0)

	nc := NewNetworkClient(dialer)

	// CreateSession overrides the cleanup for second-hop sessions,
	// but the countingDialer returns a no-op cleanup. We simulate
	// the 429-retry pattern: close → recreate → defer-close.
	sess, err := nc.CreateSession(10, "socks5://node:1080", "retry-test")
	if err != nil {
		t.Fatalf("first CreateSession: %v", err)
	}
	// Attach a tracker to the session cleanup.
	origCleanup := sess.cleanup
	countCalled := func() {
		ct.count.Add(1)
		if origCleanup != nil {
			origCleanup()
		}
	}
	sess.cleanup = countCalled

	// Simulate 429 close
	sess.Close()
	if ct.count.Load() != 1 {
		t.Fatalf("cleanup called %d times after first Close, want 1", ct.count.Load())
	}

	// Recreate
	sess2, err := nc.CreateSession(10, "socks5://node:1080", "retry-test")
	if err != nil {
		t.Fatalf("second CreateSession: %v", err)
	}
	// Attach tracker to new session
	origCleanup2 := sess2.cleanup
	countCalled2 := func() {
		ct.count.Add(1)
		if origCleanup2 != nil {
			origCleanup2()
		}
	}
	sess2.cleanup = countCalled2

	// Simulate defer close (second session's cleanup)
	sess2.Close()
	if ct.count.Load() != 2 {
		t.Fatalf("cleanup called %d times after both Close, want 2 (first session cleanup called once, second once)", ct.count.Load())
	}
}

// hangOutbound 模拟黑洞节点：DialContext 忽略 ctx 取消，持续阻塞。
type hangOutbound struct{}

func (hangOutbound) DialContext(ctx context.Context, _ string, _ M.Socksaddr) (net.Conn, error) {
	time.Sleep(2 * time.Second)
	return nil, errors.New("unreachable")
}

func (hangOutbound) ListenPacket(ctx context.Context, _ M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("not implemented")
}

func TestMakeBoxDialFunc_TimeoutMarksClosed(t *testing.T) {
	fc := &fakeCloser{}
	nb := &nodeBox{
		box:      fc,
		outbound: hangOutbound{},
	}
	dial := makeBoxDialFunc(nb)

	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()

	_, err := dial(ctx, "tcp", "1.2.3.4:443")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("error should contain 'timeout', got: %v", err)
	}

	if !nb.closed.Load() {
		t.Error("expected nb.closed to be true after timeout")
	}

	// Second dial should return "closed" error
	_, err = dial(context.Background(), "tcp", "1.2.3.4:443")
	if err == nil {
		t.Fatal("expected error on second dial after close, got nil")
	}
	if !strings.Contains(err.Error(), "closed") {
		t.Errorf("error should contain 'closed', got: %v", err)
	}

	// cleanup must not panic
	cleanup := func() {
		if !nb.closed.Load() {
			nb.closed.Store(true)
			_ = nb.box.Close()
		}
	}
	cleanup()
	cleanup()

	if fc.closeCount.Load() == 0 {
		t.Error("box.Close should have been called")
	}
}
