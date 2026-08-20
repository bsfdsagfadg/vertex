package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (r *SQLite) CreateLocalFile(ctx context.Context, value LocalFile) error {
	return insertLocalFile(ctx, r.db, value)
}

func insertLocalFile(ctx context.Context, executor sqlExecutor, value LocalFile) error {
	_, err := executor.ExecContext(ctx, `INSERT INTO local_files
		(id,dialect,name,display_name,purpose,mime_type,size_bytes,sha256,storage_path,status,metadata_json,created_at,expires_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, value.ID, value.Dialect, value.Name, value.DisplayName, value.Purpose,
		value.MimeType, value.SizeBytes, value.SHA256, value.StoragePath, value.Status,
		jsonDefault(value.MetadataJSON, "{}"), value.CreatedAt, value.ExpiresAt)
	if err != nil {
		return fmt.Errorf("create local file: %w", err)
	}
	return nil
}

func (r *SQLite) CreateLocalFileIdempotent(ctx context.Context, value LocalFile, record IdempotencyRecord) (string, bool, bool, error) {
	if record.Key == "" {
		return value.ID, false, false, r.CreateLocalFile(ctx, value)
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
	if err := insertLocalFile(ctx, tx, value); err != nil {
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

func (r *SQLite) GetLocalFile(ctx context.Context, id string, now time.Time) (LocalFile, error) {
	var value LocalFile
	err := r.db.QueryRowContext(ctx, `SELECT id,dialect,name,display_name,purpose,mime_type,size_bytes,sha256,storage_path,status,metadata_json,created_at,expires_at
		FROM local_files WHERE id=? AND expires_at>?`, id, now.UTC().Unix()).Scan(&value.ID, &value.Dialect, &value.Name,
		&value.DisplayName, &value.Purpose, &value.MimeType, &value.SizeBytes, &value.SHA256, &value.StoragePath,
		&value.Status, &value.MetadataJSON, &value.CreatedAt, &value.ExpiresAt)
	return value, err
}

func (r *SQLite) GetLocalFileDialect(ctx context.Context, id, dialect string, now time.Time) (LocalFile, error) {
	var value LocalFile
	err := r.db.QueryRowContext(ctx, `SELECT id,dialect,name,display_name,purpose,mime_type,size_bytes,sha256,storage_path,status,metadata_json,created_at,expires_at
		FROM local_files WHERE id=? AND dialect=? AND expires_at>?`, id, dialect, now.UTC().Unix()).Scan(&value.ID, &value.Dialect, &value.Name,
		&value.DisplayName, &value.Purpose, &value.MimeType, &value.SizeBytes, &value.SHA256, &value.StoragePath,
		&value.Status, &value.MetadataJSON, &value.CreatedAt, &value.ExpiresAt)
	return value, err
}

func (r *SQLite) ListLocalFiles(ctx context.Context, dialect string, afterCreated int64, afterID string, limit int, now time.Time) ([]LocalFile, error) {
	if limit <= 0 || limit > 101 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id,dialect,name,display_name,purpose,mime_type,size_bytes,sha256,storage_path,status,metadata_json,created_at,expires_at
		FROM local_files WHERE dialect=? AND expires_at>? AND (created_at>? OR (created_at=? AND id>?)) ORDER BY created_at,id LIMIT ?`,
		dialect, now.UTC().Unix(), afterCreated, afterCreated, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("list local files: %w", err)
	}
	defer rows.Close()
	var values []LocalFile
	for rows.Next() {
		var value LocalFile
		if err := rows.Scan(&value.ID, &value.Dialect, &value.Name, &value.DisplayName, &value.Purpose, &value.MimeType,
			&value.SizeBytes, &value.SHA256, &value.StoragePath, &value.Status, &value.MetadataJSON, &value.CreatedAt, &value.ExpiresAt); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (r *SQLite) DeleteLocalFile(ctx context.Context, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM idempotency_keys WHERE resource_kind='file' AND resource_id=?", id); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM local_files WHERE id=?", id)
	if err != nil {
		return fmt.Errorf("delete local file: %w", err)
	}
	if err := requireAffected(result, "file"); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *SQLite) ListExpiredLocalFiles(ctx context.Context, now time.Time) ([]LocalFile, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,dialect,name,display_name,purpose,mime_type,size_bytes,sha256,storage_path,status,metadata_json,created_at,expires_at
		FROM local_files WHERE expires_at<=? ORDER BY expires_at LIMIT 500`, now.UTC().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []LocalFile
	for rows.Next() {
		var value LocalFile
		if err := rows.Scan(&value.ID, &value.Dialect, &value.Name, &value.DisplayName, &value.Purpose, &value.MimeType,
			&value.SizeBytes, &value.SHA256, &value.StoragePath, &value.Status, &value.MetadataJSON, &value.CreatedAt, &value.ExpiresAt); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (r *SQLite) CreateCachedContent(ctx context.Context, value CachedContent) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO cached_contents
		(id,model,contents_json,system_instruction_json,tools_json,metadata_json,created_at,updated_at,expires_at)
		VALUES (?,?,?,?,?,?,?,?,?)`, value.ID, value.Model, value.ContentsJSON, jsonDefault(value.SystemInstructionJSON, "null"),
		jsonDefault(value.ToolsJSON, "[]"), jsonDefault(value.MetadataJSON, "{}"), value.CreatedAt, value.UpdatedAt, value.ExpiresAt)
	if err != nil {
		return fmt.Errorf("create cached content: %w", err)
	}
	return nil
}

