package legacy

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/repository"
	_ "github.com/glebarez/go-sqlite"
)

func (b *Builder) readDatabase(ctx context.Context, path string) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect legacy database: %w", err)
	}
	database, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		return fmt.Errorf("open legacy database: %w", err)
	}
	defer database.Close()
	if _, err := database.ExecContext(ctx, "PRAGMA query_only=ON; PRAGMA foreign_keys=ON;"); err != nil {
		return fmt.Errorf("protect legacy database connection: %w", err)
	}
	if current, err := schemaVersion(ctx, database); err != nil {
		return err
	} else if current >= repository.CurrentSchemaVersion {
		return nil
	}
	if err := b.readDatabaseNodes(ctx, database); err != nil {
		return err
	}
	if err := b.readDatabaseHealth(ctx, database); err != nil {
		return err
	}
	if err := b.readDatabaseGlobalProxies(ctx, database); err != nil {
		return err
	}
	return b.readDatabaseSubscriptions(ctx, database)
}

func schemaVersion(ctx context.Context, database *sql.DB) (int, error) {
	if ok, err := tableExists(ctx, database, "schema_migrations"); err != nil || !ok {
		return 0, err
	}
	var version int
	if err := database.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version); err != nil {
		return 0, fmt.Errorf("read legacy database schema version: %w", err)
	}
	return version, nil
}

func tableExists(ctx context.Context, database *sql.DB, table string) (bool, error) {
	var count int
	if err := database.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table,
	).Scan(&count); err != nil {
		return false, fmt.Errorf("inspect legacy database table %s: %w", table, err)
	}
	return count > 0, nil
}

