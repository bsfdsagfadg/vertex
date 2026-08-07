package transport

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"
)

func TestWithEntryProxyPinsExplicitDirectRoute(t *testing.T) {
	ctx := WithEntryProxy(context.Background(), "")
	entryURI, pinned := entryProxyFromContext(ctx)
	if !pinned || entryURI != "" {
		t.Fatalf("explicit direct route was not preserved: uri=%q pinned=%v", entryURI, pinned)
	}
}

type closeUnblocksReader struct {
	readStarted chan struct{}
	closed      chan struct{}
	readOnce    sync.Once
	closeOnce   sync.Once
}

func newCloseUnblocksReader() *closeUnblocksReader {
	return &closeUnblocksReader{
		readStarted: make(chan struct{}),
		closed:      make(chan struct{}),
	}
}

func (r *closeUnblocksReader) Read([]byte) (int, error) {
	r.readOnce.Do(func() { close(r.readStarted) })
	<-r.closed
	return 0, io.ErrClosedPipe
}

func (r *closeUnblocksReader) Close() error {
	r.closeOnce.Do(func() { close(r.closed) })
	return nil
}

func TestStreamResponseCloseDoesNotDrainOpenStream(t *testing.T) {
	body := newCloseUnblocksReader()
	response := &StreamResponse{StatusCode: 200, Body: body}
	done := make(chan struct{})
	go func() {
		response.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Close 不应等待仍保持打开的上游流被排干")
	}

	select {
	case <-body.readStarted:
		t.Fatal("Close 不应读取并排干流式响应 Body")
	default:
	}
}
