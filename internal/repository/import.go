package repository

import (
	"context"
	"fmt"
)

func (r *SQLite) ImportSnapshot(ctx context.Context, snapshot Snapshot) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin snapshot import: %w", err)
	}
	defer tx.Rollback()
	for _, node := range snapshot.Nodes {
		if node.ID == "" || node.RawURI == "" || node.CanonicalIdentity == "" || node.EndpointFingerprint == "" {
			return fmt.Errorf("invalid request node identity")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO nodes
			(id, raw_uri, canonical_identity, endpoint_fingerprint, type, name, disabled)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET raw_uri=excluded.raw_uri,
			canonical_identity=excluded.canonical_identity, endpoint_fingerprint=excluded.endpoint_fingerprint,
			type=excluded.type, name=excluded.name, disabled=excluded.disabled`,
			node.ID, node.RawURI, node.CanonicalIdentity, node.EndpointFingerprint, node.Type, node.Name, node.Disabled,
		); err != nil {
			return fmt.Errorf("import request node %s: %w", node.ID, err)
		}
	}
	for _, source := range snapshot.NodeSources {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO node_sources
			(node_id, source_type, source_id) VALUES (?, ?, ?)`,
			source.NodeID, source.SourceType, source.SourceID,
		); err != nil {
			return fmt.Errorf("import request node source: %w", err)
		}
	}
	for _, health := range snapshot.NodeHealth {
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
			health.NodeID, health.SuccessCount, health.FailCount, health.ConsecutiveFailures,
			health.LastTestMS, health.LastTestError, health.LastSuccessAt, health.LastFailAt,
			health.CooldownUntil, health.Last429At, health.RateLimitCount, health.LastSubHealthyAt,
		); err != nil {
			return fmt.Errorf("import request node health: %w", err)
		}
	}
	for _, proxy := range snapshot.GlobalProxies {
		if proxy.ID == "" || proxy.RawURI == "" || proxy.CanonicalIdentity == "" || proxy.EndpointFingerprint == "" {
			return fmt.Errorf("invalid global proxy identity")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO global_proxies
			(id, raw_uri, canonical_identity, endpoint_fingerprint, name, type, disabled, pinned)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET raw_uri=excluded.raw_uri,
			canonical_identity=excluded.canonical_identity, endpoint_fingerprint=excluded.endpoint_fingerprint,
			name=excluded.name, type=excluded.type, disabled=excluded.disabled, pinned=excluded.pinned`,
			proxy.ID, proxy.RawURI, proxy.CanonicalIdentity, proxy.EndpointFingerprint,
			proxy.Name, proxy.Type, proxy.Disabled, proxy.Pinned,
		); err != nil {
			return fmt.Errorf("import global proxy %s: %w", proxy.ID, err)
		}
	}
	for _, source := range snapshot.GlobalProxySources {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO global_proxy_sources
			(global_proxy_id, source_type, source_id) VALUES (?, ?, ?)`,
			source.GlobalProxyID, source.SourceType, source.SourceID,
		); err != nil {
			return fmt.Errorf("import global proxy source: %w", err)
		}
	}
	for _, health := range snapshot.GlobalProxyHealth {
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
			return fmt.Errorf("import global proxy health: %w", err)
		}
	}
	for _, ua := range snapshot.UserAgents {
		if _, err := tx.ExecContext(ctx, `INSERT INTO subscription_user_agents
			(id, name, user_agent) VALUES (?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET name=excluded.name, user_agent=excluded.user_agent`,
			ua.ID, ua.Name, ua.UserAgent,
		); err != nil {
			return fmt.Errorf("import subscription user agent: %w", err)
		}
	}
	for _, subscription := range snapshot.Subscriptions {
		if _, err := tx.ExecContext(ctx, `INSERT INTO subscriptions
			(id, name, url, user_agent, custom_ua_id, update_interval, adopt_manual,
			 last_update_time, last_error, revision, generation)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET name=excluded.name, url=excluded.url,
			user_agent=excluded.user_agent, custom_ua_id=excluded.custom_ua_id,
			update_interval=excluded.update_interval, adopt_manual=excluded.adopt_manual,
			last_update_time=excluded.last_update_time, last_error=excluded.last_error,
			revision=excluded.revision, generation=excluded.generation`,
			subscription.ID, subscription.Name, subscription.URL, subscription.UserAgent,
			nullableString(subscription.CustomUAID), subscription.UpdateInterval, subscription.AdoptManual,
			subscription.LastUpdateTime, subscription.LastError, subscription.Revision, subscription.Generation,
		); err != nil {
			return fmt.Errorf("import subscription %s: %w", subscription.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit snapshot import: %w", err)
	}
	return nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
