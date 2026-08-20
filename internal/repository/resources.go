package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (r *SQLite) CreateResponse(ctx context.Context, value ResponseResource) error {
	return insertResponse(ctx, r.db, value)
}

type sqlExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insertResponse(ctx context.Context, executor sqlExecutor, value ResponseResource) error {
	_, err := executor.ExecContext(ctx, `INSERT INTO responses
		(id,status,model,request_json,input_json,output_json,usage_json,error_json,incomplete_json,
		 previous_response_id,conversation_id,metadata_json,upstream_operation_id,store,background,created_at,completed_at,expires_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, value.ID, value.Status, value.Model, value.RequestJSON,
		value.InputJSON, jsonDefault(value.OutputJSON, "[]"), jsonDefault(value.UsageJSON, "{}"), jsonDefault(value.ErrorJSON, "null"),
		jsonDefault(value.IncompleteJSON, "null"), value.PreviousResponseID, value.ConversationID,
		jsonDefault(value.MetadataJSON, "{}"), value.UpstreamOperationID, value.Store, value.Background,
		value.CreatedAt, value.CompletedAt, value.ExpiresAt)
	if err != nil {
		return fmt.Errorf("create response resource: %w", err)
	}
	return nil
}

func (r *SQLite) CreateResponseIdempotent(ctx context.Context, value ResponseResource, record IdempotencyRecord) (string, bool, bool, error) {
	if record.Key == "" {
		return value.ID, false, false, r.CreateResponse(ctx, value)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, false, err
	}
	defer tx.Rollback()
	var bodyHash, resourceID string
	err = tx.QueryRowContext(ctx, `SELECT body_hash,resource_id FROM idempotency_keys
		WHERE endpoint=? AND idempotency_key=? AND expires_at>?`, record.Endpoint, record.Key, time.Now().UTC().Unix()).Scan(&bodyHash, &resourceID)
	if err == nil {
		return resourceID, true, bodyHash != record.BodyHash, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, false, err
	}
	if err := insertResponse(ctx, tx, value); err != nil {
		return "", false, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO idempotency_keys(endpoint,idempotency_key,body_hash,resource_kind,resource_id,created_at,expires_at)
		VALUES (?,?,?,?,?,?,?)`, record.Endpoint, record.Key, record.BodyHash, record.ResourceKind, record.ResourceID, record.CreatedAt, record.ExpiresAt); err != nil {
		return "", false, false, err
	}
	if err := tx.Commit(); err != nil {
		return "", false, false, err
	}
	return value.ID, false, false, nil
}

func (r *SQLite) GetResponse(ctx context.Context, id string, now time.Time) (ResponseResource, error) {
	var value ResponseResource
	err := r.db.QueryRowContext(ctx, `SELECT id,status,model,request_json,input_json,output_json,usage_json,error_json,incomplete_json,
		previous_response_id,conversation_id,metadata_json,upstream_operation_id,store,background,created_at,completed_at,expires_at
		FROM responses WHERE id=? AND expires_at>?`, id, now.UTC().Unix()).Scan(&value.ID, &value.Status, &value.Model,
		&value.RequestJSON, &value.InputJSON, &value.OutputJSON, &value.UsageJSON, &value.ErrorJSON, &value.IncompleteJSON,
		&value.PreviousResponseID, &value.ConversationID, &value.MetadataJSON, &value.UpstreamOperationID,
		&value.Store, &value.Background, &value.CreatedAt, &value.CompletedAt, &value.ExpiresAt)
	if err != nil {
		return ResponseResource{}, err
	}
	return value, nil
}

