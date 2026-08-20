package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"

	"github.com/bsfdsagfadg/vertex/internal/domain"
	"github.com/jmoiron/sqlx"
)

// SQLiteNodeRepository implements NodeRepository on SQLite.
type SQLiteNodeRepository struct {
	db *sqlx.DB
	mu sync.RWMutex
}

// NewSQLiteNodeRepository constructs a new SQLiteNodeRepository.
func NewSQLiteNodeRepository(db *sqlx.DB) *SQLiteNodeRepository {
	return &SQLiteNodeRepository{
		db: db,
	}
}

func (r *SQLiteNodeRepository) GetAll(ctx context.Context) ([]domain.Node, error) {
	var nodes []domain.Node
	err := r.db.SelectContext(ctx, &nodes, "SELECT raw_uri, type, name, disabled FROM nodes ORDER BY rowid")
	if err != nil {
		return nil, fmt.Errorf("fetch nodes: %w", err)
	}
	return nodes, nil
}

func (r *SQLiteNodeRepository) GetByURI(ctx context.Context, rawURI string) (*domain.Node, error) {
	var node domain.Node
	err := r.db.GetContext(ctx, &node, "SELECT raw_uri, type, name, disabled FROM nodes WHERE raw_uri = ?", rawURI)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("fetch node by uri: %w", err)
	}
	return &node, nil
}

func (r *SQLiteNodeRepository) GetSources(ctx context.Context, rawURI string) ([]domain.NodeSource, error) {
	var sources []domain.NodeSource
	err := r.db.SelectContext(ctx, &sources, "SELECT source_type, source_id FROM node_sources WHERE raw_uri = ? ORDER BY source_type, source_id", rawURI)
	if err != nil {
		return nil, fmt.Errorf("fetch node sources: %w", err)
	}
	return sources, nil
}

func (r *SQLiteNodeRepository) GetAllSources(ctx context.Context) (map[string][]domain.NodeSource, error) {
	type row struct {
		RawURI     string `db:"raw_uri"`
		SourceType string `db:"source_type"`
		SourceID   string `db:"source_id"`
	}
	var rows []row
	err := r.db.SelectContext(ctx, &rows, "SELECT raw_uri, source_type, source_id FROM node_sources")
	if err != nil {
		return nil, fmt.Errorf("fetch all node sources: %w", err)
	}
	result := make(map[string][]domain.NodeSource, len(rows))
	for _, rw := range rows {
		result[rw.RawURI] = append(result[rw.RawURI], domain.NodeSource{
			Type: rw.SourceType,
			ID:   rw.SourceID,
		})
	}
	return result, nil
}

