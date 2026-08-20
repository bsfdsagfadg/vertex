package repository

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrToolStateConsumed = errors.New("tool state was already consumed by a different transcript")
	ErrToolStateConflict = errors.New("tool state external call id already exists")
)

func (r *SQLite) PutToolStates(ctx context.Context, states []ToolState) error {
	if len(states) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tool state write: %w", err)
	}
	defer tx.Rollback()
	for _, state := range states {
		if state.ExternalCallID == "" || state.TranscriptHash == "" || len(state.StateJSON) == 0 {
			return errors.New("tool state identity, transcript hash and payload are required")
		}
		blob, err := r.encryptToolState(state.ExternalCallID, state.TranscriptHash, state.StateJSON)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO tool_states
			(external_call_id, response_id, conversation_id, upstream_operation, opaque_blob, expires_at, consumed_at, transcript_hash, consume_hash)
			VALUES (?, ?, ?, ?, ?, ?, 0, ?, '')`,
			state.ExternalCallID, state.ResponseID, state.ConversationID, state.UpstreamOperation,
			blob, state.ExpiresAt, state.TranscriptHash)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
				return fmt.Errorf("%w: %s", ErrToolStateConflict, state.ExternalCallID)
			}
			return fmt.Errorf("write tool state %q: %w", state.ExternalCallID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tool state write: %w", err)
	}
	return nil
}

func (r *SQLite) GetToolStates(ctx context.Context, callIDs []string, now time.Time) (map[string]ToolState, error) {
	result := make(map[string]ToolState, len(callIDs))
	for _, callID := range callIDs {
		var state ToolState
		var blob []byte
		err := r.db.QueryRowContext(ctx, `SELECT external_call_id, response_id, conversation_id,
			upstream_operation, opaque_blob, expires_at, consumed_at, transcript_hash, consume_hash
			FROM tool_states WHERE external_call_id=? AND expires_at>?`, callID, now.UTC().Unix()).Scan(
			&state.ExternalCallID, &state.ResponseID, &state.ConversationID, &state.UpstreamOperation,
			&blob, &state.ExpiresAt, &state.ConsumedAt, &state.TranscriptHash, &state.ConsumeHash)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read tool state %q: %w", callID, err)
		}
		state.StateJSON, err = r.decryptToolState(state.ExternalCallID, state.TranscriptHash, blob)
		if err != nil {
			return nil, fmt.Errorf("decrypt tool state %q: %w", callID, err)
		}
		result[callID] = state
	}
	return result, nil
}

func (r *SQLite) ConsumeToolStates(ctx context.Context, callIDs []string, consumeHash string, now time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tool state consume: %w", err)
	}
	defer tx.Rollback()
	for _, callID := range callIDs {
		var consumedAt int64
		var priorHash string
		if err := tx.QueryRowContext(ctx, "SELECT consumed_at, consume_hash FROM tool_states WHERE external_call_id=?", callID).Scan(&consumedAt, &priorHash); err != nil {
			return fmt.Errorf("read tool state consumption %q: %w", callID, err)
		}
		if consumedAt != 0 && priorHash != consumeHash {
			return ErrToolStateConsumed
		}
		if consumedAt == 0 {
			if _, err := tx.ExecContext(ctx, "UPDATE tool_states SET consumed_at=?, consume_hash=? WHERE external_call_id=?", now.UTC().Unix(), consumeHash, callID); err != nil {
				return fmt.Errorf("consume tool state %q: %w", callID, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tool state consume: %w", err)
	}
	return nil
}

func (r *SQLite) DeleteExpiredToolStates(ctx context.Context, now time.Time) (int64, error) {
	result, err := r.db.ExecContext(ctx, "DELETE FROM tool_states WHERE expires_at<=?", now.UTC().Unix())
	if err != nil {
		return 0, fmt.Errorf("delete expired tool states: %w", err)
	}
	return result.RowsAffected()
}

func (r *SQLite) DeleteToolStatesForResource(ctx context.Context, kind, id string) error {
	column := "response_id"
	if kind == "interaction" {
		column = "conversation_id"
	}
	_, err := r.db.ExecContext(ctx, "DELETE FROM tool_states WHERE "+column+"=?", id)
	if err != nil {
		return fmt.Errorf("delete %s tool states: %w", kind, err)
	}
	return nil
}

func (r *SQLite) encryptToolState(callID, transcriptHash string, plaintext []byte) ([]byte, error) {
	if r.toolStateAEAD == nil {
		return nil, errors.New("tool state encryption is unavailable")
	}
	nonce := make([]byte, r.toolStateAEAD.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate tool state nonce: %w", err)
	}
	aad := []byte(callID + "\x00" + transcriptHash)
	ciphertext := r.toolStateAEAD.Seal(nil, nonce, plaintext, aad)
	return append(nonce, ciphertext...), nil
}

func (r *SQLite) decryptToolState(callID, transcriptHash string, blob []byte) ([]byte, error) {
	if r.toolStateAEAD == nil || len(blob) < r.toolStateAEAD.NonceSize() {
		return nil, errors.New("tool state ciphertext is invalid")
	}
	nonceSize := r.toolStateAEAD.NonceSize()
	aad := []byte(callID + "\x00" + transcriptHash)
	return r.toolStateAEAD.Open(nil, blob[:nonceSize], blob[nonceSize:], aad)
}