func (r *SQLite) UpdateResponse(ctx context.Context, value ResponseResource) error {
	result, err := r.db.ExecContext(ctx, `UPDATE responses SET status=?,output_json=?,usage_json=?,error_json=?,incomplete_json=?,
		metadata_json=?,upstream_operation_id=?,completed_at=?,expires_at=? WHERE id=?`, value.Status,
		jsonDefault(value.OutputJSON, "[]"), jsonDefault(value.UsageJSON, "{}"), jsonDefault(value.ErrorJSON, "null"),
		jsonDefault(value.IncompleteJSON, "null"), jsonDefault(value.MetadataJSON, "{}"), value.UpstreamOperationID,
		value.CompletedAt, value.ExpiresAt, value.ID)
	if err != nil {
		return fmt.Errorf("update response resource: %w", err)
	}
	return requireAffected(result, "response resource")
}

func (r *SQLite) CompleteResponseIfInProgress(ctx context.Context, value ResponseResource) (bool, error) {
	result, err := r.db.ExecContext(ctx, `UPDATE responses SET status=?,output_json=?,usage_json=?,error_json=?,incomplete_json=?,
		metadata_json=?,upstream_operation_id=?,completed_at=?,expires_at=? WHERE id=? AND status='in_progress'`, value.Status,
		jsonDefault(value.OutputJSON, "[]"), jsonDefault(value.UsageJSON, "{}"), jsonDefault(value.ErrorJSON, "null"),
		jsonDefault(value.IncompleteJSON, "null"), jsonDefault(value.MetadataJSON, "{}"), value.UpstreamOperationID,
		value.CompletedAt, value.ExpiresAt, value.ID)
	if err != nil {
		return false, fmt.Errorf("complete response resource: %w", err)
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (r *SQLite) CancelResponse(ctx context.Context, id string, completedAt int64) (ResponseResource, error) {
	if _, err := r.db.ExecContext(ctx, "UPDATE responses SET status='cancelled',completed_at=? WHERE id=? AND status='in_progress'", completedAt, id); err != nil {
		return ResponseResource{}, err
	}
	return r.GetResponse(ctx, id, time.Now())
}

func (r *SQLite) DeleteResponse(ctx context.Context, id string) error {
	return r.deleteResource(ctx, "response", id, "responses")
}

func (r *SQLite) CreateConversation(ctx context.Context, value Conversation, items []ConversationItem) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin conversation create: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "INSERT INTO conversations(id,metadata_json,created_at,updated_at) VALUES (?,?,?,?)",
		value.ID, jsonDefault(value.MetadataJSON, "{}"), value.CreatedAt, value.UpdatedAt); err != nil {
		return fmt.Errorf("create conversation: %w", err)
	}
	if err := insertConversationItems(ctx, tx, value.ID, items); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *SQLite) GetConversation(ctx context.Context, id string) (Conversation, error) {
	var value Conversation
	err := r.db.QueryRowContext(ctx, "SELECT id,metadata_json,created_at,updated_at FROM conversations WHERE id=?", id).Scan(
		&value.ID, &value.MetadataJSON, &value.CreatedAt, &value.UpdatedAt)
	return value, err
}

func (r *SQLite) UpdateConversation(ctx context.Context, id string, metadata []byte, updatedAt int64) error {
	result, err := r.db.ExecContext(ctx, "UPDATE conversations SET metadata_json=?,updated_at=? WHERE id=?", jsonDefault(metadata, "{}"), updatedAt, id)
	if err != nil {
		return fmt.Errorf("update conversation: %w", err)
	}
	return requireAffected(result, "conversation")
}

func (r *SQLite) DeleteConversation(ctx context.Context, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM tool_states WHERE conversation_id=?", id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE responses SET conversation_id='' WHERE conversation_id=?", id); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM conversations WHERE id=?", id)
	if err != nil {
		return fmt.Errorf("delete conversation: %w", err)
	}
	if err := requireAffected(result, "conversation"); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *SQLite) AddConversationItems(ctx context.Context, conversationID string, items []ConversationItem) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin conversation item write: %w", err)
	}
	defer tx.Rollback()
	if err := insertConversationItems(ctx, tx, conversationID, items); err != nil {
		return err
	}
	return tx.Commit()
}

