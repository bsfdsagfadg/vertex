package repository

import (
	"context"
	"database/sql"
	"fmt"
)

type GlobalProxyRecord struct {
	GlobalProxy
	GlobalProxyHealth
	Sources []GlobalProxySource
}

func (r *SQLite) ListGlobalProxies(ctx context.Context) ([]GlobalProxyRecord, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT p.id, p.raw_uri, p.canonical_identity,
		p.endpoint_fingerprint, p.name, p.type, p.disabled, p.pinned,
		COALESCE(h.cooldown_until,0), COALESCE(h.last_test_ok,0), COALESCE(h.last_test_ms,0),
		COALESCE(h.last_test_at,0), COALESCE(h.last_test_error,''), COALESCE(h.consecutive_failures,0)
		FROM global_proxies p LEFT JOIN global_proxy_health h ON h.global_proxy_id=p.id ORDER BY p.rowid`)
	if err != nil {
		return nil, fmt.Errorf("list global proxies: %w", err)
	}
	defer rows.Close()
	records := make([]GlobalProxyRecord, 0)
	for rows.Next() {
		var record GlobalProxyRecord
		if err := rows.Scan(&record.ID, &record.RawURI, &record.CanonicalIdentity,
			&record.EndpointFingerprint, &record.Name, &record.Type, &record.Disabled, &record.Pinned,
			&record.CooldownUntil, &record.LastTestOK, &record.LastTestMS, &record.LastTestAt,
			&record.LastTestError, &record.ConsecutiveFailures); err != nil {
			return nil, err
		}
		record.GlobalProxyHealth.GlobalProxyID = record.ID
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	byID := make(map[string]int, len(records))
	for index := range records {
		byID[records[index].ID] = index
	}
	sourceRows, err := r.db.QueryContext(ctx, `SELECT global_proxy_id, source_type, source_id
		FROM global_proxy_sources ORDER BY global_proxy_id, source_type, source_id`)
	if err != nil {
		return nil, fmt.Errorf("list global proxy sources: %w", err)
	}
	defer sourceRows.Close()
	for sourceRows.Next() {
		var source GlobalProxySource
		if err := sourceRows.Scan(&source.GlobalProxyID, &source.SourceType, &source.SourceID); err != nil {
			return nil, err
		}
		if index, ok := byID[source.GlobalProxyID]; ok {
			records[index].Sources = append(records[index].Sources, source)
		}
	}
	return records, sourceRows.Err()
}

func (r *SQLite) UpsertGlobalProxy(
	ctx context.Context,
	proxy GlobalProxy,
	source GlobalProxySource,
	health GlobalProxyHealth,
) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existingIdentity string
	err = tx.QueryRowContext(ctx, "SELECT canonical_identity FROM global_proxies WHERE id=?", proxy.ID).Scan(&existingIdentity)
	if err == nil && existingIdentity != proxy.CanonicalIdentity {
		return fmt.Errorf("global proxy id cannot change canonical identity")
	}
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	var identityOwner string
	err = tx.QueryRowContext(ctx, "SELECT id FROM global_proxies WHERE canonical_identity=?", proxy.CanonicalIdentity).Scan(&identityOwner)
	if err == nil && identityOwner != proxy.ID {
		return fmt.Errorf("global proxy canonical identity already belongs to another id")
	}
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO global_proxies
		(id, raw_uri, canonical_identity, endpoint_fingerprint, name, type, disabled, pinned)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET raw_uri=excluded.raw_uri,
		canonical_identity=excluded.canonical_identity, endpoint_fingerprint=excluded.endpoint_fingerprint,
		name=excluded.name, type=excluded.type, disabled=excluded.disabled,
		pinned=global_proxies.pinned OR excluded.pinned`,
		proxy.ID, proxy.RawURI, proxy.CanonicalIdentity, proxy.EndpointFingerprint,
		proxy.Name, proxy.Type, proxy.Disabled, proxy.Pinned,
	); err != nil {
		return fmt.Errorf("upsert global proxy: %w", err)
	}
	if source.SourceType != "" {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO global_proxy_sources
			(global_proxy_id, source_type, source_id) VALUES (?, ?, ?)`,
			proxy.ID, source.SourceType, source.SourceID,
		); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO global_proxy_health
		(global_proxy_id, cooldown_until, last_test_ok, last_test_ms, last_test_at,
		 last_test_error, consecutive_failures) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(global_proxy_id) DO NOTHING`,
		proxy.ID, health.CooldownUntil, health.LastTestOK, health.LastTestMS,
		health.LastTestAt, health.LastTestError, health.ConsecutiveFailures,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *SQLite) DeleteGlobalProxy(ctx context.Context, canonicalIdentity string) (GlobalProxyRecord, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return GlobalProxyRecord{}, err
	}
	defer tx.Rollback()
	var record GlobalProxyRecord
	err = tx.QueryRowContext(ctx, `SELECT p.id, p.raw_uri, p.canonical_identity,
		p.endpoint_fingerprint, p.name, p.type, p.disabled, p.pinned,
		COALESCE(h.cooldown_until,0), COALESCE(h.last_test_ok,0), COALESCE(h.last_test_ms,0),
		COALESCE(h.last_test_at,0), COALESCE(h.last_test_error,''), COALESCE(h.consecutive_failures,0)
		FROM global_proxies p LEFT JOIN global_proxy_health h ON h.global_proxy_id=p.id
		WHERE p.canonical_identity=?`, canonicalIdentity).Scan(
		&record.ID, &record.RawURI, &record.CanonicalIdentity, &record.EndpointFingerprint,
		&record.Name, &record.Type, &record.Disabled, &record.Pinned, &record.CooldownUntil,
		&record.LastTestOK, &record.LastTestMS, &record.LastTestAt, &record.LastTestError,
		&record.ConsecutiveFailures,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return GlobalProxyRecord{}, err
		}
		return GlobalProxyRecord{}, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM global_proxies WHERE id=?", record.ID); err != nil {
		return GlobalProxyRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return GlobalProxyRecord{}, err
	}
	return record, nil
}

