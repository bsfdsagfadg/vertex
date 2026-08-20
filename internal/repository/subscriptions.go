package repository

import (
	"context"
	"database/sql"
	"fmt"
)

func (r *SQLite) LoadSubscriptions(ctx context.Context) ([]Subscription, []SubscriptionUserAgent, error) {
	userAgents := make([]SubscriptionUserAgent, 0)
	rows, err := r.db.QueryContext(ctx, "SELECT id, name, user_agent FROM subscription_user_agents ORDER BY rowid")
	if err != nil {
		return nil, nil, fmt.Errorf("load subscription user agents: %w", err)
	}
	for rows.Next() {
		var ua SubscriptionUserAgent
		if err := rows.Scan(&ua.ID, &ua.Name, &ua.UserAgent); err != nil {
			_ = rows.Close()
			return nil, nil, err
		}
		userAgents = append(userAgents, ua)
	}
	if err := rows.Close(); err != nil {
		return nil, nil, err
	}
	subscriptions := make([]Subscription, 0)
	rows, err = r.db.QueryContext(ctx, `SELECT id, name, url, user_agent, custom_ua_id,
		update_interval, adopt_manual, last_update_time, last_error, revision, generation
		FROM subscriptions ORDER BY rowid`)
	if err != nil {
		return nil, nil, fmt.Errorf("load subscriptions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var subscription Subscription
		var customUAID sql.NullString
		if err := rows.Scan(&subscription.ID, &subscription.Name, &subscription.URL,
			&subscription.UserAgent, &customUAID, &subscription.UpdateInterval,
			&subscription.AdoptManual, &subscription.LastUpdateTime, &subscription.LastError,
			&subscription.Revision, &subscription.Generation); err != nil {
			return nil, nil, err
		}
		if customUAID.Valid {
			subscription.CustomUAID = customUAID.String
		}
		subscriptions = append(subscriptions, subscription)
	}
	return subscriptions, userAgents, rows.Err()
}

func (r *SQLite) ReplaceSubscriptions(
	ctx context.Context,
	subscriptions []Subscription,
	userAgents []SubscriptionUserAgent,
) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM subscriptions"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM subscription_user_agents"); err != nil {
		return err
	}
	for _, ua := range userAgents {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO subscription_user_agents(id, name, user_agent) VALUES (?, ?, ?)",
			ua.ID, ua.Name, ua.UserAgent,
		); err != nil {
			return fmt.Errorf("save subscription user agent: %w", err)
		}
	}
	for _, subscription := range subscriptions {
		if _, err := tx.ExecContext(ctx, `INSERT INTO subscriptions
			(id, name, url, user_agent, custom_ua_id, update_interval, adopt_manual,
			 last_update_time, last_error, revision, generation)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			subscription.ID, subscription.Name, subscription.URL, subscription.UserAgent,
			nullableString(subscription.CustomUAID), subscription.UpdateInterval,
			subscription.AdoptManual, subscription.LastUpdateTime, subscription.LastError,
			subscription.Revision, subscription.Generation,
		); err != nil {
			return fmt.Errorf("save subscription: %w", err)
		}
	}
	return tx.Commit()
}
