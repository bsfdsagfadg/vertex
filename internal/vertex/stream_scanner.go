package vertex

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
)

const (
	defaultScanBufSize = 16 * 1024
	maxScanBufferSize  = 4 * 1024 * 1024
)

//nolint:gochecknoglobals // Reusable buffer pool for stream scanning
var scanReadBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, defaultScanBufSize)
		return &b
	},
}

//nolint:gochecknoglobals // Reusable accumulation buffer pool for stream scanning
var scanAccumBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, defaultScanBufSize*2)
		return &b
	},
}

// StreamingJSONScanner incrementally scans an io.Reader for top-level JSON objects.
// It handles arbitrary chunk boundaries, multi-byte UTF-8 sequences, nested braces,
// and string escapes without fragile string searches or unnecessary memory allocations.
type StreamingJSONScanner struct {
	reader        *bufio.Reader
	maxBufferSize int
}

// NewStreamingJSONScanner constructs a scanner wrapping the provided io.Reader.
func NewStreamingJSONScanner(r io.Reader) *StreamingJSONScanner {
	return &StreamingJSONScanner{
		reader:        bufio.NewReader(r),
		maxBufferSize: maxScanBufferSize,
	}
}

// WithMaxBufferSize configures a custom maximum accumulation buffer size.
func (s *StreamingJSONScanner) WithMaxBufferSize(size int) *StreamingJSONScanner {
	if size > 0 {
		s.maxBufferSize = size
	}
	return s
}

// Scan reads from the stream and invokes onObject for each complete top-level JSON object.
// When onObject returns (true, nil), scanning stops cleanly and Scan returns nil.
// If onObject returns an error, scanning aborts and returns that error.
func (s *StreamingJSONScanner) Scan(onObject func(map[string]any) (bool, error)) error {
	readBufPtr := scanReadBufPool.Get().(*[]byte)
	defer scanReadBufPool.Put(readBufPtr)
	readBuf := *readBufPtr

	accumBufPtr := scanAccumBufPool.Get().(*[]byte)
	buffer := (*accumBufPtr)[:0]
	defer func() {
		if cap(buffer) <= maxScanBufferSize {
			*accumBufPtr = buffer[:0]
			scanAccumBufPool.Put(accumBufPtr)
		}
	}()

	scanPos := 0  // Cursor inside buffer to resume brace scanning across chunk reads
	startIdx := 0 // Index of the opening '{' of the current object
	braceCount := 0
	inString := false
	escape := false

	for {
		n, readErr := s.reader.Read(readBuf)
		if n > 0 {
			buffer = append(buffer, readBuf[:n]...)

			// Guard against unbounded accumulation when not in a valid brace group
			if len(buffer) > s.maxBufferSize && braceCount == 0 {
				log.Printf("[DEBUG-scan] buffer exceeded %d bytes, resetting from scanPos=%d", s.maxBufferSize, scanPos)
				buffer = buffer[scanPos:]
				scanPos = 0
				startIdx = 0
			}

			for {
				if scanPos == 0 {
					// Locate the opening '{' of the next candidate object
					startIdx = bytes.IndexByte(buffer, '{')
					if startIdx == -1 {
						buffer = buffer[:0]
						break
					}
					scanPos = startIdx
					braceCount = 0
					inString = false
					escape = false
				}

				endIdx := -1
				for i := scanPos; i < len(buffer); i++ {
					ch := buffer[i]
					if escape {
						escape = false
						continue
					}
					if ch == '\\' {
						escape = true
						continue
					}
					if ch == '"' {
						inString = !inString
						continue
					}
					if !inString {
						if ch == '{' {
							braceCount++
						} else if ch == '}' {
							braceCount--
							if braceCount == 0 {
								endIdx = i
								break
							}
						}
					}
				}

				if endIdx != -1 {
					jsonBytes := buffer[startIdx : endIdx+1]
					obj := parseJSONObject(jsonBytes)

					// Compact buffer in-place to avoid re-allocating
					copy(buffer, buffer[endIdx+1:])
					buffer = buffer[:len(buffer)-(endIdx+1)]
					scanPos = 0

					if obj != nil {
						stop, err := onObject(obj)
						if err != nil {
							return err
						}
						if stop {
							return nil
						}
					}
				} else {
					// Incomplete JSON object: mark current scan position and wait for more bytes
					scanPos = len(buffer)
					break
				}
			}
		}

		if readErr != nil {
			if errors.Is(readErr, context.Canceled) || errors.Is(readErr, context.DeadlineExceeded) {
				return fmt.Errorf("error: %w", readErr)
			}
			if errors.Is(readErr, io.EOF) {
				// Clean EOF from upstream
				return nil
			}
			return fmt.Errorf("read upstream stream: %w", readErr)
		}
	}
}

// scanStream parses an upstream streaming response incrementally into JSON objects.
func scanStream(body io.Reader, onObject func(map[string]any) (bool, error)) error {
	return NewStreamingJSONScanner(body).Scan(onObject)
}