func (r *SQLite) DeleteDisabledGlobalProxies(ctx context.Context) ([]string, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT raw_uri FROM global_proxies WHERE disabled=1 ORDER BY rowid")
	if err != nil {
		return nil, err
	}
	removed := make([]string, 0)
	for rows.Next() {
		var rawURI string
		if err := rows.Scan(&rawURI); err != nil {
			_ = rows.Close()
			return nil, err
		}
		removed = append(removed, rawURI)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM global_proxies WHERE disabled=1"); err != nil {
		return nil, err
	}
	return removed, tx.Commit()
}

func (r *SQLite) SetGlobalProxyDisabled(ctx context.Context, canonicalIdentity string, disabled bool) error {
	result, err := r.db.ExecContext(ctx,
		"UPDATE global_proxies SET disabled=? WHERE canonical_identity=?", disabled, canonicalIdentity,
	)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *SQLite) SetGlobalProxyPinned(ctx context.Context, canonicalIdentity string, pinned bool) error {
	result, err := r.db.ExecContext(ctx,
		"UPDATE global_proxies SET pinned=? WHERE canonical_identity=?", pinned, canonicalIdentity,
	)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *SQLite) UpdateGlobalProxyHealth(ctx context.Context, health GlobalProxyHealth, disabled bool) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE global_proxies SET disabled=? WHERE id=?", disabled, health.GlobalProxyID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO global_proxy_health
		(global_proxy_id, cooldown_until, last_test_ok, last_test_ms, last_test_at,
		 last_test_error, consecutive_failures) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(global_proxy_id) DO UPDATE SET cooldown_until=excluded.cooldown_until,
		last_test_ok=excluded.last_test_ok, last_test_ms=excluded.last_test_ms,
		last_test_at=excluded.last_test_at, last_test_error=excluded.last_test_error,
		consecutive_failures=excluded.consecutive_failures`,
		health.GlobalProxyID, health.CooldownUntil, health.LastTestOK, health.LastTestMS,
		health.LastTestAt, health.LastTestError, health.ConsecutiveFailures,
	); err != nil {
		return err
	}
	return tx.Commit()
}
