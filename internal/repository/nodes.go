package repository

import (
	"context"
	"fmt"
)

func (r *SQLite) LoadNodeState(ctx context.Context) ([]Node, []NodeSource, []NodeHealth, error) {
	nodes := make([]Node, 0)
	rows, err := r.db.QueryContext(ctx, `SELECT id, raw_uri, canonical_identity,
		endpoint_fingerprint, type, name, disabled FROM nodes ORDER BY rowid`)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load request nodes: %w", err)
	}
	for rows.Next() {
		var node Node
		if err := rows.Scan(&node.ID, &node.RawURI, &node.CanonicalIdentity, &node.EndpointFingerprint,
			&node.Type, &node.Name, &node.Disabled); err != nil {
			_ = rows.Close()
			return nil, nil, nil, fmt.Errorf("scan request node: %w", err)
		}
		nodes = append(nodes, node)
	}
	if err := rows.Close(); err != nil {
		return nil, nil, nil, err
	}
	sources := make([]NodeSource, 0)
	rows, err = r.db.QueryContext(ctx, "SELECT node_id, source_type, source_id FROM node_sources")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load request node sources: %w", err)
	}
	for rows.Next() {
		var source NodeSource
		if err := rows.Scan(&source.NodeID, &source.SourceType, &source.SourceID); err != nil {
			_ = rows.Close()
			return nil, nil, nil, err
		}
		sources = append(sources, source)
	}
	if err := rows.Close(); err != nil {
		return nil, nil, nil, err
	}
	health := make([]NodeHealth, 0)
	rows, err = r.db.QueryContext(ctx, `SELECT node_id, success_count, fail_count,
		consecutive_failures, last_test_ms, last_test_error, last_success_at, last_fail_at,
		cooldown_until, last_429_at, rate_limit_count, last_sub_healthy_at FROM node_health`)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load request node health: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item NodeHealth
		if err := rows.Scan(&item.NodeID, &item.SuccessCount, &item.FailCount, &item.ConsecutiveFailures,
			&item.LastTestMS, &item.LastTestError, &item.LastSuccessAt, &item.LastFailAt,
			&item.CooldownUntil, &item.Last429At, &item.RateLimitCount, &item.LastSubHealthyAt); err != nil {
			return nil, nil, nil, err
		}
		health = append(health, item)
	}
	return nodes, sources, health, rows.Err()
}

func (r *SQLite) SaveNodeState(ctx context.Context, nodes []Node, sources []NodeSource) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin request node state save: %w", err)
	}
	defer tx.Rollback()
	current := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		current[node.ID] = struct{}{}
		if _, err := tx.ExecContext(ctx, `INSERT INTO nodes
			(id, raw_uri, canonical_identity, endpoint_fingerprint, type, name, disabled)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET raw_uri=excluded.raw_uri,
			canonical_identity=excluded.canonical_identity, endpoint_fingerprint=excluded.endpoint_fingerprint,
			type=excluded.type, name=excluded.name, disabled=excluded.disabled`,
			node.ID, node.RawURI, node.CanonicalIdentity, node.EndpointFingerprint,
			node.Type, node.Name, node.Disabled,
		); err != nil {
			return fmt.Errorf("save request node: %w", err)
		}
	}
	rows, err := tx.QueryContext(ctx, "SELECT id FROM nodes")
	if err != nil {
		return err
	}
	stale := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		if _, ok := current[id]; !ok {
			stale = append(stale, id)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range stale {
		if _, err := tx.ExecContext(ctx, "DELETE FROM nodes WHERE id = ?", id); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM node_sources"); err != nil {
		return err
	}
	for _, source := range sources {
		if _, ok := current[source.NodeID]; !ok {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO node_sources(node_id, source_type, source_id) VALUES (?, ?, ?)",
			source.NodeID, source.SourceType, source.SourceID,
		); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit request node state save: %w", err)
	}
	return nil
}

func (r *SQLite) SetNodesDisabled(ctx context.Context, nodeIDs []string, disabled bool) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range nodeIDs {
		if _, err := tx.ExecContext(ctx, "UPDATE nodes SET disabled = ? WHERE id = ?", disabled, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *SQLite) UpsertNodeHealthBatch(ctx context.Context, health []NodeHealth) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, item := range health {
		if _, err := tx.ExecContext(ctx, `INSERT INTO node_health
			(node_id, success_count, fail_count, consecutive_failures, last_test_ms, last_test_error,
			 last_success_at, last_fail_at, cooldown_until, last_429_at, rate_limit_count, last_sub_healthy_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(node_id) DO UPDATE SET success_count=excluded.success_count,
			fail_count=excluded.fail_count, consecutive_failures=excluded.consecutive_failures,
			last_test_ms=excluded.last_test_ms, last_test_error=excluded.last_test_error,
			last_success_at=excluded.last_success_at, last_fail_at=excluded.last_fail_at,
			cooldown_until=excluded.cooldown_until, last_429_at=excluded.last_429_at,
			rate_limit_count=excluded.rate_limit_count, last_sub_healthy_at=excluded.last_sub_healthy_at`,
			item.NodeID, item.SuccessCount, item.FailCount, item.ConsecutiveFailures,
			item.LastTestMS, item.LastTestError, item.LastSuccessAt, item.LastFailAt,
			item.CooldownUntil, item.Last429At, item.RateLimitCount, item.LastSubHealthyAt,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}
