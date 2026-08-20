package toolstate

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/repository"
)

type memoryStore struct {
	states      map[string]repository.ToolState
	consumeHash string
}

func (m *memoryStore) PutToolStates(_ context.Context, states []repository.ToolState) error {
	if m.states == nil {
		m.states = map[string]repository.ToolState{}
	}
	for _, state := range states {
		m.states[state.ExternalCallID] = state
	}
	return nil
}

func (m *memoryStore) GetToolStates(_ context.Context, ids []string, now time.Time) (map[string]repository.ToolState, error) {
	result := map[string]repository.ToolState{}
	for _, id := range ids {
		if state, ok := m.states[id]; ok && state.ExpiresAt > now.Unix() {
			result[id] = state
		}
	}
	return result, nil
}

func (m *memoryStore) ConsumeToolStates(_ context.Context, _ []string, consumeHash string, _ time.Time) error {
	if m.consumeHash != "" && m.consumeHash != consumeHash {
		return repository.ErrToolStateConsumed
	}
	m.consumeHash = consumeHash
	return nil
}

func (m *memoryStore) DeleteExpiredToolStates(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func TestCaptureAndRestoreParallelOpaqueStep(t *testing.T) {
	store := &memoryStore{}
	service := New(store)
	response := map[string]any{"candidates": []any{map[string]any{
		"content": map[string]any{"role": "model", "parts": []any{
			map[string]any{"thought": true, "text": "private", "thoughtSignature": "not-base64!"},
			map[string]any{"functionCall": map[string]any{"id": "call_a", "name": "lookup", "args": map[string]any{"q": "a"}}, "thoughtSignature": "sig-a!"},
			map[string]any{"functionCall": map[string]any{"id": "call_b", "name": "lookup", "args": map[string]any{"q": "b"}}, "thoughtSignature": "sig-b!"},
		}},
	}}}
	if err := service.CaptureResponse(context.Background(), response, "chatcmpl-1", "", "chat.completions"); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"contents": []any{
		map[string]any{"role": "model", "parts": []any{
			map[string]any{"functionCall": map[string]any{"id": "call_a", "name": "lookup", "args": map[string]any{"q": "a"}}},
			map[string]any{"functionCall": map[string]any{"id": "call_b", "name": "lookup", "args": map[string]any{"q": "b"}}},
		}},
		map[string]any{"role": "function", "parts": []any{
			map[string]any{"functionResponse": map[string]any{"id": "call_b", "name": "lookup", "response": map[string]any{"value": "b"}}},
			map[string]any{"functionResponse": map[string]any{"id": "call_a", "name": "lookup", "response": map[string]any{"value": "a"}}},
		}},
	}}
	if err := service.RestoreOpenAIChat(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	contents := payload["contents"].([]any)
	parts := contents[0].(map[string]any)["parts"].([]any)
	if got := parts[0].(map[string]any)["thoughtSignature"]; got != "not-base64!" {
		t.Fatalf("thought signature changed: %v", got)
	}
	if len(parts) != 3 {
		t.Fatalf("restored parts=%d, want exact upstream step", len(parts))
	}
}

func TestRestoreRejectsIncompleteParallelResults(t *testing.T) {
	store := &memoryStore{}
	service := New(store)
	response := map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"role": "model", "parts": []any{
		map[string]any{"functionCall": map[string]any{"id": "call_a", "name": "a"}},
		map[string]any{"functionCall": map[string]any{"id": "call_b", "name": "b"}},
	}}}}}
	if err := service.CaptureResponse(context.Background(), response, "r", "", "chat.completions"); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"contents": []any{
		response["candidates"].([]any)[0].(map[string]any)["content"],
		map[string]any{"role": "function", "parts": []any{map[string]any{"functionResponse": map[string]any{"id": "call_a", "name": "a", "response": map[string]any{}}}}},
	}}
	err := service.RestoreOpenAIChat(context.Background(), payload)
	var protocolErr *ProtocolError
	if !errors.As(err, &protocolErr) || protocolErr.Code != "incomplete_tool_transcript" {
		t.Fatalf("error=%v, want incomplete_tool_transcript", err)
	}
}

func TestRestoreRejectsMissingState(t *testing.T) {
	service := New(&memoryStore{states: map[string]repository.ToolState{}})
	payload := map[string]any{"contents": []any{
		map[string]any{"role": "model", "parts": []any{map[string]any{"functionCall": map[string]any{"id": "call_missing", "name": "lookup"}}}},
		map[string]any{"role": "function", "parts": []any{map[string]any{"functionResponse": map[string]any{"id": "call_missing", "name": "lookup", "response": map[string]any{}}}}},
	}}
	err := service.RestoreOpenAIChat(context.Background(), payload)
	var protocolErr *ProtocolError
	if !errors.As(err, &protocolErr) || protocolErr.Code != "tool_state_missing" {
		t.Fatalf("error=%v, want tool_state_missing", err)
	}
}
