package anonymousgraph

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

var scanBufferPool = sync.Pool{ //nolint:gochecknoglobals
	New: func() any {
		buffer := make([]byte, 16*1024)
		return &buffer
	},
}

// ScanObjects incrementally frames concatenated anonymous Graph JSON objects.
// Interpretation and downstream event semantics remain outside this package.
func ScanObjects(body io.Reader, onObject func(map[string]any) (bool, error)) error {
	if body == nil || onObject == nil {
		return errors.New("anonymous graph stream scanner has nil input")
	}
	reader := bufio.NewReader(body)
	readBufferPointer := scanBufferPool.Get().(*[]byte)
	defer scanBufferPool.Put(readBufferPointer)
	readBuffer := *readBufferPointer

	var buffer []byte
	scanPosition, startIndex, braceCount := 0, 0, 0
	inString, escape := false, false
	const maximumBufferSize = 4 * 1024 * 1024

	for {
		count, readErr := reader.Read(readBuffer)
		if count > 0 {
			buffer = append(buffer, readBuffer[:count]...)
			if len(buffer) > maximumBufferSize && braceCount == 0 {
				buffer = buffer[scanPosition:]
				scanPosition, startIndex = 0, 0
			}
			for {
				if scanPosition == 0 {
					startIndex = bytes.IndexByte(buffer, '{')
					if startIndex == -1 {
						buffer = buffer[:0]
						break
					}
					scanPosition, braceCount = startIndex, 0
					inString, escape = false, false
				}
				endIndex := -1
				for index := scanPosition; index < len(buffer); index++ {
					character := buffer[index]
					if escape {
						escape = false
						continue
					}
					if character == '\\' {
						escape = true
						continue
					}
					if character == '"' {
						inString = !inString
						continue
					}
					if inString {
						continue
					}
					switch character {
					case '{':
						braceCount++
					case '}':
						braceCount--
						if braceCount == 0 {
							endIndex = index
							index = len(buffer)
						}
					}
				}
				if endIndex == -1 {
					scanPosition = len(buffer)
					break
				}
				var object map[string]any
				_ = json.Unmarshal(buffer[startIndex:endIndex+1], &object)
				copy(buffer, buffer[endIndex+1:])
				buffer = buffer[:len(buffer)-(endIndex+1)]
				scanPosition = 0
				if object != nil {
					stop, err := onObject(object)
					if err != nil {
						return err
					}
					if stop {
						return nil
					}
				}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, context.Canceled) || errors.Is(readErr, context.DeadlineExceeded) {
				return fmt.Errorf("anonymous graph stream: %w", readErr)
			}
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return fmt.Errorf("read anonymous graph stream: %w", readErr)
		}
	}
}
