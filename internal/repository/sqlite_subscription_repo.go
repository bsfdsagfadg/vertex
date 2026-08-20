package repository

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	"github.com/bsfdsagfadg/vertex/internal/domain"
	"github.com/jmoiron/sqlx"
)

// SQLiteSubscriptionRepository implements SubscriptionRepository on SQLite.
type SQLiteSubscriptionRepository struct {
	db *sqlx.DB
	mu sync.RWMutex
}

// NewSQLiteSubscriptionRepository constructs a new SQLiteSubscriptionRepository.
func NewSQLiteSubscriptionRepository(db *sqlx.DB) *SQLiteSubscriptionRepository {
	return &SQLiteSubscriptionRepository{
		db: db,
	}
}

func (r *SQLiteSubscriptionRepository) GetAll(ctx context.Context) ([]domain.Subscription, error) {
	var list []domain.Subscription
	err := r.db.SelectContext(ctx, &list, `
		SELECT id, name, url, user_agent, custom_ua_id, update_interval,
		       adopt_manual, last_update_time, last_error, revision, generation
		FROM subscriptions ORDER BY rowid
	`)
	if err != nil {
		return nil, fmt.Errorf("select subscriptions: %w", err)
	}
	return list, nil
}

func (r *SQLiteSubscriptionRepository) GetByID(ctx context.Context, id string) (*domain.Subscription, error) {
	var sub domain.Subscription
	err := r.db.GetContext(ctx, &sub, `
		SELECT id, name, url, user_agent, custom_ua_id, update_interval,
		       adopt_manual, last_update_time, last_error, revision, generation
		FROM subscriptions WHERE id = ?
	`, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("select subscription by id: %w", err)
	}
	return &sub, nil
}

func (r *SQLiteSubscriptionRepository) Save(ctx context.Context, sub domain.Subscription) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO subscriptions (
			id, name, url, user_agent, custom_ua_id, update_interval,
			adopt_manual, last_update_time, last_error, revision, generation
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			url = excluded.url,
			user_agent = excluded.user_agent,
			custom_ua_id = excluded.custom_ua_id,
			update_interval = excluded.update_interval,
			adopt_manual = excluded.adopt_manual,
			last_update_time = excluded.last_update_time,
			last_error = excluded.last_error,
			revision = excluded.revision,
			generation = excluded.generation
	`, sub.ID, sub.Name, sub.URL, sub.UserAgent, sub.CustomUAID, sub.UpdateInterval,
		sub.AdoptManual, sub.LastUpdateTime, sub.LastError, sub.Revision, sub.Generation)
	if err != nil {
		return fmt.Errorf("save subscription: %w", err)
	}
	return nil
}

func (r *SQLiteSubscriptionRepository) Delete(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	_, err := r.db.ExecContext(ctx, "DELETE FROM subscriptions WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete subscription: %w", err)
	}
	return nil
}

func (r *SQLiteSubscriptionRepository) GetAllCustomUAs(ctx context.Context) ([]domain.CustomUA, error) {
	var list []domain.CustomUA
	err := r.db.SelectContext(ctx, &list, "SELECT id, name, user_agent FROM custom_uas ORDER BY rowid")
	if err != nil {
		return nil, fmt.Errorf("select custom uas: %w", err)
	}
	return list, nil
}

func (r *SQLiteSubscriptionRepository) GetCustomUAByID(ctx context.Context, id string) (*domain.CustomUA, error) {
	var ua domain.CustomUA
	err := r.db.GetContext(ctx, &ua, "SELECT id, name, user_agent FROM custom_uas WHERE id = ?", id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("select custom ua by id: %w", err)
	}
	return &ua, nil
}

func (r *SQLiteSubscriptionRepository) SaveCustomUA(ctx context.Context, ua domain.CustomUA) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO custom_uas (id, name, user_agent)
		VALUES (?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			user_agent = excluded.user_agent
	`, ua.ID, ua.Name, ua.UserAgent)
	if err != nil {
		return fmt.Errorf("save custom ua: %w", err)
	}
	return nil
}

func (r *SQLiteSubscriptionRepository) DeleteCustomUA(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	_, err := r.db.ExecContext(ctx, "DELETE FROM custom_uas WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete custom ua: %w", err)
	}
	return nil
}
