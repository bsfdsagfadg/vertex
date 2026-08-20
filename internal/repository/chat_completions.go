package repository

import (
	"context"
	"fmt"
	"time"
)

func (r *SQLite) CreateChatCompletion(ctx context.Context, value ChatCompletionResource) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO chat_completions
		(id,model,status,request_json,response_json,messages_json,metadata_json,created_at,updated_at,expires_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`, value.ID, value.Model, value.Status, value.RequestJSON, value.ResponseJSON,
		value.MessagesJSON, jsonDefault(value.MetadataJSON, "{}"), value.CreatedAt, value.UpdatedAt, value.ExpiresAt)
	if err != nil {
		return fmt.Errorf("create chat completion resource: %w", err)
	}
	return nil
}

func (r *SQLite) GetChatCompletion(ctx context.Context, id string, now time.Time) (ChatCompletionResource, error) {
	var value ChatCompletionResource
	err := r.db.QueryRowContext(ctx, `SELECT id,model,status,request_json,response_json,messages_json,metadata_json,created_at,updated_at,expires_at
		FROM chat_completions WHERE id=? AND expires_at>?`, id, now.UTC().Unix()).Scan(&value.ID, &value.Model, &value.Status,
		&value.RequestJSON, &value.ResponseJSON, &value.MessagesJSON, &value.MetadataJSON, &value.CreatedAt, &value.UpdatedAt, &value.ExpiresAt)
	return value, err
}

func (r *SQLite) ListChatCompletions(ctx context.Context, afterCreated int64, afterID string, limit int, now time.Time) ([]ChatCompletionResource, error) {
	if limit <= 0 || limit > 101 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id,model,status,request_json,response_json,messages_json,metadata_json,created_at,updated_at,expires_at
		FROM chat_completions WHERE expires_at>? AND (created_at>? OR (created_at=? AND id>?)) ORDER BY created_at,id LIMIT ?`,
		now.UTC().Unix(), afterCreated, afterCreated, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []ChatCompletionResource
	for rows.Next() {
		var value ChatCompletionResource
		if err := rows.Scan(&value.ID, &value.Model, &value.Status, &value.RequestJSON, &value.ResponseJSON, &value.MessagesJSON,
			&value.MetadataJSON, &value.CreatedAt, &value.UpdatedAt, &value.ExpiresAt); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (r *SQLite) UpdateChatCompletionMetadata(ctx context.Context, id string, metadata []byte, updatedAt int64) error {
	result, err := r.db.ExecContext(ctx, "UPDATE chat_completions SET metadata_json=?,updated_at=? WHERE id=?", jsonDefault(metadata, "{}"), updatedAt, id)
	if err != nil {
		return err
	}
	return requireAffected(result, "chat completion")
}

func (r *SQLite) DeleteChatCompletion(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, "DELETE FROM chat_completions WHERE id=?", id)
	if err != nil {
		return err
	}
	return requireAffected(result, "chat completion")
}
