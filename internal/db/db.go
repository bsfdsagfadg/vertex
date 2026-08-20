package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "github.com/glebarez/go-sqlite"
)

var (
	GlobalDB *sql.DB    //nolint:gochecknoglobals
	mu       sync.Mutex //nolint:gochecknoglobals
)

// InitDB initializes the SQLite database at the given path.
func InitDB(dbPath string) error {
	mu.Lock()
	defer mu.Unlock()

	if GlobalDB != nil {
		return nil // Already initialized
	}

	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("error: %w", err)

	}

	// Use WAL mode for better concurrency
	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("error: %w", err)

	}

	// Ensure DB is reachable
	if errPing := db.Ping(); errPing != nil { //nolint:govet
		return fmt.Errorf("error: %w", errPing)

	}

	GlobalDB = db

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Create tables
	err = createTables(db)
	if err != nil {
		return err
	}

	return nil
}

func createTables(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS nodes (
		raw_uri TEXT PRIMARY KEY,
		type TEXT NOT NULL,
		name TEXT NOT NULL,
		disabled BOOLEAN NOT NULL DEFAULT 0
	);

	CREATE TABLE IF NOT EXISTS node_health (
		raw_uri TEXT PRIMARY KEY,
		success_count INTEGER NOT NULL DEFAULT 0,
		fail_count INTEGER NOT NULL DEFAULT 0,
		consecutive_failures INTEGER NOT NULL DEFAULT 0,
		last_test_ms REAL NOT NULL DEFAULT 0,
		last_test_error TEXT NOT NULL DEFAULT '',
		last_success_at INTEGER NOT NULL DEFAULT 0,
		last_fail_at INTEGER NOT NULL DEFAULT 0,
		cooldown_until INTEGER NOT NULL DEFAULT 0,
		FOREIGN KEY(raw_uri) REFERENCES nodes(raw_uri) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS entry_nodes (
		raw_uri TEXT PRIMARY KEY,
		type TEXT NOT NULL,
		name TEXT NOT NULL,
		disabled BOOLEAN NOT NULL DEFAULT 0
	);

	CREATE TABLE IF NOT EXISTS entry_node_health (
		raw_uri TEXT PRIMARY KEY,
		success_count INTEGER NOT NULL DEFAULT 0,
		fail_count INTEGER NOT NULL DEFAULT 0,
		consecutive_failures INTEGER NOT NULL DEFAULT 0,
		last_test_ms REAL NOT NULL DEFAULT 0,
		last_test_error TEXT NOT NULL DEFAULT '',
		last_success_at INTEGER NOT NULL DEFAULT 0,
		last_fail_at INTEGER NOT NULL DEFAULT 0,
		cooldown_until INTEGER NOT NULL DEFAULT 0,
		FOREIGN KEY(raw_uri) REFERENCES entry_nodes(raw_uri) ON DELETE CASCADE
	);
	`
	_, err := db.Exec(schema)
	if err != nil {
		return fmt.Errorf("error: %w", err)
	}
	return nil

}

// CloseDB closes the global database connection.
func CloseDB() {
	mu.Lock()
	defer mu.Unlock()
	if GlobalDB != nil {
		_ = GlobalDB.Close()
		GlobalDB = nil
	}
}
