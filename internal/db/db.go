package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

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
	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("error: %w", err)

	}

	// Ensure DB is reachable
	if errPing := db.Ping(); errPing != nil { //nolint:govet
		_ = db.Close()
		return fmt.Errorf("error: %w", errPing)

	}

	GlobalDB = db

	// Create tables
	err = createTables(db)
	if err != nil {
		_ = db.Close()
		GlobalDB = nil
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
		last_429_at INTEGER NOT NULL DEFAULT 0,
		rate_limit_count INTEGER NOT NULL DEFAULT 0,
		last_sub_healthy_at INTEGER NOT NULL DEFAULT 0,
		FOREIGN KEY(raw_uri) REFERENCES nodes(raw_uri) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS node_sources (
		raw_uri TEXT NOT NULL,
		source_type TEXT NOT NULL,
		source_id TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (raw_uri, source_type, source_id),
		FOREIGN KEY(raw_uri) REFERENCES nodes(raw_uri) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_node_sources_owner
	ON node_sources(source_type, source_id);

	-- Responses state is intentionally kept as JSON aggregates.  These tables
	-- are additive to the original node schema and can therefore be created on
	-- existing installations without changing any node primary keys.
	CREATE TABLE IF NOT EXISTS responses (
		id TEXT PRIMARY KEY,
		status TEXT NOT NULL,
		model TEXT NOT NULL DEFAULT '',
		aggregate_json BLOB NOT NULL DEFAULT '{}',
		request_json BLOB NOT NULL DEFAULT '{}',
		input_json BLOB NOT NULL DEFAULT '[]',
		output_json BLOB NOT NULL DEFAULT '[]',
		usage_json BLOB NOT NULL DEFAULT '{}',
		error_json BLOB NOT NULL DEFAULT 'null',
		incomplete_json BLOB NOT NULL DEFAULT 'null',
		metadata_json BLOB NOT NULL DEFAULT '{}',
		previous_response_id TEXT NOT NULL DEFAULT '',
		conversation_id TEXT NOT NULL DEFAULT '',
		store BOOLEAN NOT NULL DEFAULT 1,
		background BOOLEAN NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		completed_at INTEGER NOT NULL DEFAULT 0,
		expires_at INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_responses_expiry ON responses(expires_at);
	CREATE INDEX IF NOT EXISTS idx_responses_conversation ON responses(conversation_id, created_at);

	CREATE TABLE IF NOT EXISTS idempotency_keys (
		endpoint TEXT NOT NULL,
		idempotency_key TEXT NOT NULL,
		body_hash TEXT NOT NULL,
		resource_kind TEXT NOT NULL DEFAULT 'response',
		resource_id TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL,
		PRIMARY KEY(endpoint, idempotency_key)
	);
	CREATE INDEX IF NOT EXISTS idx_idempotency_expiry ON idempotency_keys(expires_at);

	CREATE TABLE IF NOT EXISTS conversations (
		id TEXT PRIMARY KEY,
		metadata_json BLOB NOT NULL DEFAULT '{}',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		next_ordinal INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE IF NOT EXISTS conversation_items (
		id TEXT PRIMARY KEY,
		conversation_id TEXT NOT NULL,
		ordinal INTEGER NOT NULL,
		item_json BLOB NOT NULL,
		created_at INTEGER NOT NULL,
		UNIQUE(conversation_id, ordinal),
		FOREIGN KEY(conversation_id) REFERENCES conversations(id) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_conversation_items_page ON conversation_items(conversation_id, ordinal);

	CREATE TABLE IF NOT EXISTS tool_states (
		external_call_id TEXT PRIMARY KEY,
		response_id TEXT NOT NULL DEFAULT '',
		conversation_id TEXT NOT NULL DEFAULT '',
		upstream_operation TEXT NOT NULL DEFAULT '',
		opaque_blob BLOB NOT NULL,
		expires_at INTEGER NOT NULL,
		consumed_at INTEGER NOT NULL DEFAULT 0,
		transcript_hash TEXT NOT NULL DEFAULT '',
		consume_hash TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX IF NOT EXISTS idx_tool_states_expiry ON tool_states(expires_at);
	CREATE INDEX IF NOT EXISTS idx_tool_states_response ON tool_states(response_id);

	CREATE TABLE IF NOT EXISTS resource_events (
		resource_kind TEXT NOT NULL,
		resource_id TEXT NOT NULL,
		sequence_number INTEGER NOT NULL,
		event_id TEXT NOT NULL UNIQUE,
		event_type TEXT NOT NULL,
		event_json BLOB NOT NULL,
		created_at INTEGER NOT NULL,
		PRIMARY KEY(resource_kind, resource_id, sequence_number)
	);
	CREATE INDEX IF NOT EXISTS idx_resource_events_resume ON resource_events(resource_kind, resource_id, event_id);
	CREATE TABLE IF NOT EXISTS resource_event_counters (
		resource_kind TEXT NOT NULL,
		resource_id TEXT NOT NULL,
		next_sequence INTEGER NOT NULL,
		PRIMARY KEY(resource_kind, resource_id)
	);
	CREATE TABLE IF NOT EXISTS entry_proxy_candidates (
		raw_uri TEXT PRIMARY KEY,
		normalized_uri TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL DEFAULT '',
		type TEXT NOT NULL DEFAULT '',
		disabled BOOLEAN NOT NULL DEFAULT 0,
		cooldown_until INTEGER NOT NULL DEFAULT 0,
		last_test_ok BOOLEAN NOT NULL DEFAULT 0,
		last_test_ms REAL NOT NULL DEFAULT 0,
		last_test_at INTEGER NOT NULL DEFAULT 0,
		last_test_error TEXT NOT NULL DEFAULT '',
		consecutive_failures INTEGER NOT NULL DEFAULT 0
	);
	`
	_, err := db.Exec(schema)
	if err != nil {
		return fmt.Errorf("error: %w", err)
	}

	if err := ensureNodeHealthColumns(db); err != nil {
		return err
	}
	if err := ensureEntryProxyCandidateColumns(db); err != nil {
		return err
	}
	if err := ensureResponseColumns(db); err != nil {
		return err
	}
	return ensureNodeSources(db)
}

// ensureResponseColumns upgrades an early Responses table created before the
// JSON aggregate column was introduced.  ALTER is intentionally additive so
// existing resource data remains untouched.
func ensureResponseColumns(db *sql.DB) error {
	rows, err := db.Query("PRAGMA table_info(responses)")
	if err != nil {
		return fmt.Errorf("read responses schema: %w", err)
	}
	defer rows.Close()
	hasAggregate := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return fmt.Errorf("scan responses schema: %w", err)
		}
		if name == "aggregate_json" {
			hasAggregate = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !hasAggregate {
		if _, err := db.Exec("ALTER TABLE responses ADD COLUMN aggregate_json BLOB NOT NULL DEFAULT '{}'"); err != nil {
			return fmt.Errorf("add responses.aggregate_json: %w", err)
		}
	}
	return nil
}

func ensureEntryProxyCandidateColumns(db *sql.DB) error {
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('entry_proxy_candidates') WHERE name = 'consecutive_failures'").Scan(&count); err != nil {
		return fmt.Errorf("read entry_proxy_candidates schema: %w", err)
	}
	if count > 0 {
		return nil
	}
	if _, err := db.Exec("ALTER TABLE entry_proxy_candidates ADD COLUMN consecutive_failures INTEGER NOT NULL DEFAULT 0"); err != nil {
		return fmt.Errorf("add entry_proxy_candidates.consecutive_failures: %w", err)
	}
	return nil
}

func ensureNodeSources(db *sql.DB) error {
	rows, err := db.Query("PRAGMA table_info(nodes)")
	if err != nil {
		return fmt.Errorf("read nodes schema: %w", err)
	}
	hasLegacySourceColumn := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if errScan := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); errScan != nil {
			_ = rows.Close()
			return fmt.Errorf("scan nodes schema: %w", errScan)
		}
		if name == "source" {
			hasLegacySourceColumn = true
		}
	}
	if errClose := rows.Close(); errClose != nil {
		return fmt.Errorf("close nodes schema rows: %w", errClose)
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin node source migration: %w", err)
	}
	if hasLegacySourceColumn {
		if _, err = tx.Exec(`INSERT OR IGNORE INTO node_sources (raw_uri, source_type, source_id)
			SELECT raw_uri, 'subscription', source FROM nodes WHERE source <> ''`); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migrate subscription node sources: %w", err)
		}
		if _, err = tx.Exec("UPDATE nodes SET source = '' WHERE source <> ''"); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("clear legacy node source column: %w", err)
		}
	}
	if _, err = tx.Exec(`INSERT OR IGNORE INTO node_sources (raw_uri, source_type, source_id)
		SELECT n.raw_uri, 'legacy', '' FROM nodes n
		WHERE NOT EXISTS (SELECT 1 FROM node_sources s WHERE s.raw_uri = n.raw_uri)`); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("mark legacy node sources: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit node source migration: %w", err)
	}
	return nil
}

func ensureNodeHealthColumns(db *sql.DB) error {
	rows, err := db.Query("PRAGMA table_info(node_health)")
	if err != nil {
		return fmt.Errorf("read node_health schema: %w", err)
	}
	existing := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if errScan := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); errScan != nil {
			_ = rows.Close()
			return fmt.Errorf("scan node_health schema: %w", errScan)
		}
		existing[name] = true
	}
	if errClose := rows.Close(); errClose != nil {
		return fmt.Errorf("close node_health schema rows: %w", errClose)
	}

	columns := []struct {
		name       string
		definition string
	}{
		{"last_429_at", "INTEGER NOT NULL DEFAULT 0"},
		{"rate_limit_count", "INTEGER NOT NULL DEFAULT 0"},
		{"last_sub_healthy_at", "INTEGER NOT NULL DEFAULT 0"},
	}
	for _, column := range columns {
		if existing[column.name] {
			continue
		}
		query := fmt.Sprintf("ALTER TABLE node_health ADD COLUMN %s %s", column.name, column.definition)
		if _, errAlter := db.Exec(query); errAlter != nil {
			return fmt.Errorf("add node_health.%s: %w", column.name, errAlter)
		}
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
				Source   string `json:"source"`
			} `json:"nodes"`
		}
		if errUnm := json.Unmarshal(data, &d); errUnm == nil { //nolint:govet
			tx, _ := db.Begin()
			stmt, _ := tx.Prepare("INSERT OR IGNORE INTO nodes (raw_uri, type, name, disabled) VALUES (?, ?, ?, ?)")
			sourceStmt, _ := tx.Prepare("INSERT OR IGNORE INTO node_sources (raw_uri, source_type, source_id) VALUES (?, ?, ?)")
			for _, n := range d.Nodes {
				_, _ = stmt.Exec(n.RawURI, n.Type, n.Name, n.Disabled)
				sourceType := "legacy"
				sourceID := ""
				if n.Source != "" {
					sourceType = "subscription"
					sourceID = n.Source
				}
				if sourceStmt != nil {
					_, _ = sourceStmt.Exec(n.RawURI, sourceType, sourceID)
				}
			}
			if stmt != nil {
				_ = stmt.Close()
			}
			if sourceStmt != nil {
				_ = sourceStmt.Close()
			}
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
			Last429At           int64   `json:"last_429_at"`
			RateLimitCount      int     `json:"rate_limit_count"`
			LastSubHealthyAt    int64   `json:"last_sub_healthy_at"`
		}
		if errUnm := json.Unmarshal(data, &healthMap); errUnm == nil { //nolint:govet
			tx, _ := db.Begin()
			stmt, _ := tx.Prepare(`INSERT OR REPLACE INTO node_health
				(raw_uri, success_count, fail_count, consecutive_failures, last_test_ms, last_test_error, last_success_at, last_fail_at, cooldown_until, last_429_at, rate_limit_count, last_sub_healthy_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
			migrated := 0
			for uri, h := range healthMap {
				_, err := stmt.Exec(uri, h.SuccessCount, h.FailCount, h.ConsecutiveFailures, h.LastTestMs, h.LastTestError, h.LastSuccessAt, h.LastFailAt, h.CooldownUntil, h.Last429At, h.RateLimitCount, h.LastSubHealthyAt) //nolint:govet
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