func (r *SQLite) GetCachedContent(ctx context.Context, id string, now time.Time) (CachedContent, error) {
	var value CachedContent
	err := r.db.QueryRowContext(ctx, `SELECT id,model,contents_json,system_instruction_json,tools_json,metadata_json,created_at,updated_at,expires_at
		FROM cached_contents WHERE id=? AND expires_at>?`, id, now.UTC().Unix()).Scan(&value.ID, &value.Model, &value.ContentsJSON,
		&value.SystemInstructionJSON, &value.ToolsJSON, &value.MetadataJSON, &value.CreatedAt, &value.UpdatedAt, &value.ExpiresAt)
	return value, err
}

func (r *SQLite) ListCachedContents(ctx context.Context, limit int, now time.Time) ([]CachedContent, error) {
	if limit <= 0 || limit > 101 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id,model,contents_json,system_instruction_json,tools_json,metadata_json,created_at,updated_at,expires_at
		FROM cached_contents WHERE expires_at>? ORDER BY created_at,id LIMIT ?`, now.UTC().Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []CachedContent
	for rows.Next() {
		var value CachedContent
		if err := rows.Scan(&value.ID, &value.Model, &value.ContentsJSON, &value.SystemInstructionJSON, &value.ToolsJSON,
			&value.MetadataJSON, &value.CreatedAt, &value.UpdatedAt, &value.ExpiresAt); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (r *SQLite) UpdateCachedContent(ctx context.Context, value CachedContent) error {
	result, err := r.db.ExecContext(ctx, `UPDATE cached_contents SET metadata_json=?,updated_at=?,expires_at=? WHERE id=?`,
		jsonDefault(value.MetadataJSON, "{}"), value.UpdatedAt, value.ExpiresAt, value.ID)
	if err != nil {
		return fmt.Errorf("update cached content: %w", err)
	}
	return requireAffected(result, "cached content")
}

func (r *SQLite) DeleteCachedContent(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, "DELETE FROM cached_contents WHERE id=?", id)
	if err != nil {
		return fmt.Errorf("delete cached content: %w", err)
	}
	return requireAffected(result, "cached content")
}

func (r *SQLite) CreateBatchIdempotent(ctx context.Context, value Batch, record IdempotencyRecord) (string, bool, bool, error) {
	if record.Key == "" {
		return value.ID, false, false, r.createBatch(ctx, value)
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
	if err := insertBatch(ctx, tx, value); err != nil {
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

func (r *SQLite) createBatch(ctx context.Context, value Batch) error {
	return insertBatch(ctx, r.db, value)
}

func insertBatch(ctx context.Context, executor sqlExecutor, value Batch) error {
	_, err := executor.ExecContext(ctx, `INSERT INTO batches
		(id,dialect,endpoint,input_file_id,output_file_id,error_file_id,status,request_counts_json,metadata_json,error_json,
		 created_at,in_progress_at,completed_at,cancelled_at,expires_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		value.ID, value.Dialect, value.Endpoint, value.InputFileID, value.OutputFileID, value.ErrorFileID, value.Status,
		jsonDefault(value.RequestCountsJSON, "{}"), jsonDefault(value.MetadataJSON, "{}"), jsonDefault(value.ErrorJSON, "null"),
		value.CreatedAt, value.InProgressAt, value.CompletedAt, value.CancelledAt, value.ExpiresAt)
	if err != nil {
		return fmt.Errorf("create batch: %w", err)
	}
	return nil
}

