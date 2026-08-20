package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitDBCreatesTables(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "db_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	dbPath := filepath.Join(tempDir, "data.db")

	// Init DB
	if errInit := InitDB(dbPath); errInit != nil {
		t.Fatalf("Failed to InitDB: %v", errInit)
	}
	defer CloseDB()

	// Verify nodes table exists and starts empty
	var count int
	err = GlobalDB.QueryRow("SELECT COUNT(*) FROM nodes").Scan(&count)
	if err != nil || count != 0 {
		t.Errorf("Expected 0 nodes in fresh database, got %d, error: %v", count, err)
	}

	// Verify node_health table exists and starts empty
	var healthCount int
	err = GlobalDB.QueryRow("SELECT COUNT(*) FROM node_health").Scan(&healthCount)
	if err != nil || healthCount != 0 {
		t.Errorf("Expected 0 health records in fresh database, got %d, error: %v", healthCount, err)
	}
}

func TestInitDB_ConnectionPoolLimits(t *testing.T) {
	CloseDB()

	tempDir, err := os.MkdirTemp("", "db_pool_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	dbPath := filepath.Join(tempDir, "pool.db")

	if errInit := InitDB(dbPath); errInit != nil {
		t.Fatalf("Failed to InitDB: %v", errInit)
	}
	defer CloseDB()

	stats := GlobalDB.Stats()

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