func tableColumns(ctx context.Context, database *sql.DB, table string) (map[string]bool, error) {
	rows, err := database.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return nil, fmt.Errorf("inspect legacy database columns for %s: %w", table, err)
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, fmt.Errorf("scan legacy database columns for %s: %w", table, err)
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

func columnOr(columns map[string]bool, name, fallback string) string {
	if columns[name] {
		return name
	}
	return fallback + " AS " + name
}

func (b *Builder) readDatabaseNodes(ctx context.Context, database *sql.DB) error {
	if ok, err := tableExists(ctx, database, "nodes"); err != nil || !ok {
		return err
	}
	columns, err := tableColumns(ctx, database, "nodes")
	if err != nil {
		return err
	}
	query := "SELECT raw_uri, type, name, disabled, " + columnOr(columns, "source", "''") + " FROM nodes"
	rows, err := database.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("read legacy database nodes: %w", err)
	}
	for rows.Next() {
		var rawURI, nodeType, name, source string
		var disabled bool
		if err := rows.Scan(&rawURI, &nodeType, &name, &disabled, &source); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan legacy database node: %w", err)
		}
		sourceType, sourceID := "legacy_database", ""
		if source != "" {
			sourceType, sourceID = "subscription", source
		}
		if err := b.addNode(rawURI, nodeType, name, disabled, sourceType, sourceID); err != nil {
			_ = rows.Close()
			return err
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close legacy database nodes: %w", err)
	}
	if ok, err := tableExists(ctx, database, "node_sources"); err != nil || !ok {
		return err
	}
	rows, err = database.QueryContext(ctx, "SELECT raw_uri, source_type, source_id FROM node_sources")
	if err != nil {
		return fmt.Errorf("read legacy database node sources: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var rawURI, sourceType, sourceID string
		if err := rows.Scan(&rawURI, &sourceType, &sourceID); err != nil {
			return fmt.Errorf("scan legacy database node source: %w", err)
		}
		nodeID := b.nodeIDByRawURI[strings.TrimSpace(rawURI)]
		if nodeID == "" {
			return fmt.Errorf("legacy database node source references unknown node")
		}
		source := repository.NodeSource{NodeID: nodeID, SourceType: sourceType, SourceID: sourceID}
		b.NodeSources[nodeID+"\x00"+sourceType+"\x00"+sourceID] = source
	}
	return rows.Err()
}

func (b *Builder) readDatabaseHealth(ctx context.Context, database *sql.DB) error {
	if ok, err := tableExists(ctx, database, "node_health"); err != nil || !ok {
		return err
	}
	columns, err := tableColumns(ctx, database, "node_health")
	if err != nil {
		return err
	}
	names := []string{"success_count", "fail_count", "consecutive_failures", "last_test_ms", "last_test_error", "last_success_at", "last_fail_at", "cooldown_until", "last_429_at", "rate_limit_count", "last_sub_healthy_at"}
	parts := []string{"raw_uri"}
	for _, name := range names {
		fallback := "0"
		if name == "last_test_error" {
			fallback = "''"
		}
		parts = append(parts, columnOr(columns, name, fallback))
	}
	rows, err := database.QueryContext(ctx, "SELECT "+strings.Join(parts, ", ")+" FROM node_health")
	if err != nil {
		return fmt.Errorf("read legacy database node health: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var rawURI string
		var health repository.NodeHealth
		if err := rows.Scan(&rawURI, &health.SuccessCount, &health.FailCount, &health.ConsecutiveFailures,
			&health.LastTestMS, &health.LastTestError, &health.LastSuccessAt, &health.LastFailAt,
			&health.CooldownUntil, &health.Last429At, &health.RateLimitCount, &health.LastSubHealthyAt,
		); err != nil {
			return fmt.Errorf("scan legacy database node health: %w", err)
		}
		health.NodeID = b.nodeIDByRawURI[strings.TrimSpace(rawURI)]
		if health.NodeID == "" {
			return fmt.Errorf("legacy database health references unknown node")
		}
		b.NodeHealth[health.NodeID] = health
	}
	return rows.Err()
}

func (b *Builder) readDatabaseGlobalProxies(ctx context.Context, database *sql.DB) error {
	if ok, err := tableExists(ctx, database, "entry_proxy_candidates"); err != nil || !ok {
		return err
	}
	columns, err := tableColumns(ctx, database, "entry_proxy_candidates")
	if err != nil {
		return err
	}
	query := "SELECT raw_uri, name, type, disabled, cooldown_until, last_test_ok, last_test_ms, last_test_at, last_test_error, " + columnOr(columns, "consecutive_failures", "0") + " FROM entry_proxy_candidates"
	rows, err := database.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("read legacy database global proxies: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var rawURI, name, proxyType, lastError string
		var disabled, lastOK bool
		var cooldown, lastAt int64
		var lastMS float64
		var failures int
		if err := rows.Scan(&rawURI, &name, &proxyType, &disabled, &cooldown, &lastOK, &lastMS, &lastAt, &lastError, &failures); err != nil {
			return fmt.Errorf("scan legacy database global proxy: %w", err)
		}
		if err := b.addGlobalProxy(rawURI, name, proxyType, disabled, false, "legacy_database", "entry_proxy_candidates"); err != nil {
			return err
		}
		id := b.proxyIDByRawURI[strings.TrimSpace(rawURI)]
		b.GlobalProxyHealth[id] = repository.GlobalProxyHealth{
			GlobalProxyID: id, CooldownUntil: cooldown, LastTestOK: lastOK, LastTestMS: lastMS,
			LastTestAt: lastAt, LastTestError: lastError, ConsecutiveFailures: failures,
		}
	}
	return rows.Err()
}

func (b *Builder) readDatabaseSubscriptions(ctx context.Context, database *sql.DB) error {
	uaTable := "custom_uas"
	if ok, err := tableExists(ctx, database, uaTable); err != nil {
		return err
	} else if ok {
		rows, err := database.QueryContext(ctx, "SELECT id, name, user_agent FROM "+uaTable)
		if err != nil {
			return fmt.Errorf("read legacy database custom UAs: %w", err)
		}
		for rows.Next() {
			var ua repository.SubscriptionUserAgent
			if err := rows.Scan(&ua.ID, &ua.Name, &ua.UserAgent); err != nil {
				_ = rows.Close()
				return fmt.Errorf("scan legacy database custom UA: %w", err)
			}
			b.UserAgents[ua.ID] = ua
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	if ok, err := tableExists(ctx, database, "subscriptions"); err != nil || !ok {
		return err
	}
	rows, err := database.QueryContext(ctx, `SELECT id, name, url, user_agent, custom_ua_id,
		update_interval, adopt_manual, last_update_time, last_error, revision, generation FROM subscriptions`)
	if err != nil {
		return fmt.Errorf("read legacy database subscriptions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var subscription repository.Subscription
		if err := rows.Scan(&subscription.ID, &subscription.Name, &subscription.URL, &subscription.UserAgent,
			&subscription.CustomUAID, &subscription.UpdateInterval, &subscription.AdoptManual,
			&subscription.LastUpdateTime, &subscription.LastError, &subscription.Revision, &subscription.Generation,
		); err != nil {
			return fmt.Errorf("scan legacy database subscription: %w", err)
		}
		if subscription.CustomUAID != "" {
			if _, ok := b.UserAgents[subscription.CustomUAID]; !ok {
				return fmt.Errorf("legacy database subscription references unknown custom UA")
			}
		}
		b.Subscriptions[subscription.ID] = subscription
	}
	return rows.Err()
}
