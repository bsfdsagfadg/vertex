package db

import (
	"database/sql"
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
			{"raw_uri": "http://127.0.0.1:8080", "type": "openai", "name": "Node A", "disabled": false}
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

func TestInitDBAddsNodeHealthStateColumnsToExistingDatabase(t *testing.T) {
	CloseDB()
	path := filepath.Join(t.TempDir(), "data.db")
	legacyDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = legacyDB.Exec(`CREATE TABLE node_health (
		raw_uri TEXT PRIMARY KEY,
		success_count INTEGER NOT NULL DEFAULT 0,
		fail_count INTEGER NOT NULL DEFAULT 0,
		consecutive_failures INTEGER NOT NULL DEFAULT 0,
		last_test_ms REAL NOT NULL DEFAULT 0,
		last_test_error TEXT NOT NULL DEFAULT '',
		last_success_at INTEGER NOT NULL DEFAULT 0,
		last_fail_at INTEGER NOT NULL DEFAULT 0,
		cooldown_until INTEGER NOT NULL DEFAULT 0
	)`)
	if err != nil {
		t.Fatal(err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatal(err)
	}

	if err := InitDB(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(CloseDB)

	for _, column := range []string{"last_429_at", "rate_limit_count", "last_sub_healthy_at"} {
		var count int
		if err := GlobalDB.QueryRow("SELECT COUNT(*) FROM pragma_table_info('node_health') WHERE name = ?", column).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("旧数据库未补齐列 %s", column)
		}
	}
}
