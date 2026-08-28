package responses

// This file contains the persistence boundary for stateful Responses.  The
// payload fields are JSON blobs on purpose: protocol evolution should not
// require a database migration for every new output item.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type ResponseRecord struct {
	ID                 string          `json:"id"`
	Status             string          `json:"status"`
	Model              string          `json:"model,omitempty"`
	Request            json.RawMessage `json:"request,omitempty"`
	Input              json.RawMessage `json:"input,omitempty"`
	Output             json.RawMessage `json:"output,omitempty"`
	Usage              json.RawMessage `json:"usage,omitempty"`
	Error              json.RawMessage `json:"error,omitempty"`
	Incomplete         json.RawMessage `json:"incomplete_details,omitempty"`
	Metadata           json.RawMessage `json:"metadata,omitempty"`
	PreviousResponseID string          `json:"previous_response_id,omitempty"`
	ConversationID     string          `json:"conversation_id,omitempty"`
	Store              bool            `json:"store"`
	Background         bool            `json:"background"`
	CreatedAt          int64           `json:"created_at"`
	CompletedAt        int64           `json:"completed_at,omitempty"`
	ExpiresAt          int64           `json:"expires_at"`
}

// Compatibility aliases keep the store boundary easy to adopt from the
// repository implementation used by older handlers.
type ResponseResource = ResponseRecord
type ResourceEvent = Event
type ConversationItem = InputItem

type IdempotencyRecord struct {
	Endpoint, Key, BodyHash, ResourceKind, ResourceID string
	CreatedAt, ExpiresAt                              int64
}

type Event struct {
	ResourceKind, ResourceID string
	Sequence                 int64
	EventID, EventType       string
	Data                     json.RawMessage
	CreatedAt                int64
}

type InputItem struct {
	ID        string
	Ordinal   int64
	Data      json.RawMessage
	CreatedAt int64
}

type Conversation struct {
	ID                   string
	Metadata             json.RawMessage
	CreatedAt, UpdatedAt int64
}

type ToolState struct {
	CallID, ExternalCallID, ResponseID, ConversationID, UpstreamOperation string
	State, StateJSON                                                      []byte
	ExpiresAt, ConsumedAt                                                 int64
	TranscriptHash, ConsumeHash                                           string
}

type ResponseStore struct{ db *sql.DB }

func NewResponseStore(db *sql.DB) *ResponseStore { return &ResponseStore{db: db} }

func (s *ResponseStore) CreateResponse(ctx context.Context, r ResponseRecord) error {
	return s.Create(ctx, r)
}
func (s *ResponseStore) GetResponse(ctx context.Context, id string, now time.Time) (ResponseRecord, error) {
	return s.Get(ctx, id, now)
}
func (s *ResponseStore) CompleteResponseIfInProgress(ctx context.Context, r ResponseRecord) (bool, error) {
	return s.UpdateTerminalCAS(ctx, r)
}
func (s *ResponseStore) CancelResponse(ctx context.Context, id string, at int64) (ResponseRecord, error) {
	return s.Cancel(ctx, id, time.Unix(at, 0))
}
func (s *ResponseStore) DeleteResponse(ctx context.Context, id string) error {
	return s.Delete(ctx, id)
}
func (s *ResponseStore) AllocateResourceEventSequence(ctx context.Context, kind, id string) (int64, error) {
	return s.AllocateEventSequence(ctx, kind, id)
}
func (s *ResponseStore) AppendResourceEvent(ctx context.Context, e Event) error {
	return s.AppendEvent(ctx, e)
}
func (s *ResponseStore) ListResourceEvents(ctx context.Context, kind, id, afterID string) ([]Event, error) {
	return s.ReplayEvents(ctx, kind, id, afterID)
}

