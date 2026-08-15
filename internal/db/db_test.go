package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitDBAndMigrate(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "db_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	dbPath := filepath.Join(tempDir, "data.db")

	// Create dummy legacy files to test migration
	nodesContent := []byte(`{
		"nodes": [
			{"raw_uri": "http://127.0.0.1:8080", "type": "socks5", "name": "Node A", "disabled": false}
		]
	}`)
	_ = os.WriteFile(filepath.Join(tempDir, "nodes.json"), nodesContent, 0644)

	healthContent := []byte(`{
		"http://127.0.0.1:8080": {
			"success_count": 10,
			"fail_count": 0,
			"consecutive_failures": 0,
			"last_test_ms": 150.5,
			"last_test_error": "",
			"last_success_at": 1670000000,
			"last_fail_at": 0,
			"cooldown_until": 0
		}
	}`)
	_ = os.WriteFile(filepath.Join(tempDir, "node_health.json"), healthContent, 0644)

	// Init DB
	if errInit := InitDB(dbPath); errInit != nil {
		t.Fatalf("Failed to InitDB: %v", errInit)
	}
	defer CloseDB()

	// Verify nodes table
	var count int
	err = GlobalDB.QueryRow("SELECT COUNT(*) FROM nodes").Scan(&count)
	if err != nil || count != 1 {
		t.Errorf("Expected 1 node, got %d, error: %v", count, err)
	}

	// Verify node_health table
	var successCount int
	err = GlobalDB.QueryRow("SELECT success_count FROM node_health WHERE raw_uri = 'http://127.0.0.1:8080'").Scan(&successCount)
	if err != nil || successCount != 10 {
		t.Errorf("Expected success_count 10, got %d, error: %v", successCount, err)
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
