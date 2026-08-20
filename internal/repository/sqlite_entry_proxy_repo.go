package repository

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/domain"
	"github.com/jmoiron/sqlx"
)

// SQLiteEntryProxyRepository implements EntryProxyRepository on SQLite.
type SQLiteEntryProxyRepository struct {
	db *sqlx.DB
	mu sync.RWMutex
}

// NewSQLiteEntryProxyRepository constructs a new SQLiteEntryProxyRepository.
func NewSQLiteEntryProxyRepository(db *sqlx.DB) *SQLiteEntryProxyRepository {
	return &SQLiteEntryProxyRepository{
		db: db,
	}
}

func (r *SQLiteEntryProxyRepository) GetAll(ctx context.Context) ([]domain.EntryProxyCandidate, error) {
	var list []domain.EntryProxyCandidate
	err := r.db.SelectContext(ctx, &list, `
		SELECT raw_uri, normalized_uri, name, type, disabled, cooldown_until,
		       last_test_ok, last_test_ms, last_test_at, last_test_error, consecutive_failures
		FROM entry_proxy_candidates ORDER BY rowid
	`)
	if err != nil {
		return nil, fmt.Errorf("select entry proxies: %w", err)
	}
	return list, nil
}

func (r *SQLiteEntryProxyRepository) GetByNormalizedURI(ctx context.Context, normalizedURI string) (*domain.EntryProxyCandidate, error) {
	var candidate domain.EntryProxyCandidate
	err := r.db.GetContext(ctx, &candidate, `
		SELECT raw_uri, normalized_uri, name, type, disabled, cooldown_until,
		       last_test_ok, last_test_ms, last_test_at, last_test_error, consecutive_failures
		FROM entry_proxy_candidates WHERE normalized_uri = ?
	`, normalizedURI)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("select entry proxy: %w", err)
	}
	return &candidate, nil
}

func (r *SQLiteEntryProxyRepository) Add(ctx context.Context, candidate domain.EntryProxyCandidate) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO entry_proxy_candidates (
			raw_uri, normalized_uri, name, type, disabled, cooldown_until,
			last_test_ok, last_test_ms, last_test_at, last_test_error, consecutive_failures
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(normalized_uri) DO UPDATE SET
			name = excluded.name,
			type = excluded.type,
			disabled = excluded.disabled
	`, candidate.RawURI, candidate.NormalizedURI, candidate.Name, candidate.Type, candidate.Disabled,
		candidate.CooldownUntil, candidate.LastTestOK, candidate.LastTestMs, candidate.LastTestAt,
		candidate.LastTestError, candidate.ConsecutiveFailures)
	if err != nil {
		return fmt.Errorf("add entry proxy: %w", err)
	}
	return nil
}

func (r *SQLiteEntryProxyRepository) Remove(ctx context.Context, normalizedURI string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	res, err := r.db.ExecContext(ctx, "DELETE FROM entry_proxy_candidates WHERE normalized_uri = ?", normalizedURI)
	if err != nil {
		return fmt.Errorf("remove entry proxy: %w", err)
	}
	count, _ := res.RowsAffected()
	if count == 0 {
		return fmt.Errorf("candidate not found")
	}
	return nil
}

func (r *SQLiteEntryProxyRepository) RemoveDisabled(ctx context.Context) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var removed []string
	if err := tx.SelectContext(ctx, &removed, "SELECT raw_uri FROM entry_proxy_candidates WHERE disabled = 1"); err != nil {
		return nil, fmt.Errorf("select disabled entry proxies: %w", err)
	}
	if len(removed) == 0 {
		return nil, nil
	}

	if _, err := tx.ExecContext(ctx, "DELETE FROM entry_proxy_candidates WHERE disabled = 1"); err != nil {
		return nil, fmt.Errorf("delete disabled entry proxies: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit delete disabled entry proxies: %w", err)
	}
	return removed, nil
}

func (r *SQLiteEntryProxyRepository) UpdateTestResult(
	ctx context.Context,
	normalizedURI string,
	ok bool,
	latencyMs float64,
	errText string,
	cooldownSec int,
	countScheduledFailure bool,
	autoDisable bool,
	failureLimit int,
) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var consecutiveFailures int
	var disabled bool
	err := r.db.QueryRowContext(ctx, `
		SELECT consecutive_failures, disabled FROM entry_proxy_candidates WHERE normalized_uri = ?
	`, normalizedURI).Scan(&consecutiveFailures, &disabled)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, fmt.Errorf("candidate not found")
		}
		return false, fmt.Errorf("query candidate: %w", err)
	}

	if cooldownSec < 0 {
		cooldownSec = 0
	}
	cooldown := int64(0)
	autoDisabled := false
	now := time.Now().Unix()

	if ok {
		consecutiveFailures = 0
	} else {
		cooldown = now + int64(cooldownSec)
		if countScheduledFailure {
			consecutiveFailures++
			if autoDisable && failureLimit > 0 && consecutiveFailures >= failureLimit && !disabled {
				disabled = true
				autoDisabled = true
				cooldown = 0
			}
		}
	}

	_, err = r.db.ExecContext(ctx, `
		UPDATE entry_proxy_candidates
		SET last_test_ok = ?, last_test_ms = ?, last_test_at = ?, last_test_error = ?,
		    cooldown_until = ?, consecutive_failures = ?, disabled = ?
		WHERE normalized_uri = ?
	`, ok, latencyMs, now, errText, cooldown, consecutiveFailures, disabled, normalizedURI)
	if err != nil {
		return false, fmt.Errorf("update test result: %w", err)
	}
	return autoDisabled, nil
}

func (r *SQLiteEntryProxyRepository) Exists(ctx context.Context, normalizedURI string) (bool, error) {
	var count int
	err := r.db.GetContext(ctx, &count, "SELECT COUNT(*) FROM entry_proxy_candidates WHERE normalized_uri = ?", normalizedURI)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
