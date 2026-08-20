package repository

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	_ "github.com/glebarez/go-sqlite"
)

type SQLite struct {
	db            *sql.DB
	path          string
	toolStateAEAD cipher.AEAD
}

func OpenReadOnly(path string) (*SQLite, error) {
	if path == "" {
		return nil, errors.New("repository path is empty")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve repository path: %w", err)
	}
	database, err := sql.Open("sqlite", "file:"+filepath.ToSlash(absPath)+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("open repository read-only: %w", err)
	}
	database.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := database.ExecContext(ctx, "PRAGMA query_only=ON; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;"); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("configure read-only repository: %w", err)
	}
	return &SQLite{db: database, path: absPath}, nil
}

func Open(path string) (*SQLite, error) {
	if path == "" {
		return nil, errors.New("repository path is empty")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve repository path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o700); err != nil {
		return nil, fmt.Errorf("create repository directory: %w", err)
	}
	key, err := loadOrCreateToolStateKey(filepath.Join(filepath.Dir(absPath), "tool-state.key"))
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initialize tool state cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize tool state AEAD: %w", err)
	}
	database, err := sql.Open("sqlite", absPath)
	if err != nil {
		return nil, fmt.Errorf("open repository: %w", err)
	}
	database.SetMaxOpenConns(1)
	repository := &SQLite{db: database, path: absPath, toolStateAEAD: aead}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := repository.initialize(ctx); err != nil {
		_ = database.Close()
		return nil, err
	}
	return repository, nil
}

func (r *SQLite) initialize(ctx context.Context) error {
	if _, err := r.db.ExecContext(ctx, "PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;"); err != nil {
		return fmt.Errorf("configure repository: %w", err)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin repository schema transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, schemaV6); err != nil {
		return fmt.Errorf("create V2 repository schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES (?, ?)",
		CurrentSchemaVersion, time.Now().UTC().Unix(),
	); err != nil {
		return fmt.Errorf("record V2 repository schema: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit V2 repository schema: %w", err)
	}
	return nil
}

func loadOrCreateToolStateKey(path string) ([]byte, error) {
	const keySize = 32
	if encoded, err := os.ReadFile(path); err == nil {
		key, decodeErr := base64.RawURLEncoding.DecodeString(string(encoded))
		if decodeErr != nil || len(key) != keySize {
			return nil, errors.New("tool state key is invalid")
		}
		return key, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read tool state key: %w", err)
	}
	key := make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate tool state key: %w", err)
	}
	encoded := []byte(base64.RawURLEncoding.EncodeToString(key))
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return loadOrCreateToolStateKey(path)
	}
	if err != nil {
		return nil, fmt.Errorf("create tool state key: %w", err)
	}
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("write tool state key: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close tool state key: %w", err)
	}
	return key, nil
}

func (r *SQLite) Close() error { return r.db.Close() }

func (r *SQLite) Path() string { return r.path }

func (r *SQLite) Checkpoint(ctx context.Context) error {
	if _, err := r.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return fmt.Errorf("checkpoint repository: %w", err)
	}
	return nil
}

func (r *SQLite) Verify(ctx context.Context) (Counts, error) {
	var version int
	if err := r.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version); err != nil {
		return Counts{}, fmt.Errorf("read repository schema version: %w", err)
	}
	if version != CurrentSchemaVersion {
		return Counts{}, fmt.Errorf("repository schema version %d, expected %d", version, CurrentSchemaVersion)
	}
	queries := []struct {
		query string
		dst   *int
	}{}
	var counts Counts
	queries = append(queries,
		struct {
			query string
			dst   *int
		}{"SELECT COUNT(*) FROM nodes", &counts.Nodes},
		struct {
			query string
			dst   *int
		}{"SELECT COUNT(*) FROM node_sources", &counts.NodeSources},
		struct {
			query string
			dst   *int
		}{"SELECT COUNT(*) FROM node_health", &counts.NodeHealth},
		struct {
			query string
			dst   *int
		}{"SELECT COUNT(*) FROM global_proxies", &counts.GlobalProxies},
		struct {
			query string
			dst   *int
		}{"SELECT COUNT(*) FROM subscriptions", &counts.Subscriptions},
		struct {
			query string
			dst   *int
		}{"SELECT COUNT(*) FROM subscription_user_agents", &counts.UserAgents},
	)
	for _, item := range queries {
		if err := r.db.QueryRowContext(ctx, item.query).Scan(item.dst); err != nil {
			return Counts{}, fmt.Errorf("verify repository: %w", err)
		}
	}
	var foreignKeyErrors int
	rows, err := r.db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return Counts{}, fmt.Errorf("verify repository references: %w", err)
	}
	for rows.Next() {
		foreignKeyErrors++
	}
	if err := rows.Close(); err != nil {
		return Counts{}, fmt.Errorf("close repository verification rows: %w", err)
	}
	if foreignKeyErrors != 0 {
		return Counts{}, fmt.Errorf("repository has %d foreign key violations", foreignKeyErrors)
	}
	return counts, nil
}
