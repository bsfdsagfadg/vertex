package transport

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type blockingPipeReader struct {
	r          *io.PipeReader
	w          *io.PipeWriter
	closeCalls atomic.Int32
	readCalls  atomic.Int32
}

func newBlockingPipeReader() *blockingPipeReader {
	r, w := io.Pipe()
	return &blockingPipeReader{r: r, w: w}
}

func (b *blockingPipeReader) Read(p []byte) (n int, err error) {
	b.readCalls.Add(1)
	return b.r.Read(p)
}

func (b *blockingPipeReader) Close() error {
	b.closeCalls.Add(1)
	_ = b.w.CloseWithError(errors.New("closed"))
	return b.r.Close()
}

func TestStreamResponse_Abort_PromptReturnWithoutDrain(t *testing.T) {
	body := newBlockingPipeReader()
	sr := &StreamResponse{StatusCode: 200, Body: body}

	done := make(chan struct{})
	go func() {
		sr.Abort()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("sr.Abort() did not return promptly")
	}

	if sr.Body == nil {
		t.Fatal("expected non-nil Body")
	}
	if calls := body.closeCalls.Load(); calls != 1 {
		t.Fatalf("expected 1 close call, got %d", calls)
	}
	if calls := body.readCalls.Load(); calls != 0 {
		t.Fatalf("Abort() must not drain the body, got %d Read calls", calls)
	}
}

func TestStreamResponse_Abort_UnblocksOngoingCloseDrain(t *testing.T) {
	body := newBlockingPipeReader()
	sr := &StreamResponse{StatusCode: 200, Body: body}

	closeDone := make(chan struct{})
	go func() {
		sr.Close() // Will block inside io.Copy(io.Discard, sr.Body) until Abort closes Body
		close(closeDone)
	}()

	// Give Close a moment to enter Read/Copy
	time.Sleep(20 * time.Millisecond)

	abortDone := make(chan struct{})
	go func() {
		sr.Abort()
		close(abortDone)
	}()

	select {
	case <-abortDone:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("sr.Abort() hung while sr.Close() was running")
	}

	select {
	case <-closeDone:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("sr.Close() did not unblock after Abort()")
	}

	if calls := body.closeCalls.Load(); calls != 1 {
		t.Fatalf("expected exactly 1 body close call, got %d", calls)
	}
}

func TestStreamResponse_Close_NormalDrainAndIdempotentClose(t *testing.T) {
	r, w := io.Pipe()
	var closeCalls atomic.Int32

	wrapped := &wrappedReader{
		Reader: r,
		closeFn: func() error {
			closeCalls.Add(1)
			return r.Close()
		},
	}

	sr := &StreamResponse{StatusCode: 200, Body: wrapped}

	go func() {
		_, _ = w.Write([]byte("some stream data"))
		_ = w.Close()
	}()

	sr.Close()

	if calls := closeCalls.Load(); calls != 1 {
		t.Fatalf("expected 1 close call on normal Close(), got %d", calls)
	}

	// Secondary abort/close calls should not re-invoke Body.Close()
	sr.Abort()
	sr.Close()

	if calls := closeCalls.Load(); calls != 1 {
		t.Fatalf("expected closeCalls to remain 1 after repeated Abort/Close, got %d", calls)
	}
}

type wrappedReader struct {
	io.Reader
	closeFn func() error
	once    sync.Once
}

func (w *wrappedReader) Close() error {
	var err error
	w.once.Do(func() {
		if w.closeFn != nil {
			err = w.closeFn()
		}
	})
	return err
}
