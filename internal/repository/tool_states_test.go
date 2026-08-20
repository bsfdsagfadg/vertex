package repository

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestToolStateRoundTripIsEncryptedAtRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.db")
	repo, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	plaintext := []byte(`{"thoughtSignature":"opaque-secret-value"}`)
	state := ToolState{ExternalCallID: "call_1", UpstreamOperation: "chat.completions", StateJSON: plaintext, ExpiresAt: time.Now().Add(time.Hour).Unix(), TranscriptHash: "hash-1"}
	if err := repo.PutToolStates(context.Background(), []ToolState{state}); err != nil {
		t.Fatal(err)
	}
	databaseBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(databaseBytes, []byte("opaque-secret-value")) {
		t.Fatal("opaque tool state leaked into SQLite plaintext")
	}
	loaded, err := repo.GetToolStates(context.Background(), []string{"call_1"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loaded["call_1"].StateJSON, plaintext) {
		t.Fatalf("round trip=%q, want %q", loaded["call_1"].StateJSON, plaintext)
	}
}
