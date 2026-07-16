package transport

import (
	"context"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	M "github.com/sagernet/sing/common/metadata"
)

// ─── fakes ───

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

type fakeCfg struct {
	proxyURL atomic.Value // string
}

func newFakeCfg(proxyURL string) *fakeCfg {
	c := &fakeCfg{}
	c.proxyURL.Store(proxyURL)
	return c
}

func (c *fakeCfg) ProxyURL() string {
	v, _ := c.proxyURL.Load().(string)
	return v
}

// ConfigProvider methods — all panic except ProxyURL
func (c *fakeCfg) PortAPI() int                                { panic("unexpected") }
func (c *fakeCfg) MaxRetries() int                             { panic("unexpected") }
func (c *fakeCfg) AdminPassword() string                       { panic("unexpected") }
func (c *fakeCfg) ProxyURLCandidates() []config.ProxyCandidate { panic("unexpected") }
func (c *fakeCfg) DebugPprof() bool                            { panic("unexpected") }
func (c *fakeCfg) DebugMode() bool                             { panic("unexpected") }
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
func (c *fakeCfg) StickyNodePriority() bool                    { panic("unexpected") }
func (c *fakeCfg) ParallelPoolRetryEnabled() bool              { panic("unexpected") }
func (c *fakeCfg) ParallelPoolSize() int                       { panic("unexpected") }
func (c *fakeCfg) ParallelPoolDelayDynamic() bool              { panic("unexpected") }
func (c *fakeCfg) ActiveNodeURI() string                       { panic("unexpected") }
func (c *fakeCfg) ParallelNodeTopK() int                       { panic("unexpected") }
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

type fakeBuilder struct {
	count    atomic.Int64
	preBuild func() // optional hook, called inside builder before returning
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

// ─── tests ───

func TestBox_BasicBuildAndCache(t *testing.T) {
	cfg := newFakeCfg("socks5://entry:1080")
	builder := &fakeBuilder{}
	d := &singDialer{
		cfg:        cfg,
		boxCache:   make(map[boxKey]*nodeBox),
		boxBuilder: builder.build,
	}

	nb, err := d.box("socks5://node:1080")
	if err != nil {
		t.Fatalf("box: %v", err)
	}
	if nb == nil {
		t.Fatal("box returned nil")
	}
	if builder.count.Load() != 1 {
		t.Fatalf("builder called %d times, want 1", builder.count.Load())
	}

	// second call should hit cache
	nb2, err := d.box("socks5://node:1080")
	if err != nil {
		t.Fatalf("box second call: %v", err)
	}
	if nb != nb2 {
		t.Fatal("second call returned different *nodeBox")
	}
	if builder.count.Load() != 1 {
		t.Fatalf("builder should still be 1 after cache hit, got %d", builder.count.Load())
	}
}

func TestBox_DedupUnderConcurrency(t *testing.T) {
	cfg := newFakeCfg("socks5://entry:1080")
	builder := &fakeBuilder{
		preBuild: func() {
			time.Sleep(2 * time.Millisecond)
		},
	}
	d := &singDialer{
		cfg:        cfg,
		boxCache:   make(map[boxKey]*nodeBox),
		boxBuilder: builder.build,
	}

	const goroutines = 50
	results := make(map[*nodeBox]int)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			nb, err := d.box("socks5://node:1080")
			if err != nil {
				t.Errorf("box: %v", err)
				return
			}
			mu.Lock()
			results[nb]++
			mu.Unlock()
		}()
	}
	wg.Wait()

	// all goroutines returned the same *nodeBox
	if len(results) != 1 {
		t.Fatalf("expected 1 unique *nodeBox, got %d", len(results))
	}
	if results[nil] > 0 {
		t.Fatal("some goroutines got nil *nodeBox")
	}

	d.cacheMu.Lock()
	cacheLen := len(d.boxCache)
	d.cacheMu.Unlock()
	if cacheLen != 1 {
		t.Fatalf("boxCache should have 1 entry, got %d", cacheLen)
	}

	// builder count should be > 1 when the sleep amplifies the race window
	count := builder.count.Load()
	if count < 2 {
		t.Logf("note: builder called %d times (sleep may not have overlapped all goroutines)", count)
	}
}

func TestBox_EntryChangeFlushes(t *testing.T) {
	cfg := newFakeCfg("socks5://entry:1080")
	builder := &fakeBuilder{}
	d := &singDialer{
		cfg:        cfg,
		boxCache:   make(map[boxKey]*nodeBox),
		boxBuilder: builder.build,
	}

	nb, err := d.box("socks5://node:1080")
	if err != nil {
		t.Fatalf("box: %v", err)
	}
	fc := nb.box.(*fakeCloser)

	// change entry
	cfg.proxyURL.Store("socks5://new-entry:1081")

	nb2, err := d.box("socks5://node:1080")
	if err != nil {
		t.Fatalf("box after entry change: %v", err)
	}

	if fc.closeCount.Load() != 1 {
		t.Fatalf("old box closeCount=1 after flush, got %d", fc.closeCount.Load())
	}
	if nb == nb2 {
		t.Fatal("entry change should produce a different *nodeBox")
	}

	d.cacheMu.Lock()
	cacheLen := len(d.boxCache)
	d.cacheMu.Unlock()
	if cacheLen != 1 {
		t.Fatalf("boxCache should have 1 entry, got %d", cacheLen)
	}

	fc2 := nb2.box.(*fakeCloser)
	if fc2.closeCount.Load() != 0 {
		t.Fatalf("new box should not be closed, got closeCount=%d", fc2.closeCount.Load())
	}
}