func (s *ResponseStore) Create(ctx context.Context, r ResponseRecord) error {
	if s == nil || s.db == nil {
		return errors.New("responses: nil database")
	}
	if r.CreatedAt == 0 {
		r.CreatedAt = time.Now().UTC().Unix()
	}
	if r.ExpiresAt == 0 {
		r.ExpiresAt = r.CreatedAt + 24*60*60
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO responses
		(id,status,model,aggregate_json,request_json,input_json,output_json,usage_json,error_json,incomplete_json,metadata_json,previous_response_id,conversation_id,store,background,created_at,completed_at,expires_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, r.ID, r.Status, r.Model, aggregateJSON(r), jsonOr(r.Request, "{}"), jsonOr(r.Input, "[]"), jsonOr(r.Output, "[]"), jsonOr(r.Usage, "{}"), jsonOr(r.Error, "null"), jsonOr(r.Incomplete, "null"), jsonOr(r.Metadata, "{}"), r.PreviousResponseID, r.ConversationID, r.Store, r.Background, r.CreatedAt, r.CompletedAt, r.ExpiresAt)
	return err
}

// CreateIdempotent claims the key and creates the response in one transaction.
// The key row is inserted first, making concurrent callers resolve through the
// unique constraint instead of a SELECT-then-INSERT race.
func (s *ResponseStore) CreateIdempotent(ctx context.Context, r ResponseRecord, rec IdempotencyRecord) (id string, reused, conflict bool, err error) {
	if s == nil || s.db == nil {
		return "", false, false, errors.New("responses: nil database")
	}
	if rec.Key == "" {
		return r.ID, false, false, s.Create(ctx, r)
	}
	if r.CreatedAt == 0 {
		r.CreatedAt = time.Now().UTC().Unix()
	}
	if r.ExpiresAt == 0 {
		r.ExpiresAt = r.CreatedAt + 24*60*60
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, false, err
	}
	defer tx.Rollback()
	if rec.ResourceKind == "" {
		rec.ResourceKind = "response"
	}
	if rec.ExpiresAt == 0 {
		rec.ExpiresAt = r.ExpiresAt
	}
	_, insErr := tx.ExecContext(ctx, `INSERT INTO idempotency_keys(endpoint,idempotency_key,body_hash,resource_kind,resource_id,created_at,expires_at) VALUES (?,?,?,?,?,?,?)`, rec.Endpoint, rec.Key, rec.BodyHash, rec.ResourceKind, r.ID, rec.CreatedAt, rec.ExpiresAt)
	if insErr != nil {
		var oldHash, oldID string
		var oldExpiry int64
		qerr := tx.QueryRowContext(ctx, `SELECT body_hash,resource_id,expires_at FROM idempotency_keys WHERE endpoint=? AND idempotency_key=?`, rec.Endpoint, rec.Key).Scan(&oldHash, &oldID, &oldExpiry)
		if qerr != nil {
			return "", false, false, insErr
		}
		if oldExpiry <= time.Now().UTC().Unix() {
			if _, qerr = tx.ExecContext(ctx, `DELETE FROM idempotency_keys WHERE endpoint=? AND idempotency_key=?`, rec.Endpoint, rec.Key); qerr != nil {
				return "", false, false, qerr
			}
			if _, qerr = tx.ExecContext(ctx, `INSERT INTO idempotency_keys(endpoint,idempotency_key,body_hash,resource_kind,resource_id,created_at,expires_at) VALUES (?,?,?,?,?,?,?)`, rec.Endpoint, rec.Key, rec.BodyHash, rec.ResourceKind, r.ID, rec.CreatedAt, rec.ExpiresAt); qerr != nil {
				return "", false, false, qerr
			}
		} else {
			return oldID, true, oldHash != rec.BodyHash, nil
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO responses (id,status,model,aggregate_json,request_json,input_json,output_json,usage_json,error_json,incomplete_json,metadata_json,previous_response_id,conversation_id,store,background,created_at,completed_at,expires_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, r.ID, r.Status, r.Model, aggregateJSON(r), jsonOr(r.Request, "{}"), jsonOr(r.Input, "[]"), jsonOr(r.Output, "[]"), jsonOr(r.Usage, "{}"), jsonOr(r.Error, "null"), jsonOr(r.Incomplete, "null"), jsonOr(r.Metadata, "{}"), r.PreviousResponseID, r.ConversationID, r.Store, r.Background, r.CreatedAt, r.CompletedAt, r.ExpiresAt); err != nil {
		return "", false, false, err
	}
	if err = tx.Commit(); err != nil {
		return "", false, false, err
	}
	return r.ID, false, false, nil
}

func (s *ResponseStore) CreateResponseIdempotent(ctx context.Context, r ResponseRecord, rec IdempotencyRecord) (string, bool, bool, error) {
	return s.CreateIdempotent(ctx, r, rec)
}

func (s *ResponseStore) Get(ctx context.Context, id string, now time.Time) (ResponseRecord, error) {
	var r ResponseRecord
	err := s.db.QueryRowContext(ctx, `SELECT id,status,model,request_json,input_json,output_json,usage_json,error_json,incomplete_json,metadata_json,previous_response_id,conversation_id,store,background,created_at,completed_at,expires_at FROM responses WHERE id=? AND expires_at>?`, id, now.UTC().Unix()).Scan(&r.ID, &r.Status, &r.Model, &r.Request, &r.Input, &r.Output, &r.Usage, &r.Error, &r.Incomplete, &r.Metadata, &r.PreviousResponseID, &r.ConversationID, &r.Store, &r.Background, &r.CreatedAt, &r.CompletedAt, &r.ExpiresAt)
	return r, err
}

func (s *ResponseStore) UpdateTerminalCAS(ctx context.Context, r ResponseRecord) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE responses SET status=?,aggregate_json=?,output_json=?,usage_json=?,error_json=?,incomplete_json=?,metadata_json=?,completed_at=?,expires_at=? WHERE id=? AND status='in_progress'`, r.Status, aggregateJSON(r), jsonOr(r.Output, "[]"), jsonOr(r.Usage, "{}"), jsonOr(r.Error, "null"), jsonOr(r.Incomplete, "null"), jsonOr(r.Metadata, "{}"), r.CompletedAt, r.ExpiresAt, r.ID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (s *ResponseStore) Cancel(ctx context.Context, id string, at time.Time) (ResponseRecord, error) {
	_, err := s.db.ExecContext(ctx, `UPDATE responses SET status='cancelled',completed_at=? WHERE id=? AND status='in_progress'`, at.UTC().Unix(), id)
	if err != nil {
		return ResponseRecord{}, err
	}
	return s.Get(ctx, id, at.Add(time.Second))
}

func (s *ResponseStore) Delete(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{"DELETE FROM resource_events WHERE resource_kind='response' AND resource_id=?", "DELETE FROM resource_event_counters WHERE resource_kind='response' AND resource_id=?", "DELETE FROM tool_states WHERE response_id=?", "DELETE FROM idempotency_keys WHERE resource_kind='response' AND resource_id=?"} {
		if _, err = tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	res, err := tx.ExecContext(ctx, "DELETE FROM responses WHERE id=?", id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

func (s *ResponseStore) InputItems(ctx context.Context, id string) ([]InputItem, error) {
	r, err := s.Get(ctx, id, time.Now())
	if err != nil {
		return nil, err
	}
	var raw []any
	if err := json.Unmarshal(r.Input, &raw); err != nil {
		return nil, err
	}
	out := make([]InputItem, 0, len(raw))
	for i, v := range raw {
		b, _ := json.Marshal(v)
		item := InputItem{Ordinal: int64(i), Data: b}
		if obj, ok := v.(map[string]any); ok {
			if id, ok := obj["id"].(string); ok {
				item.ID = id
			}
		}
		out = append(out, item)
	}
	return out, nil
}
func (s *ResponseStore) GetInputItems(ctx context.Context, id string) ([]InputItem, error) {
	return s.InputItems(ctx, id)
}

func (s *ResponseStore) AllocateEventSequence(ctx context.Context, kind, id string) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `INSERT INTO resource_event_counters(resource_kind,resource_id,next_sequence) VALUES (?,?,2) ON CONFLICT(resource_kind,resource_id) DO UPDATE SET next_sequence=next_sequence+1 RETURNING next_sequence-1`, kind, id).Scan(&n)
	return n, err
}
func (s *ResponseStore) AppendEvent(ctx context.Context, e Event) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO resource_events(resource_kind,resource_id,sequence_number,event_id,event_type,event_json,created_at) VALUES (?,?,?,?,?,?,?)`, e.ResourceKind, e.ResourceID, e.Sequence, e.EventID, e.EventType, jsonOr(e.Data, "{}"), e.CreatedAt)
	return err
}
func (s *ResponseStore) ReplayEvents(ctx context.Context, kind, id, afterID string) ([]Event, error) {
	after := int64(0)
	if afterID != "" {
		if err := s.db.QueryRowContext(ctx, `SELECT sequence_number FROM resource_events WHERE resource_kind=? AND resource_id=? AND event_id=?`, kind, id, afterID).Scan(&after); err != nil {
			return nil, err
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT resource_kind,resource_id,sequence_number,event_id,event_type,event_json,created_at FROM resource_events WHERE resource_kind=? AND resource_id=? AND sequence_number>? ORDER BY sequence_number`, kind, id, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ResourceKind, &e.ResourceID, &e.Sequence, &e.EventID, &e.EventType, &e.Data, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *ResponseStore) CreateConversation(ctx context.Context, c Conversation) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO conversations(id,metadata_json,created_at,updated_at) VALUES (?,?,?,?)`, c.ID, jsonOr(c.Metadata, "{}"), c.CreatedAt, c.UpdatedAt)
	return err
}
func (s *ResponseStore) GetConversation(ctx context.Context, id string) (Conversation, error) {
	var c Conversation
	err := s.db.QueryRowContext(ctx, `SELECT id,metadata_json,created_at,updated_at FROM conversations WHERE id=?`, id).Scan(&c.ID, &c.Metadata, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

func (s *ResponseStore) DeleteConversation(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM conversations WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *ResponseStore) ListConversationItems(ctx context.Context, id string, after int64, limit int) ([]InputItem, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,ordinal,item_json,created_at FROM conversation_items WHERE conversation_id=? AND ordinal>? ORDER BY ordinal LIMIT ?`, id, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InputItem
	for rows.Next() {
		var it InputItem
		if err := rows.Scan(&it.ID, &it.Ordinal, &it.Data, &it.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}
func (s *ResponseStore) AddConversationItems(ctx context.Context, id string, items []InputItem) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var start int64
	if err = tx.QueryRowContext(ctx, `UPDATE conversations SET next_ordinal=next_ordinal+?,updated_at=? WHERE id=? RETURNING next_ordinal-?`, len(items), time.Now().Unix(), id, len(items)).Scan(&start); err != nil {
		return err
	}
	for i, it := range items {
		if _, err = tx.ExecContext(ctx, `INSERT INTO conversation_items(id,conversation_id,ordinal,item_json,created_at) VALUES (?,?,?,?,?)`, it.ID, id, start+int64(i), jsonOr(it.Data, "{}"), it.CreatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *ResponseStore) PutToolState(ctx context.Context, t ToolState) error {
	if t.CallID == "" {
		t.CallID = t.ExternalCallID
	}
	if len(t.State) == 0 {
		t.State = t.StateJSON
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO tool_states(external_call_id,response_id,conversation_id,upstream_operation,opaque_blob,expires_at,consumed_at,transcript_hash,consume_hash) VALUES (?,?,?,?,?,?,?,?,?) ON CONFLICT(external_call_id) DO UPDATE SET opaque_blob=excluded.opaque_blob,expires_at=excluded.expires_at,transcript_hash=excluded.transcript_hash,consume_hash=excluded.consume_hash`, t.CallID, t.ResponseID, t.ConversationID, t.UpstreamOperation, t.State, t.ExpiresAt, t.ConsumedAt, t.TranscriptHash, t.ConsumeHash)
	return err
}

func (s *ResponseStore) PutToolStates(ctx context.Context, states []ToolState) error {
	for _, state := range states {
		if err := s.PutToolState(ctx, state); err != nil {
			return err
		}
	}
	return nil
}

func (s *ResponseStore) GetToolStates(ctx context.Context, callIDs []string, now time.Time) (map[string]ToolState, error) {
	out := make(map[string]ToolState, len(callIDs))
	for _, id := range callIDs {
		var t ToolState
		err := s.db.QueryRowContext(ctx, `SELECT external_call_id,response_id,conversation_id,upstream_operation,opaque_blob,expires_at,consumed_at,transcript_hash,consume_hash FROM tool_states WHERE external_call_id=? AND expires_at>?`, id, now.Unix()).Scan(&t.ExternalCallID, &t.ResponseID, &t.ConversationID, &t.UpstreamOperation, &t.State, &t.ExpiresAt, &t.ConsumedAt, &t.TranscriptHash, &t.ConsumeHash)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		t.CallID, t.StateJSON = t.ExternalCallID, t.State
		out[id] = t
	}
	return out, nil
}

func (s *ResponseStore) ConsumeToolStates(ctx context.Context, callIDs []string, hash string, at time.Time) error {
	for _, id := range callIDs {
		var prior string
		var consumed int64
		if err := s.db.QueryRowContext(ctx, `SELECT consumed_at,consume_hash FROM tool_states WHERE external_call_id=?`, id).Scan(&consumed, &prior); err != nil {
			return err
		}
		if consumed != 0 && prior != hash {
			return errors.New("tool state was already consumed by a different transcript")
		}
		if consumed == 0 {
			if _, err := s.db.ExecContext(ctx, `UPDATE tool_states SET consumed_at=?,consume_hash=? WHERE external_call_id=?`, at.Unix(), hash, id); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *ResponseStore) ConsumeToolState(ctx context.Context, callID, hash string, at time.Time) (ToolState, error) {
	var t ToolState
	err := s.db.QueryRowContext(ctx, `UPDATE tool_states SET consumed_at=?,consume_hash=? WHERE external_call_id=? AND consumed_at=0 AND expires_at>? RETURNING external_call_id,response_id,conversation_id,upstream_operation,opaque_blob,expires_at,consumed_at,transcript_hash,consume_hash`, at.Unix(), hash, callID, at.Unix()).Scan(&t.CallID, &t.ResponseID, &t.ConversationID, &t.UpstreamOperation, &t.State, &t.ExpiresAt, &t.ConsumedAt, &t.TranscriptHash, &t.ConsumeHash)
	t.ExternalCallID, t.StateJSON = t.CallID, t.State
	return t, err
}

// DeleteExpired removes response-owned state and replay data in one
// transaction. It is safe to call periodically from a maintenance loop.
func (s *ResponseStore) DeleteExpired(ctx context.Context, now time.Time) error {
	ts := now.UTC().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	queries := []string{
		"DELETE FROM tool_states WHERE response_id IN (SELECT id FROM responses WHERE expires_at<=?)",
		"DELETE FROM resource_events WHERE resource_kind='response' AND resource_id IN (SELECT id FROM responses WHERE expires_at<=?)",
		"DELETE FROM resource_event_counters WHERE resource_kind='response' AND resource_id NOT IN (SELECT id FROM responses)",
		"DELETE FROM responses WHERE expires_at<=?",
		"DELETE FROM idempotency_keys WHERE expires_at<=?",
	}
	for _, q := range queries {
		if _, err := tx.ExecContext(ctx, q, ts); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *ResponseStore) MarkInterrupted(ctx context.Context, now time.Time) error {
	if s == nil || s.db == nil {
		return errors.New("responses: nil database")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM responses WHERE status='in_progress'`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	errorJSON := []byte(`{"message":"local process restarted before the operation completed","code":"operation_interrupted"}`)
	if _, err := tx.ExecContext(ctx, `UPDATE responses SET status='failed',completed_at=?,error_json=? WHERE status='in_progress'`, now.UTC().Unix(), errorJSON); err != nil {
		return err
	}
	for _, id := range ids {
		var seq int64
		if err := tx.QueryRowContext(ctx, `INSERT INTO resource_event_counters(resource_kind,resource_id,next_sequence) VALUES (?,?,2) ON CONFLICT(resource_kind,resource_id) DO UPDATE SET next_sequence=next_sequence+1 RETURNING next_sequence-1`, "response", id).Scan(&seq); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"id": id, "status": "failed", "error": map[string]any{"message": "local process restarted before the operation completed", "code": "operation_interrupted"}})
		if _, err := tx.ExecContext(ctx, `INSERT INTO resource_events(resource_kind,resource_id,sequence_number,event_id,event_type,event_json,created_at) VALUES (?,?,?,?,?,?,?)`, "response", id, seq, id+"_interrupted", "response.failed", data, now.UTC().Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *ResponseStore) DeleteExpiredResources(ctx context.Context, now time.Time) error {
	return s.DeleteExpired(ctx, now)
}
func (s *ResponseStore) MarkInterruptedResources(ctx context.Context, now time.Time) error {
	return s.MarkInterrupted(ctx, now)
}

func aggregateJSON(r ResponseRecord) []byte { b, _ := json.Marshal(r); return b }
func jsonOr(b json.RawMessage, fallback string) []byte {
	if len(b) == 0 {
		return []byte(fallback)
	}
	if !json.Valid(b) {
		return []byte(fallback)
	}
	return b
}
