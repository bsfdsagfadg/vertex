package vertex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// chunkFeedReader feeds data in arbitrary byte-sized slices to simulate varied network packet arrival.
type chunkFeedReader struct {
	data      []byte
	chunkSize int
	pos       int
}

func (r *chunkFeedReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	remaining := len(r.data) - r.pos
	toRead := r.chunkSize
	if toRead > remaining {
		toRead = remaining
	}
	if toRead > len(p) {
		toRead = len(p)
	}
	copy(p, r.data[r.pos:r.pos+toRead])
	r.pos += toRead
	return toRead, nil
}

func TestStreamingJSONScanner_UTF8MultiByteChunkSplits(t *testing.T) {
	// A JSON payload containing complex UTF-8 characters: Chinese, Japanese, emoji, symbols
	raw := wrap(`{"candidates":[{"content":{"parts":[{"text":"你好，世界！🌍 🚀 こんにちは \\\"escaped\\\" test"}],"role":"model"},"finishReason":"STOP"}]}`)

	// Test feeding with chunk sizes from 1 byte up to full length
	for chunkSize := 1; chunkSize <= 16; chunkSize++ {
		t.Run(fmt.Sprintf("ChunkSize_%d", chunkSize), func(t *testing.T) {
			reader := &chunkFeedReader{
				data:      []byte(raw),
				chunkSize: chunkSize,
			}
			scanner := NewStreamingJSONScanner(reader)
			var received []map[string]any

			err := scanner.Scan(func(obj map[string]any) (bool, error) {
				received = append(received, obj)
				return true, nil
			})
			if err != nil {
				t.Fatalf("Scan error with chunk size %d: %v", chunkSize, err)
			}
			if len(received) != 1 {
				t.Fatalf("Expected 1 object, got %d with chunk size %d", len(received), chunkSize)
			}
		})
	}
}

func TestStreamingJSONScanner_EscapedCharacters(t *testing.T) {
	// Various complex escape sequences inside JSON string values
	raw := `{"key1": "hello \\\"world\\\"", "key2": "brace { inside } string", "key3": "escaped backslash \\\\"}`
	reader := strings.NewReader(raw)
	scanner := NewStreamingJSONScanner(reader)

	var received map[string]any
	err := scanner.Scan(func(obj map[string]any) (bool, error) {
		received = obj
		return true, nil
	})
	if err != nil {
		t.Fatalf("Unexpected scan error: %v", err)
	}
	if received == nil {
		t.Fatal("Expected object to be parsed")
	}
	if received["key2"] != "brace { inside } string" {
		t.Errorf("Unexpected key2: %v", received["key2"])
	}
}

func TestStreamingJSONScanner_MultipleConsecutiveObjects(t *testing.T) {
	raw := `{"id": 1}{"id": 2}{"id": 3}`
	reader := strings.NewReader(raw)
	scanner := NewStreamingJSONScanner(reader)

	var ids []float64
	err := scanner.Scan(func(obj map[string]any) (bool, error) {
		if id, ok := obj["id"].(float64); ok {
			ids = append(ids, id)
		}
		return false, nil // continue scanning
	})
	if err != nil {
		t.Fatalf("Scan error: %v", err)
	}
	if len(ids) != 3 || ids[0] != 1 || ids[1] != 2 || ids[2] != 3 {
		t.Fatalf("Unexpected ids: %v", ids)
	}
}

func TestStreamingJSONScanner_EarlyStop(t *testing.T) {
	raw := `{"id": 1}{"id": 2}{"id": 3}`
	reader := strings.NewReader(raw)
	scanner := NewStreamingJSONScanner(reader)

	var count int
	err := scanner.Scan(func(obj map[string]any) (bool, error) {
		count++
		return true, nil // Stop after first object
	})
	if err != nil {
		t.Fatalf("Scan error: %v", err)
	}
	if count != 1 {
		t.Fatalf("Expected early stop at 1 object, scanned %d", count)
	}
}

func TestStreamingJSONScanner_ErrorPropagation(t *testing.T) {
	raw := `{"id": 1}`
	reader := strings.NewReader(raw)
	scanner := NewStreamingJSONScanner(reader)
	customErr := errors.New("abort on custom error")

	err := scanner.Scan(func(obj map[string]any) (bool, error) {
		return false, customErr
	})
	if !errors.Is(err, customErr) {
		t.Fatalf("Expected customErr, got %v", err)
	}
}

func TestStreamingJSONScanner_MaxBufferSizeOverflow(t *testing.T) {
	// Feed a stream with junk bytes exceeding maxBufferSize without any opening brace
	junk := bytes.Repeat([]byte("A"), 200)
	junkWithJSON := append(junk, []byte(`{"valid": true}`)...)

	scanner := NewStreamingJSONScanner(bytes.NewReader(junkWithJSON)).WithMaxBufferSize(50)
	var found bool
	err := scanner.Scan(func(obj map[string]any) (bool, error) {
		if obj["valid"] == true {
			found = true
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("Scan error: %v", err)
	}
	if !found {
		t.Fatal("Expected valid object to be scanned after junk buffer reset")
	}
}

func TestStreamingJSONScanner_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	r := &contextAwareReader{ctx: ctx}
	scanner := NewStreamingJSONScanner(r)
	err := scanner.Scan(func(map[string]any) (bool, error) {
		return false, nil
	})
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("Expected context canceled error, got %v", err)
	}
}

type contextAwareReader struct {
	ctx context.Context
}

func (c *contextAwareReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return 0, io.EOF
}