func TestBox_PublishRaceEntryChanged(t *testing.T) {
	cfg := newFakeCfg("socks5://entry:1080")

	enterBuild := make(chan struct{})
	proceed := make(chan struct{})

	var once sync.Once
	builder := &fakeBuilder{
		preBuild: func() {
			once.Do(func() {
				enterBuild <- struct{}{}
				<-proceed
			})
		},
	}

	d := &singDialer{
		cfg:        cfg,
		boxCache:   make(map[boxKey]*nodeBox),
		boxBuilder: builder.build,
	}

	var wg sync.WaitGroup
	wg.Add(1)
	var nb1 *nodeBox
	var err1 error

	go func() {
		defer wg.Done()
		nb1, err1 = d.box("socks5://node:1080")
	}()

	<-enterBuild
	// change entry while goroutine is building
	cfg.proxyURL.Store("socks5://new-entry:1081")
	close(proceed)

	wg.Wait()

	if err1 != nil {
		t.Errorf("box error: %v", err1)
	}
	if nb1 == nil {
		t.Fatal("box returned nil")
	}

	// after retry, cache should have the new entry
	d.cacheMu.Lock()
	_, hasNew := d.boxCache[boxKey{entry: "socks5://new-entry:1081", node: "socks5://node:1080"}]
	_, hasOld := d.boxCache[boxKey{entry: "socks5://entry:1080", node: "socks5://node:1080"}]
	d.cacheMu.Unlock()

	if !hasNew {
		t.Error("cache should contain the new entry after retry")
	}
	if hasOld {
		t.Error("cache should NOT contain the old entry after retry")
	}

	// builder should have been called at least twice (once for each attempt)
	if builder.count.Load() < 2 {
		t.Errorf("builder should be called >= 2 times (retry), got %d", builder.count.Load())
	}
}

func TestBox_BuilderError(t *testing.T) {
	cfg := newFakeCfg("socks5://entry:1080")
	errBuilder := func(uri string) (*nodeBox, error) {
		return nil, assertAnError("build failed")
	}
	d := &singDialer{
		cfg:        cfg,
		boxCache:   make(map[boxKey]*nodeBox),
		boxBuilder: errBuilder,
	}

	_, err := d.box("socks5://node:1080")
	if err == nil {
		t.Fatal("expected error from builder, got nil")
	}
	if !strings.Contains(err.Error(), "build failed") {
		t.Fatalf("unexpected error message: %v", err)
	}

	d.cacheMu.Lock()
	cacheLen := len(d.boxCache)
	d.cacheMu.Unlock()
	if cacheLen != 0 {
		t.Fatalf("boxCache should be empty after builder error, got %d entries", cacheLen)
	}
}

type assertAnError string

func (e assertAnError) Error() string { return string(e) }

func TestBox_RemoveDialer_ClosesBox(t *testing.T) {
	cfg := newFakeCfg("")
	builder := &fakeBuilder{}
	d := &singDialer{
		cfg:        cfg,
		boxCache:   make(map[boxKey]*nodeBox),
		boxBuilder: builder.build,
	}

	nb, err := d.box("socks5://node:1080")
	if err != nil {
		t.Fatalf("box: %v", err)
	}
	fc := nb.box.(*fakeCloser)

	d.RemoveDialer("socks5://node:1080")

	if fc.closeCount.Load() != 1 {
		t.Fatalf("box should be closed after RemoveDialer, closeCount=%d", fc.closeCount.Load())
	}

	d.cacheMu.Lock()
	cacheLen := len(d.boxCache)
	d.cacheMu.Unlock()
	if cacheLen != 0 {
		t.Fatalf("boxCache should be empty after RemoveDialer, got %d entries", cacheLen)
	}
}

func TestBox_StopAll_ClosesAll(t *testing.T) {
	cfg := newFakeCfg("")
	builder := &fakeBuilder{}
	d := &singDialer{
		cfg:        cfg,
		boxCache:   make(map[boxKey]*nodeBox),
		boxBuilder: builder.build,
	}

	uri1 := "socks5://node1:1080"
	uri2 := "socks5://node2:1080"

	nb1, _ := d.box(uri1)
	nb2, _ := d.box(uri2)
	fc1 := nb1.box.(*fakeCloser)
	fc2 := nb2.box.(*fakeCloser)

	d.StopAll()

	if fc1.closeCount.Load() != 1 {
		t.Fatalf("box1 should be closed after StopAll, closeCount=%d", fc1.closeCount.Load())
	}
	if fc2.closeCount.Load() != 1 {
		t.Fatalf("box2 should be closed after StopAll, closeCount=%d", fc2.closeCount.Load())
	}

	d.cacheMu.Lock()
	cacheLen := len(d.boxCache)
	d.cacheMu.Unlock()
	if cacheLen != 0 {
		t.Fatalf("boxCache should be empty after StopAll, got %d entries", cacheLen)
	}
}
