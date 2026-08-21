package db

import (
	"path/filepath"
	"testing"
)

func TestOpenCreatesTables(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "data.db")

	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	// Verify nodes table exists and starts empty
	var count int
	err = database.QueryRow("SELECT COUNT(*) FROM nodes").Scan(&count)
	if err != nil || count != 0 {
		t.Errorf("Expected 0 nodes in fresh database, got %d, error: %v", count, err)
	}

	// Verify node_health table exists and starts empty
	var healthCount int
	err = database.QueryRow("SELECT COUNT(*) FROM node_health").Scan(&healthCount)
	if err != nil || healthCount != 0 {
		t.Errorf("Expected 0 health records in fresh database, got %d, error: %v", healthCount, err)
	}
}

func TestOpen_ConnectionPoolLimits(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pool.db")

	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	stats := database.Stats()

	if stats.MaxOpenConnections <= 0 {
		t.Fatalf("MaxOpenConnections=%d, want > 0 (connection pool limit not set)", stats.MaxOpenConnections)
	}
	if stats.MaxOpenConnections > 1 {
		t.Fatalf("MaxOpenConnections=%d, want <= 1", stats.MaxOpenConnections)
	}

	if stats.OpenConnections <= 0 {
		t.Errorf("OpenConnections=%d, want > 0 (connection should be established)", stats.OpenConnections)
	}

	if stats.Idle <= 0 {
		t.Errorf("Idle=%d, want > 0 (idle connection should be present)", stats.Idle)
	}
}
