package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DailyLogger implements an io.Writer that writes to logs_latest.log.
// On Close, it appends the content to a daily log file and clears logs_latest.log.
type DailyLogger struct {
	mu       sync.Mutex
	logDir   string
	latestFd *os.File
	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewDailyLogger creates a new DailyLogger that writes logs to the specified directory.
func NewDailyLogger(dir string) *DailyLogger {
	_ = os.MkdirAll(dir, 0755)

	latestPath := filepath.Join(dir, "logs_latest.log")
	f, _ := os.OpenFile(latestPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)

	if f != nil {
		now := time.Now()
		startupMsg := fmt.Sprintf("\n========== STARTUP: %s (Timestamp: %d) ==========\n", now.Format("2006-01-02 15:04:05"), now.Unix())
		_, _ = f.WriteString(startupMsg)
	}

	dl := &DailyLogger{
		logDir:   dir,
		latestFd: f,
		stopCh:   make(chan struct{}),
	}
	go dl.cleanupRoutine()
	return dl
}

func (l *DailyLogger) Write(p []byte) (n int, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.latestFd == nil {
		return 0, fmt.Errorf("logger closed")
	}
	return l.latestFd.Write(p)
}

func (l *DailyLogger) Close() error {
	l.stopOnce.Do(func() {
		close(l.stopCh)
	})

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.latestFd != nil {
		_ = l.latestFd.Close()
		l.latestFd = nil
	}

	latestPath := filepath.Join(l.logDir, "logs_latest.log")
	nowDate := time.Now().Format("2006-01-02")
	targetPath := filepath.Join(l.logDir, fmt.Sprintf("vproxy-%s.log", nowDate))

	latestData, _ := os.ReadFile(latestPath)

	// Create or open the target file regardless of whether there's data
	f, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err == nil {
		if len(latestData) > 0 {
			_, _ = f.Write(latestData)
		}
		_ = f.Close()
	}

	// Always remove logs_latest.log
	_ = os.Remove(latestPath)

	return nil
}

func (l *DailyLogger) cleanupRoutine() {
	l.cleanup()
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-l.stopCh:
			return
		case <-ticker.C:
			l.cleanup()
		}
	}
}

func (l *DailyLogger) cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()
	entries, err := os.ReadDir(l.logDir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		info, err := entry.Info()
		if err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(l.logDir, entry.Name()))
		}
	}
}