func (r *SQLite) GetBatch(ctx context.Context, id string, now time.Time) (Batch, error) {
	var value Batch
	err := r.db.QueryRowContext(ctx, `SELECT id,dialect,endpoint,input_file_id,output_file_id,error_file_id,status,request_counts_json,
		metadata_json,error_json,created_at,in_progress_at,completed_at,cancelled_at,expires_at FROM batches WHERE id=? AND expires_at>?`,
		id, now.UTC().Unix()).Scan(&value.ID, &value.Dialect, &value.Endpoint, &value.InputFileID, &value.OutputFileID,
		&value.ErrorFileID, &value.Status, &value.RequestCountsJSON, &value.MetadataJSON, &value.ErrorJSON, &value.CreatedAt,
		&value.InProgressAt, &value.CompletedAt, &value.CancelledAt, &value.ExpiresAt)
	return value, err
}

func (r *SQLite) GetBatchDialect(ctx context.Context, id, dialect string, now time.Time) (Batch, error) {
	var value Batch
	err := r.db.QueryRowContext(ctx, `SELECT id,dialect,endpoint,input_file_id,output_file_id,error_file_id,status,request_counts_json,
		metadata_json,error_json,created_at,in_progress_at,completed_at,cancelled_at,expires_at FROM batches WHERE id=? AND dialect=? AND expires_at>?`,
		id, dialect, now.UTC().Unix()).Scan(&value.ID, &value.Dialect, &value.Endpoint, &value.InputFileID, &value.OutputFileID,
		&value.ErrorFileID, &value.Status, &value.RequestCountsJSON, &value.MetadataJSON, &value.ErrorJSON, &value.CreatedAt,
		&value.InProgressAt, &value.CompletedAt, &value.CancelledAt, &value.ExpiresAt)
	return value, err
}

func (r *SQLite) ListBatches(ctx context.Context, dialect string, afterCreated int64, afterID string, limit int, now time.Time) ([]Batch, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id,dialect,endpoint,input_file_id,output_file_id,error_file_id,status,request_counts_json,
		metadata_json,error_json,created_at,in_progress_at,completed_at,cancelled_at,expires_at FROM batches
		WHERE dialect=? AND expires_at>? AND (created_at>? OR (created_at=? AND id>?)) ORDER BY created_at,id LIMIT ?`,
		dialect, now.UTC().Unix(), afterCreated, afterCreated, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []Batch
	for rows.Next() {
		var value Batch
		if err := rows.Scan(&value.ID, &value.Dialect, &value.Endpoint, &value.InputFileID, &value.OutputFileID,
			&value.ErrorFileID, &value.Status, &value.RequestCountsJSON, &value.MetadataJSON, &value.ErrorJSON, &value.CreatedAt,
			&value.InProgressAt, &value.CompletedAt, &value.CancelledAt, &value.ExpiresAt); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (r *SQLite) UpdateBatchIfActive(ctx context.Context, value Batch) (bool, error) {
	result, err := r.db.ExecContext(ctx, `UPDATE batches SET output_file_id=?,error_file_id=?,status=?,request_counts_json=?,error_json=?,
		in_progress_at=?,completed_at=?,cancelled_at=?,expires_at=? WHERE id=? AND status IN ('validating','in_progress','finalizing','cancelling')`,
		value.OutputFileID, value.ErrorFileID, value.Status, jsonDefault(value.RequestCountsJSON, "{}"), jsonDefault(value.ErrorJSON, "null"),
		value.InProgressAt, value.CompletedAt, value.CancelledAt, value.ExpiresAt, value.ID)
	if err != nil {
		return false, fmt.Errorf("update active batch: %w", err)
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (r *SQLite) CancelBatch(ctx context.Context, id string, cancelledAt int64) (Batch, error) {
	if _, err := r.db.ExecContext(ctx, `UPDATE batches SET status='cancelled',cancelled_at=? WHERE id=? AND status IN ('validating','in_progress','finalizing','cancelling')`, cancelledAt, id); err != nil {
		return Batch{}, err
	}
	return r.GetBatch(ctx, id, time.Now())
}

func (r *SQLite) DeleteBatch(ctx context.Context, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM idempotency_keys WHERE resource_kind='batch' AND resource_id=?", id); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM batches WHERE id=?", id)
	if err != nil {
		return err
	}
	if err := requireAffected(result, "batch"); err != nil {
		return err
	}
	return tx.Commit()
}
