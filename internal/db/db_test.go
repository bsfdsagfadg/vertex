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
	var sourceType, sourceID string
	err = GlobalDB.QueryRow("SELECT source_type, source_id FROM node_sources WHERE raw_uri = 'http://127.0.0.1:8080'").Scan(&sourceType, &sourceID)
	if err != nil || sourceType != "legacy" || sourceID != "" {
		t.Errorf("migrated node should be marked legacy, got %q/%q, error: %v", sourceType, sourceID, err)
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

func TestInitDBAddsEntryProxyFailureCountToExistingDatabase(t *testing.T) {
	CloseDB()
	path := filepath.Join(t.TempDir(), "data.db")
	legacyDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = legacyDB.Exec(`CREATE TABLE entry_proxy_candidates (
		raw_uri TEXT PRIMARY KEY,
		normalized_uri TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL DEFAULT '',
		type TEXT NOT NULL DEFAULT '',
		disabled BOOLEAN NOT NULL DEFAULT 0,
		cooldown_until INTEGER NOT NULL DEFAULT 0,
		last_test_ok BOOLEAN NOT NULL DEFAULT 0,
		last_test_ms REAL NOT NULL DEFAULT 0,
		last_test_at INTEGER NOT NULL DEFAULT 0,
		last_test_error TEXT NOT NULL DEFAULT ''
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
	var count int
	if err := GlobalDB.QueryRow("SELECT COUNT(*) FROM pragma_table_info('entry_proxy_candidates') WHERE name = 'consecutive_failures'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("旧数据库未补齐 entry_proxy_candidates.consecutive_failures")
	}
}

func TestInitDBMigratesDevelopmentSourceColumnOnce(t *testing.T) {
	CloseDB()
	path := filepath.Join(t.TempDir(), "data.db")
	legacyDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = legacyDB.Exec(`CREATE TABLE nodes (
		raw_uri TEXT PRIMARY KEY,
		type TEXT NOT NULL,
		name TEXT NOT NULL,
		disabled BOOLEAN NOT NULL DEFAULT 0,
		source TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err = legacyDB.Exec("INSERT INTO nodes (raw_uri, type, name, source) VALUES ('uri-a', 'vless', 'A', 'sub-a')"); err != nil {
		t.Fatal(err)
	}
	if err = legacyDB.Close(); err != nil {
		t.Fatal(err)
	}

	if err = InitDB(path); err != nil {
		t.Fatal(err)
	}
	var sourceType, sourceID, oldColumn string
	if err = GlobalDB.QueryRow("SELECT source_type, source_id FROM node_sources WHERE raw_uri = 'uri-a'").Scan(&sourceType, &sourceID); err != nil {
		t.Fatal(err)
	}
	if sourceType != "subscription" || sourceID != "sub-a" {
		t.Fatalf("unexpected migrated source: %q/%q", sourceType, sourceID)
	}
	if err = GlobalDB.QueryRow("SELECT source FROM nodes WHERE raw_uri = 'uri-a'").Scan(&oldColumn); err != nil {
		t.Fatal(err)
	}
	if oldColumn != "" {
		t.Fatalf("legacy source column must be cleared after migration: %q", oldColumn)
	}
	CloseDB()

	if err = InitDB(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(CloseDB)
	var count int
	if err = GlobalDB.QueryRow("SELECT COUNT(*) FROM node_sources WHERE raw_uri = 'uri-a'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("source migration must be idempotent, got %d rows", count)
	}
}