func insertConversationItems(ctx context.Context, tx *sql.Tx, conversationID string, items []ConversationItem) error {
	if len(items) == 0 {
		return nil
	}
	var next int64
	if err := tx.QueryRowContext(ctx, `UPDATE conversations SET next_ordinal=next_ordinal+?,updated_at=? WHERE id=?
		RETURNING next_ordinal-?`, len(items), time.Now().UTC().Unix(), conversationID, len(items)).Scan(&next); err != nil {
		return fmt.Errorf("allocate conversation ordinals: %w", err)
	}
	for index, item := range items {
		ordinal := next + int64(index)
		if _, err := tx.ExecContext(ctx, `INSERT INTO conversation_items(id,conversation_id,ordinal,item_json,created_at) VALUES (?,?,?,?,?)`,
			item.ID, conversationID, ordinal, item.ItemJSON, item.CreatedAt); err != nil {
			return fmt.Errorf("create conversation item: %w", err)
		}
	}
	return nil
}

func (r *SQLite) ListConversationItems(ctx context.Context, conversationID string, after int64, limit int, descending bool) ([]ConversationItem, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	operator, order := ">", "ASC"
	if descending {
		operator, order = "<", "DESC"
		if after < 0 {
			after = 1<<62 - 1
		}
	}
	query := fmt.Sprintf("SELECT id,conversation_id,ordinal,item_json,created_at FROM conversation_items WHERE conversation_id=? AND ordinal %s ? ORDER BY ordinal %s LIMIT ?", operator, order)
	rows, err := r.db.QueryContext(ctx, query, conversationID, after, limit)
	if err != nil {
		return nil, fmt.Errorf("list conversation items: %w", err)
	}
	defer rows.Close()
	var result []ConversationItem
	for rows.Next() {
		var item ConversationItem
		if err := rows.Scan(&item.ID, &item.ConversationID, &item.Ordinal, &item.ItemJSON, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan conversation item: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *SQLite) GetConversationItem(ctx context.Context, conversationID, itemID string) (ConversationItem, error) {
	var item ConversationItem
	err := r.db.QueryRowContext(ctx, "SELECT id,conversation_id,ordinal,item_json,created_at FROM conversation_items WHERE conversation_id=? AND id=?", conversationID, itemID).Scan(
		&item.ID, &item.ConversationID, &item.Ordinal, &item.ItemJSON, &item.CreatedAt)
	return item, err
}

func (r *SQLite) DeleteConversationItem(ctx context.Context, conversationID, itemID string) error {
	result, err := r.db.ExecContext(ctx, "DELETE FROM conversation_items WHERE conversation_id=? AND id=?", conversationID, itemID)
	if err != nil {
		return fmt.Errorf("delete conversation item: %w", err)
	}
	return requireAffected(result, "conversation item")
}

func (r *SQLite) CreateInteraction(ctx context.Context, value InteractionResource) error {
	return insertInteraction(ctx, r.db, value)
}

func insertInteraction(ctx context.Context, executor sqlExecutor, value InteractionResource) error {
	_, err := executor.ExecContext(ctx, `INSERT INTO interactions
		(id,status,model,agent,request_json,steps_json,usage_json,error_json,previous_interaction_id,labels_json,store,background,created_at,updated_at,expires_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, value.ID, value.Status, value.Model, value.Agent, value.RequestJSON,
		jsonDefault(value.StepsJSON, "[]"), jsonDefault(value.UsageJSON, "{}"), jsonDefault(value.ErrorJSON, "null"),
		value.PreviousInteractionID, jsonDefault(value.LabelsJSON, "{}"), value.Store, value.Background,
		value.CreatedAt, value.UpdatedAt, value.ExpiresAt)
	if err != nil {
		return fmt.Errorf("create interaction resource: %w", err)
	}
	return nil
}

func (r *SQLite) CreateInteractionIdempotent(ctx context.Context, value InteractionResource, record IdempotencyRecord) (string, bool, bool, error) {
	if record.Key == "" {
		return value.ID, false, false, r.CreateInteraction(ctx, value)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, false, err
	}
	defer tx.Rollback()
	var bodyHash, resourceID string
	err = tx.QueryRowContext(ctx, `SELECT body_hash,resource_id FROM idempotency_keys
		WHERE endpoint=? AND idempotency_key=? AND expires_at>?`, record.Endpoint, record.Key, time.Now().UTC().Unix()).Scan(&bodyHash, &resourceID)
	if err == nil {
		return resourceID, true, bodyHash != record.BodyHash, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, false, err
	}
	if err := insertInteraction(ctx, tx, value); err != nil {
		return "", false, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO idempotency_keys(endpoint,idempotency_key,body_hash,resource_kind,resource_id,created_at,expires_at)
		VALUES (?,?,?,?,?,?,?)`, record.Endpoint, record.Key, record.BodyHash, record.ResourceKind, record.ResourceID, record.CreatedAt, record.ExpiresAt); err != nil {
		return "", false, false, err
	}
	if err := tx.Commit(); err != nil {
		return "", false, false, err
	}
	return value.ID, false, false, nil
}

func (r *SQLite) GetInteraction(ctx context.Context, id string, now time.Time) (InteractionResource, error) {
	var value InteractionResource
	err := r.db.QueryRowContext(ctx, `SELECT id,status,model,agent,request_json,steps_json,usage_json,error_json,
		previous_interaction_id,labels_json,store,background,created_at,updated_at,expires_at
		FROM interactions WHERE id=? AND expires_at>?`, id, now.UTC().Unix()).Scan(&value.ID, &value.Status, &value.Model,
		&value.Agent, &value.RequestJSON, &value.StepsJSON, &value.UsageJSON, &value.ErrorJSON,
		&value.PreviousInteractionID, &value.LabelsJSON, &value.Store, &value.Background,
		&value.CreatedAt, &value.UpdatedAt, &value.ExpiresAt)
	return value, err
}

func (r *SQLite) UpdateInteraction(ctx context.Context, value InteractionResource) error {
	result, err := r.db.ExecContext(ctx, `UPDATE interactions SET status=?,steps_json=?,usage_json=?,error_json=?,updated_at=?,expires_at=? WHERE id=?`,
		value.Status, jsonDefault(value.StepsJSON, "[]"), jsonDefault(value.UsageJSON, "{}"),
		jsonDefault(value.ErrorJSON, "null"), value.UpdatedAt, value.ExpiresAt, value.ID)
	if err != nil {
		return fmt.Errorf("update interaction resource: %w", err)
	}
	return requireAffected(result, "interaction resource")
}

func (r *SQLite) CompleteInteractionIfInProgress(ctx context.Context, value InteractionResource) (bool, error) {
	result, err := r.db.ExecContext(ctx, `UPDATE interactions SET status=?,steps_json=?,usage_json=?,error_json=?,updated_at=?,expires_at=?
		WHERE id=? AND status='in_progress'`, value.Status, jsonDefault(value.StepsJSON, "[]"), jsonDefault(value.UsageJSON, "{}"),
		jsonDefault(value.ErrorJSON, "null"), value.UpdatedAt, value.ExpiresAt, value.ID)
	if err != nil {
		return false, fmt.Errorf("complete interaction resource: %w", err)
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (r *SQLite) CancelInteraction(ctx context.Context, id string, updatedAt int64) (InteractionResource, error) {
	if _, err := r.db.ExecContext(ctx, "UPDATE interactions SET status='cancelled',updated_at=? WHERE id=? AND status='in_progress'", updatedAt, id); err != nil {
		return InteractionResource{}, err
	}
	return r.GetInteraction(ctx, id, time.Now())
}

func (r *SQLite) DeleteInteraction(ctx context.Context, id string) error {
	return r.deleteResource(ctx, "interaction", id, "interactions")
}

func (r *SQLite) AppendResourceEvent(ctx context.Context, event ResourceEvent) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO resource_events(resource_kind,resource_id,sequence_number,event_id,event_type,event_json,created_at)
		VALUES (?,?,?,?,?,?,?)`, event.ResourceKind, event.ResourceID, event.Sequence, event.EventID, event.EventType, event.EventJSON, event.CreatedAt)
	if err != nil {
		return fmt.Errorf("append resource event: %w", err)
	}
	return nil
}

func (r *SQLite) AllocateResourceEventSequence(ctx context.Context, kind, id string) (int64, error) {
	var sequence int64
	err := r.db.QueryRowContext(ctx, `INSERT INTO resource_event_counters(resource_kind,resource_id,next_sequence)
		VALUES (?,?,2) ON CONFLICT(resource_kind,resource_id) DO UPDATE SET next_sequence=next_sequence+1
		RETURNING next_sequence-1`, kind, id).Scan(&sequence)
	if err != nil {
		return 0, fmt.Errorf("allocate resource event sequence: %w", err)
	}
	return sequence, nil
}

func (r *SQLite) ListResourceEvents(ctx context.Context, kind, id, afterEventID string) ([]ResourceEvent, error) {
	afterSequence := int64(0)
	if afterEventID != "" {
		if err := r.db.QueryRowContext(ctx, "SELECT sequence_number FROM resource_events WHERE resource_kind=? AND resource_id=? AND event_id=?", kind, id, afterEventID).Scan(&afterSequence); err != nil {
			return nil, err
		}
	}
	rows, err := r.db.QueryContext(ctx, `SELECT resource_kind,resource_id,sequence_number,event_id,event_type,event_json,created_at
		FROM resource_events WHERE resource_kind=? AND resource_id=? AND sequence_number>? ORDER BY sequence_number`, kind, id, afterSequence)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []ResourceEvent
	for rows.Next() {
		var event ResourceEvent
		if err := rows.Scan(&event.ResourceKind, &event.ResourceID, &event.Sequence, &event.EventID, &event.EventType, &event.EventJSON, &event.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (r *SQLite) PutBackgroundJob(ctx context.Context, job BackgroundJob) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO background_jobs(id,resource_kind,resource_id,status,error_json,created_at,updated_at,expires_at)
		VALUES (?,?,?,?,?,?,?,?) ON CONFLICT(resource_kind,resource_id) DO UPDATE SET status=excluded.status,error_json=excluded.error_json,updated_at=excluded.updated_at,expires_at=excluded.expires_at`,
		job.ID, job.ResourceKind, job.ResourceID, job.Status, jsonDefault(job.ErrorJSON, "null"), job.CreatedAt, job.UpdatedAt, job.ExpiresAt)
	return err
}

func (r *SQLite) ResolveIdempotency(ctx context.Context, endpoint, key, bodyHash string, now time.Time) (IdempotencyRecord, bool, error) {
	if key == "" {
		return IdempotencyRecord{}, false, nil
	}
	var record IdempotencyRecord
	err := r.db.QueryRowContext(ctx, `SELECT endpoint,idempotency_key,body_hash,resource_kind,resource_id,created_at,expires_at
		FROM idempotency_keys WHERE endpoint=? AND idempotency_key=? AND expires_at>?`, endpoint, key, now.UTC().Unix()).Scan(
		&record.Endpoint, &record.Key, &record.BodyHash, &record.ResourceKind, &record.ResourceID, &record.CreatedAt, &record.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return IdempotencyRecord{}, false, nil
	}
	return record, err == nil, err
}

func (r *SQLite) PutIdempotency(ctx context.Context, record IdempotencyRecord) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO idempotency_keys(endpoint,idempotency_key,body_hash,resource_kind,resource_id,created_at,expires_at)
		VALUES (?,?,?,?,?,?,?)`, record.Endpoint, record.Key, record.BodyHash, record.ResourceKind, record.ResourceID, record.CreatedAt, record.ExpiresAt)
	return err
}

func (r *SQLite) DeleteExpiredResources(ctx context.Context, now time.Time) error {
	timestamp := now.UTC().Unix()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, query := range []string{
		"DELETE FROM tool_states WHERE response_id IN (SELECT id FROM responses WHERE expires_at<=?) OR conversation_id IN (SELECT id FROM interactions WHERE expires_at<=?)",
		"DELETE FROM resource_events WHERE (resource_kind='response' AND resource_id IN (SELECT id FROM responses WHERE expires_at<=?)) OR (resource_kind='interaction' AND resource_id IN (SELECT id FROM interactions WHERE expires_at<=?))",
		"DELETE FROM resource_event_counters WHERE (resource_kind='response' AND resource_id IN (SELECT id FROM responses WHERE expires_at<=?)) OR (resource_kind='interaction' AND resource_id IN (SELECT id FROM interactions WHERE expires_at<=?))",
		"DELETE FROM responses WHERE expires_at<=?", "DELETE FROM interactions WHERE expires_at<=?",
		"DELETE FROM cached_contents WHERE expires_at<=?", "DELETE FROM batches WHERE expires_at<=?",
		"DELETE FROM chat_completions WHERE expires_at<=?",
		"DELETE FROM background_jobs WHERE expires_at<=?", "DELETE FROM idempotency_keys WHERE expires_at<=?",
	} {
		args := []any{timestamp}
		if strings.Count(query, "?") == 2 {
			args = append(args, timestamp)
		}
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("delete expired resources: %w", err)
		}
	}
	return tx.Commit()
}

func (r *SQLite) MarkInterruptedResources(ctx context.Context, now time.Time) error {
	timestamp := now.UTC().Unix()
	errorJSON := []byte(`{"message":"local process restarted before the background operation completed","code":"operation_interrupted"}`)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	queries := []struct {
		statement string
		args      []any
	}{
		{"UPDATE responses SET status='failed',completed_at=?,error_json=? WHERE status='in_progress'", []any{timestamp, errorJSON}},
		{"UPDATE interactions SET status='failed',updated_at=?,error_json=? WHERE status='in_progress'", []any{timestamp, errorJSON}},
		{"UPDATE batches SET status='failed',completed_at=?,error_json=? WHERE status IN ('validating','in_progress','finalizing','cancelling')", []any{timestamp, errorJSON}},
		{"UPDATE background_jobs SET status='failed',updated_at=?,error_json=? WHERE status='in_progress'", []any{timestamp, errorJSON}},
	}
	for _, query := range queries {
		if _, err := tx.ExecContext(ctx, query.statement, query.args...); err != nil {
			return fmt.Errorf("mark interrupted resources: %w", err)
		}
	}
	return tx.Commit()
}

func (r *SQLite) deleteResource(ctx context.Context, kind, id, table string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM resource_events WHERE resource_kind=? AND resource_id=?", kind, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM resource_event_counters WHERE resource_kind=? AND resource_id=?", kind, id); err != nil {
		return err
	}
	toolStateColumn := "response_id"
	if kind == "interaction" {
		toolStateColumn = "conversation_id"
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM tool_states WHERE "+toolStateColumn+"=?", id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM background_jobs WHERE resource_kind=? AND resource_id=?", kind, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM idempotency_keys WHERE resource_kind=? AND resource_id=?", kind, id); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE id=?", id)
	if err != nil {
		return err
	}
	if err := requireAffected(result, kind); err != nil {
		return err
	}
	return tx.Commit()
}

func requireAffected(result sql.Result, resource string) error {
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("%s not found: %w", resource, sql.ErrNoRows)
	}
	return nil
}

func jsonDefault(value []byte, fallback string) []byte {
	if len(value) == 0 {
		return []byte(fallback)
	}
	return value
}
