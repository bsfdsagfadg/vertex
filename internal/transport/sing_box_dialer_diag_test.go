package transport

import (
	"context"
	"net"
	"testing"
	"time"
)

// TestDialerBlackhole_ClientTimeoutNotEnforced_Repro reproduces the root cause:
// tls-client's HTTP/2 dial path (dialTLSHTTP2 → context.Background()) discards
// the 15s Client.Timeout, causing unbounded blocking on blackhole SOCKS5 nodes.
//
// Setup: local blackhole TCP listener (accept + Read block, never write/close).
// Expectation before fix: session.Do blocks >16s or protection ctx expires → RED.
// Expectation after fix: 15s dial timeout in makeBoxDialFunc returns ≤16s → GREEN.
func TestDialerBlackhole_ClientTimeoutNotEnforced_Repro(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1024)
				// Read once to consume the SOCKS5 handshake, then block
				// forever so the connection stays open but never writes back.
				c.Read(buf)
				select {}
			}(conn)
		}
	}()

	blackholeURI := "socks5://" + ln.Addr().String()

	dialer := NewSingDialer(&fakeCfg{})
	netClient := NewNetworkClient(dialer)

	sess, err := netClient.CreateSession(15, blackholeURI, "diag-repro")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// Close in goroutine with timeout to avoid deadlock: RoundTrip (goroutine)
	// holds rt.Lock, and sess.Close → CloseIdleConnections would block on it.
	defer func() {
		done := make(chan struct{}, 1)
		go func() {
			sess.Close()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(100 * time.Millisecond):
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resultCh := make(chan struct {
		elapsed time.Duration
		err     error
	}, 1)

	start := time.Now()
	go func() {
		_, reqErr := sess.Do(ctx, "GET", "https://www.google.com/", nil, nil)
		resultCh <- struct {
			elapsed time.Duration
			err     error
		}{time.Since(start), reqErr}
	}()

	var elapsed time.Duration
	select {
	case res := <-resultCh:
		elapsed = res.elapsed
		t.Logf("elapsed=%v, err=%v", elapsed, res.err)
	case <-ctx.Done():
		elapsed = time.Since(start)
		t.Logf("protection ctx expired at %v (session.Do hung beyond protection window)", elapsed)
	}

	if elapsed > 16*time.Second {
		t.Errorf("elapsed=%v > 16s (Client.Timeout 15s not enforced during HTTP/2 dial — bug confirmed)", elapsed)
	}
}

// TestDialerBlackhole_UnboundedHangCheck determines the worst-case upper bound
// for blackhole SOCKS5 nodes when the request context is also discarded.
// Skipped under -short because it needs a 70s window.
//
// Interpretation:
//   - Returns error within 70s → test ctx can provide upper bound (moderate severity).
//   - 70s blocks → unbounded hang; batch test can hang forever (severe).
func TestDialerBlackhole_UnboundedHangCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped in short mode (requires 70s window)")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// Intentionally NOT deferring ln.Close inside the handler path to keep
	// the listener open. Each accepted connection blocks on Read forever.
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1024)
				c.Read(buf)
				select {}
			}(conn)
		}
	}()

	blackholeURI := "socks5://" + ln.Addr().String()

	dialer := NewSingDialer(&fakeCfg{})
	netClient := NewNetworkClient(dialer)

	sess, err := netClient.CreateSession(15, blackholeURI, "diag-hang")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		done := make(chan struct{}, 1)
		go func() {
			sess.Close()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(100 * time.Millisecond):
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()

	resultCh := make(chan struct {
		elapsed time.Duration
		err     error
	}, 1)

	start := time.Now()
	go func() {
		_, reqErr := sess.Do(ctx, "GET", "https://www.google.com/", nil, nil)
		resultCh <- struct {
			elapsed time.Duration
			err     error
		}{time.Since(start), reqErr}
	}()

	select {
	case res := <-resultCh:
		elapsed := res.elapsed
		t.Logf("completed in %v with err=%v — ctx provides upper bound (moderate severity)", elapsed, res.err)
		if elapsed >= 70*time.Second {
			t.Errorf("elapsed=%v near protection limit, may be ctx-enforced", elapsed)
		}
	case <-ctx.Done():
		t.Errorf("protection ctx expired at %v — unbounded hang confirmed (severe: batch test can hang forever)", time.Since(start))
	}
}
