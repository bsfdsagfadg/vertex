package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
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
// If it's a new database, it attempts to migrate data from nodes.json and node_health.json.
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

	isNewDB := false
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		isNewDB = true
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

	// Migrate if new
	if isNewDB {
		log.Printf("[DB] New database created at %s, attempting to migrate from legacy files...", dbPath)
		migrateFromFiles(db, dir)
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

func migrateFromFiles(db *sql.DB, configDir string) {
	migratedFolder := filepath.Join(configDir, "migrated")

	// Migrate nodes
	nodesPath := filepath.Join(configDir, "nodes.json")
	if data, err := os.ReadFile(nodesPath); err == nil {
		var d struct {
			Nodes []struct {
				Type     string `json:"type"`
				Name     string `json:"name"`
				RawURI   string `json:"raw_uri"`
				Disabled bool   `json:"disabled"`
			} `json:"nodes"`
		}
		if errUnm := json.Unmarshal(data, &d); errUnm == nil { //nolint:govet
			tx, _ := db.Begin()
			stmt, _ := tx.Prepare("INSERT OR IGNORE INTO nodes (raw_uri, type, name, disabled) VALUES (?, ?, ?, ?)")
			for _, n := range d.Nodes {
				_, _ = stmt.Exec(n.RawURI, n.Type, n.Name, n.Disabled)
			}
			_ = stmt.Close()
			_ = tx.Commit()
			log.Printf("[DB] Migrated %d nodes from nodes.json", len(d.Nodes))

			_ = os.MkdirAll(migratedFolder, 0755)
			_ = os.Rename(nodesPath, filepath.Join(migratedFolder, "nodes.json.migrated"))
		}
	}

	// Migrate node_health
	healthPath := filepath.Join(configDir, "node_health.json")
	if data, err := os.ReadFile(healthPath); err == nil {
		var healthMap map[string]struct { //nolint:govet
			SuccessCount        int     `json:"success_count"`
			FailCount           int     `json:"fail_count"`
			ConsecutiveFailures int     `json:"consecutive_failures"`
			LastTestMs          float64 `json:"last_test_ms"`
			LastTestError       string  `json:"last_test_error"`
			LastSuccessAt       int64   `json:"last_success_at"`
			LastFailAt          int64   `json:"last_fail_at"`
			CooldownUntil       int64   `json:"cooldown_until"`
		}
		if errUnm := json.Unmarshal(data, &healthMap); errUnm == nil { //nolint:govet
			tx, _ := db.Begin()
			stmt, _ := tx.Prepare(`INSERT OR REPLACE INTO node_health 
				(raw_uri, success_count, fail_count, consecutive_failures, last_test_ms, last_test_error, last_success_at, last_fail_at, cooldown_until) 
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
			migrated := 0
			for uri, h := range healthMap {
				_, err := stmt.Exec(uri, h.SuccessCount, h.FailCount, h.ConsecutiveFailures, h.LastTestMs, h.LastTestError, h.LastSuccessAt, h.LastFailAt, h.CooldownUntil) //nolint:govet
				if err == nil {
					migrated++
				}
			}
			_ = stmt.Close()
			_ = tx.Commit()
			log.Printf("[DB] Migrated %d node health records from node_health.json", migrated)

			_ = os.MkdirAll(migratedFolder, 0755)
			_ = os.Rename(healthPath, filepath.Join(migratedFolder, "node_health.json.migrated"))
		}
	}
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
