package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDailyLogger(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	logger := NewDailyLogger(logDir)
	defer func() {
		if logger.latestFd != nil {
			_ = logger.latestFd.Close()
		}
	}()

	// Write something
	msg := []byte("hello world\n")
	n, err := logger.Write(msg)
	if err != nil {
		t.Fatalf("Failed to write to logger: %v", err)
	}
	if n != len(msg) {
		t.Fatalf("Expected to write %d bytes, wrote %d", len(msg), n)
	}

	// Verify file is created
	latestPath := filepath.Join(logDir, "logs_latest.log")
	content, err := os.ReadFile(latestPath)
	if err != nil {
		t.Fatalf("Failed to read logs_latest.log: %v", err)
	}
	if !strings.Contains(string(content), string(msg)) {
		t.Fatalf("Expected content to contain %q, got %q", string(msg), string(content))
	}

	// Close to trigger merge
	_ = logger.Close()

	// Verify logs_latest.log is deleted (os.IsNotExist)
	_, err = os.ReadFile(latestPath)
	if err == nil {
		t.Fatalf("Expected logs_latest.log to be deleted")
	}

	nowDate := time.Now().Format("2006-01-02")
	expectedName := fmt.Sprintf("vproxy-%s.log", nowDate)

	// Read merged content
	mergedContent, err := os.ReadFile(filepath.Join(logDir, expectedName))
	if err != nil {
		t.Fatalf("Failed to read merged log file: %v", err)
	}
	if !strings.Contains(string(mergedContent), string(msg)) {
		t.Fatalf("Expected merged content to contain %q, got %q", string(msg), string(mergedContent))
	}

	// Create some old files to test cleanup
	oldDate1 := time.Now().Add(-8 * 24 * time.Hour)
	oldFile1 := filepath.Join(logDir, fmt.Sprintf("vproxy-%s.log", oldDate1.Format("2006-01-02")))
	if err := os.WriteFile(oldFile1, []byte("old log 1"), 0644); err != nil {
		t.Fatalf("Failed to create old log file: %v", err)
	}
	_ = os.Chtimes(oldFile1, oldDate1, oldDate1) // Ensure mod time is old

	oldDate2 := time.Now().Add(-6 * 24 * time.Hour) // This one should NOT be deleted (within 7 days)
	oldFile2 := filepath.Join(logDir, fmt.Sprintf("vproxy-%s.log", oldDate2.Format("2006-01-02")))
	if err := os.WriteFile(oldFile2, []byte("old log 2"), 0644); err != nil {
		t.Fatalf("Failed to create old log file: %v", err)
	}
	_ = os.Chtimes(oldFile2, oldDate2, oldDate2)

	// Call cleanup manually
	logger.cleanup()

	// Verify cleanup
	entries, _ := os.ReadDir(logDir)
	var filenames []string
	for _, e := range entries {
		filenames = append(filenames, e.Name())
	}

	// oldFile1 should be deleted, oldFile2 and current file should remain.
	foundOld1 := false
	foundOld2 := false
	for _, name := range filenames {
		if name == filepath.Base(oldFile1) {
			foundOld1 = true
		}
		if name == filepath.Base(oldFile2) {
			foundOld2 = true
		}
	}

	if foundOld1 {
		t.Fatalf("Cleanup failed to delete old file: %s", oldFile1)
	}
	if !foundOld2 {
		t.Fatalf("Cleanup incorrectly deleted recent file: %s", oldFile2)
	}
}
