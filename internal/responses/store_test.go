package responses

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	_ "github.com/glebarez/go-sqlite"
)

func testStore(t *testing.T) *ResponseStore {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE responses (id TEXT PRIMARY KEY,status TEXT,model TEXT,aggregate_json BLOB,request_json BLOB,input_json BLOB,output_json BLOB,usage_json BLOB,error_json BLOB,incomplete_json BLOB,metadata_json BLOB,previous_response_id TEXT,conversation_id TEXT,store BOOLEAN,background BOOLEAN,created_at INTEGER,completed_at INTEGER,expires_at INTEGER);
CREATE TABLE idempotency_keys (endpoint TEXT NOT NULL,idempotency_key TEXT NOT NULL,body_hash TEXT NOT NULL,resource_kind TEXT,resource_id TEXT,created_at INTEGER,expires_at INTEGER,PRIMARY KEY(endpoint,idempotency_key));
CREATE TABLE resource_event_counters(resource_kind TEXT,resource_id TEXT,next_sequence INTEGER,PRIMARY KEY(resource_kind,resource_id));
CREATE TABLE resource_events(resource_kind TEXT,resource_id TEXT,sequence_number INTEGER,event_id TEXT UNIQUE,event_type TEXT,event_json BLOB,created_at INTEGER,PRIMARY KEY(resource_kind,resource_id,sequence_number));
CREATE TABLE tool_states(external_call_id TEXT PRIMARY KEY,response_id TEXT,conversation_id TEXT,upstream_operation TEXT,opaque_blob BLOB,expires_at INTEGER,consumed_at INTEGER,transcript_hash TEXT,consume_hash TEXT);`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return NewResponseStore(db)
}

func TestResponseStoreIdempotencyAndCAS(t *testing.T) {
	s := testStore(t)
	now := time.Now()
	r := ResponseRecord{ID: "resp_1", Status: "in_progress", Model: "m", Input: json.RawMessage(`[{"type":"message"}]`), CreatedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix()}
	rec := IdempotencyRecord{Endpoint: "/v1/responses", Key: "k", BodyHash: "h", CreatedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix()}
	id, reused, conflict, err := s.CreateIdempotent(context.Background(), r, rec)
	if err != nil || id != r.ID || reused || conflict {
		t.Fatalf("first create: %v %q %v %v", err, id, reused, conflict)
	}
	id, reused, conflict, err = s.CreateIdempotent(context.Background(), ResponseRecord{ID: "resp_2", ExpiresAt: r.ExpiresAt}, rec)
	if err != nil || id != r.ID || !reused || conflict {
		t.Fatalf("reuse: %v %q %v %v", err, id, reused, conflict)
	}
	_, reused, conflict, err = s.CreateIdempotent(context.Background(), ResponseRecord{ID: "resp_3", ExpiresAt: r.ExpiresAt}, IdempotencyRecord{Endpoint: rec.Endpoint, Key: rec.Key, BodyHash: "different", ExpiresAt: rec.ExpiresAt})
	if err != nil || !reused || !conflict {
		t.Fatalf("conflict: %v %v %v", err, reused, conflict)
	}
	r.Status = "completed"
	ok, err := s.UpdateTerminalCAS(context.Background(), r)
	if err != nil || !ok {
		t.Fatalf("CAS completion: %v %v", err, ok)
	}
	ok, err = s.UpdateTerminalCAS(context.Background(), r)
	if err != nil || ok {
		t.Fatalf("second CAS should lose: %v %v", err, ok)
	}
}

func TestResponseStoreEventSequenceReplay(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	seq, err := s.AllocateEventSequence(ctx, "response", "r")
	if err != nil || seq != 1 {
		t.Fatalf("first sequence: %v %d", err, seq)
	}
	if err := s.AppendEvent(ctx, Event{ResourceKind: "response", ResourceID: "r", Sequence: seq, EventID: "e1", EventType: "created", Data: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	seq, _ = s.AllocateEventSequence(ctx, "response", "r")
	_ = s.AppendEvent(ctx, Event{ResourceKind: "response", ResourceID: "r", Sequence: seq, EventID: "e2", EventType: "completed", Data: json.RawMessage(`{}`)})
	events, err := s.ReplayEvents(ctx, "response", "r", "e1")
	if err != nil || len(events) != 1 || events[0].EventID != "e2" {
		t.Fatalf("replay: %v %#v", err, events)
	}
}
