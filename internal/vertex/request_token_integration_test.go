package vertex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

func TestStreamingHTTPCode3RecaptchaErrorBecomesRefreshControl(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"errors":[{"message":"Recaptcha token is invalid","extensions":{"status":{"code":3,"status":"INVALID_ARGUMENT"}}}]}`))
	}))
	defer server.Close()

	client, ctx, fetches := newRequestTokenTestClient(t, server.URL)
	var gotErr *VertexError
	client.executeStreamingWithRetries(ctx, "gemini-test", map[string]any{}, "", func(chunk StreamChunk) bool {
		gotErr = chunk.Err
		return true
	})

	if gotErr == nil || !gotErr.requestTokenInvalid || gotErr.requestToken != "token-1" {
		t.Fatalf("HTTP code=3 recaptcha error did not become refresh control: %+v", gotErr)
	}
	if got := fetches.Load(); got != 1 {
		t.Fatalf("token fetched %d times before RunRace coordination, want 1", got)
	}
}

func TestStreamingFirstVerifyFailureRetriesSameToken(t *testing.T) {
	var requests atomic.Int32
	var tokens []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		tokens = append(tokens, toStr(toMap(body["variables"])["recaptchaToken"]))
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"errors":[{"message":"Failed to verify action","extensions":{"status":{"code":3,"status":"INVALID_ARGUMENT"}}}]}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"data":{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1}}}]}`))
	}))
	defer server.Close()

	client, ctx, fetches := newRequestTokenTestClient(t, server.URL)
	var gotErr *VertexError
	var gotData bool
	client.executeStreamingWithRetries(ctx, "gemini-test", map[string]any{}, "", func(chunk StreamChunk) bool {
		if chunk.Err != nil {
			gotErr = chunk.Err
		}
		gotData = gotData || chunk.Data != nil
		return true
	})

	if gotErr != nil || !gotData {
		t.Fatalf("same-token retry did not recover: data=%v err=%v", gotData, gotErr)
	}
	if requests.Load() != 2 || len(tokens) != 2 || tokens[0] != "token-1" || tokens[1] != "token-1" {
		t.Fatalf("verify retry did not reuse token: requests=%d tokens=%v", requests.Load(), tokens)
	}
	if got := fetches.Load(); got != 1 {
		t.Fatalf("verify retry fetched %d tokens, want 1", got)
	}
}

func TestStreamingRepeatedVerifyFailureBecomesRefreshControl(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"errors":[{"message":"Failed to verify action","extensions":{"status":{"code":3,"status":"INVALID_ARGUMENT"}}}]}`))
	}))
	defer server.Close()

	client, ctx, fetches := newRequestTokenTestClient(t, server.URL)
	var gotErr *VertexError
	client.executeStreamingWithRetries(ctx, "gemini-test", map[string]any{}, "", func(chunk StreamChunk) bool {
		gotErr = chunk.Err
		return true
	})

	if gotErr == nil || !gotErr.requestTokenInvalid || gotErr.requestToken != "token-1" {
		t.Fatalf("repeated verify failure did not become refresh control: %+v", gotErr)
	}
	if requests.Load() != 2 || fetches.Load() != 1 {
		t.Fatalf("unexpected retry counts: requests=%d token fetches=%d", requests.Load(), fetches.Load())
	}
}

func newRequestTokenTestClient(t *testing.T, endpoint string) (*VertexAIClient, context.Context, *atomic.Int32) {
	t.Helper()
	oldEndpoint := batchGraphqlURL
	batchGraphqlURL = endpoint
	t.Cleanup(func() { batchGraphqlURL = oldEndpoint })

	cfg := config.DefaultConfig()
	cfg.ParallelPoolEnabled = true
	cfg.ParallelPoolRetryEnabled = false
	cfg.MaxRetries = 0
	provider := config.StaticProvider(cfg)
	var fetches atomic.Int32
	state := &requestTokenState{fetchToken: func(context.Context) (string, error) {
		return fmt.Sprintf("token-%d", fetches.Add(1)), nil
	}} //nolint:exhaustruct
	state.setRefreshLimit(1)
	if _, err := state.get(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), requestRouteKey{}, &requestRoute{
		token:              state,
		authCandidateBound: true,
	})
	return &VertexAIClient{
		net: transport.NewNetworkClient(false),
		cfg: provider,
	}, ctx, &fetches
}