func (r *SQLiteNodeRepository) UpsertNodesWithSource(ctx context.Context, newNodes []domain.Node, source domain.NodeSource, adoptManual bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	upsertNodeStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO nodes (raw_uri, type, name, disabled)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(raw_uri) DO UPDATE SET
			type = excluded.type,
			name = excluded.name,
			disabled = excluded.disabled
	`)
	if err != nil {
		return fmt.Errorf("prepare upsert node: %w", err)
	}
	defer upsertNodeStmt.Close()

	upsertSourceStmt, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO node_sources (raw_uri, source_type, source_id)
		VALUES (?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare insert source: %w", err)
	}
	defer upsertSourceStmt.Close()

	for _, n := range newNodes {
		uri := strings.TrimSpace(n.RawURI)
		if uri == "" {
			continue
		}
		if _, err := upsertNodeStmt.ExecContext(ctx, uri, n.Type, n.Name, n.Disabled); err != nil {
			return fmt.Errorf("upsert node %s: %w", uri, err)
		}
		if _, err := upsertSourceStmt.ExecContext(ctx, uri, source.Type, source.ID); err != nil {
			return fmt.Errorf("upsert source for %s: %w", uri, err)
		}

		if source.Type == domain.SourceSubscription {
			if adoptManual {
				if _, err := tx.ExecContext(ctx, "DELETE FROM node_sources WHERE raw_uri = ? AND source_type = ?", uri, domain.SourceManual); err != nil {
					return fmt.Errorf("adopt manual delete: %w", err)
				}
			}
			// Reconcile legacy source
			if _, err := tx.ExecContext(ctx, "DELETE FROM node_sources WHERE raw_uri = ? AND source_type = ?", uri, domain.SourceLegacy); err != nil {
				return fmt.Errorf("reconcile legacy source: %w", err)
			}
		}
	}

	return tx.Commit()
}

func (r *SQLiteNodeRepository) ReplaceSubscriptionNodes(ctx context.Context, subscriptionID string, newNodes []domain.Node, adoptManual bool) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	// 1. Get old URIs owned by this subscription
	var oldURIs []string
	err = tx.SelectContext(ctx, &oldURIs, "SELECT raw_uri FROM node_sources WHERE source_type = ? AND source_id = ?", domain.SourceSubscription, subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("query existing sub nodes: %w", err)
	}

	newURIMap := make(map[string]bool, len(newNodes))
	for _, n := range newNodes {
		if strings.TrimSpace(n.RawURI) != "" {
			newURIMap[n.RawURI] = true
		}
	}

	// 2. Remove subscription source link for nodes no longer in the subscription
	for _, oldURI := range oldURIs {
		if !newURIMap[oldURI] {
			if _, err := tx.ExecContext(ctx, "DELETE FROM node_sources WHERE raw_uri = ? AND source_type = ? AND source_id = ?", oldURI, domain.SourceSubscription, subscriptionID); err != nil {
				return nil, fmt.Errorf("delete old sub source: %w", err)
			}
		}
	}

	// 3. Upsert incoming nodes
	upsertNodeStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO nodes (raw_uri, type, name, disabled)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(raw_uri) DO UPDATE SET
			type = excluded.type,
			name = excluded.name,
			disabled = excluded.disabled
	`)
	if err != nil {
		return nil, fmt.Errorf("prepare upsert node: %w", err)
	}
	defer upsertNodeStmt.Close()

	upsertSourceStmt, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO node_sources (raw_uri, source_type, source_id)
		VALUES (?, ?, ?)
	`)
	if err != nil {
		return nil, fmt.Errorf("prepare insert source: %w", err)
	}
	defer upsertSourceStmt.Close()

	for _, n := range newNodes {
		uri := strings.TrimSpace(n.RawURI)
		if uri == "" {
			continue
		}
		if _, err := upsertNodeStmt.ExecContext(ctx, uri, n.Type, n.Name, n.Disabled); err != nil {
			return nil, fmt.Errorf("upsert node %s: %w", uri, err)
		}
		if _, err := upsertSourceStmt.ExecContext(ctx, uri, domain.SourceSubscription, subscriptionID); err != nil {
			return nil, fmt.Errorf("upsert source for %s: %w", uri, err)
		}
		if adoptManual {
			if _, err := tx.ExecContext(ctx, "DELETE FROM node_sources WHERE raw_uri = ? AND source_type = ?", uri, domain.SourceManual); err != nil {
				return nil, fmt.Errorf("adopt manual delete: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM node_sources WHERE raw_uri = ? AND source_type = ?", uri, domain.SourceLegacy); err != nil {
			return nil, fmt.Errorf("reconcile legacy source: %w", err)
		}
	}

	// 4. Find all nodes with 0 sources and delete them
	var orphanedURIs []string
	err = tx.SelectContext(ctx, &orphanedURIs, `
		SELECT n.raw_uri FROM nodes n
		WHERE NOT EXISTS (SELECT 1 FROM node_sources s WHERE s.raw_uri = n.raw_uri)
	`)
	if err != nil {
		return nil, fmt.Errorf("find orphaned nodes: %w", err)
	}

	if len(orphanedURIs) > 0 {
		for _, oURI := range orphanedURIs {
			if _, err := tx.ExecContext(ctx, "DELETE FROM node_health WHERE raw_uri = ?", oURI); err != nil {
				return nil, fmt.Errorf("delete orphaned health: %w", err)
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM nodes WHERE raw_uri = ?", oURI); err != nil {
				return nil, fmt.Errorf("delete orphaned node: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit replace subscription nodes: %w", err)
	}

	return orphanedURIs, nil
}

func (r *SQLiteNodeRepository) DeleteByURI(ctx context.Context, rawURI string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, "DELETE FROM node_sources WHERE raw_uri = ?", rawURI); err != nil {
		return fmt.Errorf("delete node sources: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM node_health WHERE raw_uri = ?", rawURI); err != nil {
		return fmt.Errorf("delete node health: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM nodes WHERE raw_uri = ?", rawURI); err != nil {
		return fmt.Errorf("delete node: %w", err)
	}

	return tx.Commit()
}

func (r *SQLiteNodeRepository) DeleteDisabled(ctx context.Context) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var disabledURIs []string
	if err := tx.SelectContext(ctx, &disabledURIs, "SELECT raw_uri FROM nodes WHERE disabled = 1"); err != nil {
		return nil, fmt.Errorf("select disabled nodes: %w", err)
	}

	if len(disabledURIs) == 0 {
		return nil, nil
	}

	for _, uri := range disabledURIs {
		if _, err := tx.ExecContext(ctx, "DELETE FROM node_sources WHERE raw_uri = ?", uri); err != nil {
			return nil, fmt.Errorf("delete sources: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM node_health WHERE raw_uri = ?", uri); err != nil {
			return nil, fmt.Errorf("delete health: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM nodes WHERE raw_uri = ?", uri); err != nil {
			return nil, fmt.Errorf("delete node: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit delete disabled: %w", err)
	}

	return disabledURIs, nil
}

func (r *SQLiteNodeRepository) BatchDelete(ctx context.Context, uris []string) error {
	if len(uris) == 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	for _, uri := range uris {
		if _, err := tx.ExecContext(ctx, "DELETE FROM node_sources WHERE raw_uri = ?", uri); err != nil {
			return fmt.Errorf("delete sources for %s: %w", uri, err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM node_health WHERE raw_uri = ?", uri); err != nil {
			return fmt.Errorf("delete health for %s: %w", uri, err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM nodes WHERE raw_uri = ?", uri); err != nil {
			return fmt.Errorf("delete node %s: %w", uri, err)
		}
	}

	return tx.Commit()
}

func (r *SQLiteNodeRepository) BatchSetDisabled(ctx context.Context, uris []string, disabled bool) error {
	if len(uris) == 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	stmt, err := tx.PrepareContext(ctx, "UPDATE nodes SET disabled = ? WHERE raw_uri = ?")
	if err != nil {
		return fmt.Errorf("prepare update disabled: %w", err)
	}
	defer stmt.Close()

	for _, uri := range uris {
		if _, err := stmt.ExecContext(ctx, disabled, uri); err != nil {
			return fmt.Errorf("update disabled for %s: %w", uri, err)
		}
	}

	return tx.Commit()
}

func (r *SQLiteNodeRepository) SetDisabled(ctx context.Context, rawURI string, disabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	_, err := r.db.ExecContext(ctx, "UPDATE nodes SET disabled = ? WHERE raw_uri = ?", disabled, rawURI)
	if err != nil {
		return fmt.Errorf("set disabled: %w", err)
	}
	return nil
}

func (r *SQLiteNodeRepository) Dedup(ctx context.Context) (domain.DedupPreview, error) {
	// Implemented as part of store deduplication
	return domain.DedupPreview{}, nil
}
