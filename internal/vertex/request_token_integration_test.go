package vertex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/recaptcha"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

func TestStreamingFirstVerifyFailureRetriesSameCandidateToken(t *testing.T) {
	var requests atomic.Int32
	var mu sync.Mutex
	var tokens []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := requestRecaptchaToken(t, r)
		mu.Lock()
		tokens = append(tokens, token)
		mu.Unlock()
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"errors":[{"message":"Failed to verify action","extensions":{"status":{"code":3,"status":"INVALID_ARGUMENT"}}}]}`))
			return
		}
		writeSuccessfulStream(w)
	}))
	defer server.Close()

	client, fetches := newIndependentTokenTestClient(t, server.URL, 1)
	var gotErr *VertexError
	var gotData bool
	client.executeStreamingWithRetries(context.Background(), "gemini-test", map[string]any{}, "", func(chunk StreamChunk) bool {
		if chunk.Err != nil {
			gotErr = chunk.Err
		}
		gotData = gotData || chunk.Data != nil
		return true
	})

	mu.Lock()
	gotTokens := append([]string(nil), tokens...)
	mu.Unlock()
	if gotErr != nil || !gotData {
		t.Fatalf("same-token warmup retry did not recover: data=%v err=%v", gotData, gotErr)
	}
	if requests.Load() != 2 || len(gotTokens) != 2 || gotTokens[0] != "token-1" || gotTokens[1] != "token-1" {
		t.Fatalf("warmup retry should reuse only the candidate's token: requests=%d tokens=%v", requests.Load(), gotTokens)
	}
	if got := fetches.Load(); got != 1 {
		t.Fatalf("warmup retry fetched %d tokens, want 1", got)
	}
}

func TestStreamingRepeatedVerifyFailureRefreshesCandidateToken(t *testing.T) {
	var requests atomic.Int32
	var mu sync.Mutex
	var tokens []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := requestRecaptchaToken(t, r)
		mu.Lock()
		tokens = append(tokens, token)
		mu.Unlock()
		if requests.Add(1) <= 2 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"errors":[{"message":"Failed to verify action","extensions":{"status":{"code":3,"status":"INVALID_ARGUMENT"}}}]}`))
			return
		}
		writeSuccessfulStream(w)
	}))
	defer server.Close()

	client, fetches := newIndependentTokenTestClient(t, server.URL, 1)
	var gotErr *VertexError
	var gotData bool
	client.executeStreamingWithRetries(context.Background(), "gemini-test", map[string]any{}, "", func(chunk StreamChunk) bool {
		if chunk.Err != nil {
			gotErr = chunk.Err
		}
		gotData = gotData || chunk.Data != nil
		return true
	})

	mu.Lock()
	gotTokens := append([]string(nil), tokens...)
	mu.Unlock()
	if gotErr != nil || !gotData {
		t.Fatalf("refreshed candidate token did not recover: data=%v err=%v", gotData, gotErr)
	}
	if len(gotTokens) != 3 || gotTokens[0] != "token-1" || gotTokens[1] != "token-1" || gotTokens[2] != "token-2" {
		t.Fatalf("unexpected verify token sequence: %v", gotTokens)
	}
	if got := fetches.Load(); got != 2 {
		t.Fatalf("verify recovery fetched %d tokens, want 2", got)
	}
}

func TestStreamingRateLimitRefreshesCandidateToken(t *testing.T) {
	var requests atomic.Int32
	var mu sync.Mutex
	var tokens []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := requestRecaptchaToken(t, r)
		mu.Lock()
		tokens = append(tokens, token)
		mu.Unlock()
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"quota"}}`))
			return
		}
		writeSuccessfulStream(w)
	}))
	defer server.Close()

	client, fetches := newIndependentTokenTestClient(t, server.URL, 1)
	var gotErr *VertexError
	var gotData bool
	client.executeStreamingWithRetries(context.Background(), "gemini-test", map[string]any{}, "", func(chunk StreamChunk) bool {
		if chunk.Err != nil {
			gotErr = chunk.Err
		}
		gotData = gotData || chunk.Data != nil
		return true
	})

	mu.Lock()
	gotTokens := append([]string(nil), tokens...)
	mu.Unlock()
	if gotErr != nil || !gotData {
		t.Fatalf("429 retry did not recover: data=%v err=%v", gotData, gotErr)
	}
	if len(gotTokens) != 2 || gotTokens[0] != "token-1" || gotTokens[1] != "token-2" {
		t.Fatalf("429 retry must use a fresh token: %v", gotTokens)
	}
	if got := fetches.Load(); got != 2 {
		t.Fatalf("429 retry fetched %d tokens, want 2", got)
	}
}

func newIndependentTokenTestClient(t *testing.T, endpoint string, maxRetries int) (*VertexAIClient, *atomic.Int32) {
	t.Helper()
	oldEndpoint := batchGraphqlURL
	batchGraphqlURL = endpoint
	t.Cleanup(func() { batchGraphqlURL = oldEndpoint })

	cfg := config.DefaultConfig()
	cfg.ParallelPoolEnabled = false
	cfg.MaxRetries = maxRetries
	provider := config.StaticProvider(cfg)
	var fetches atomic.Int32
	pool := recaptcha.NewTokenPoolCustomContext(func(context.Context, string) (string, error) {
		return fmt.Sprintf("token-%d", fetches.Add(1)), nil
	})
	return &VertexAIClient{
		net:  transport.NewNetworkClient(false),
		pool: pool,
		cfg:  provider,
	}, &fetches
}

func requestRecaptchaToken(t *testing.T, r *http.Request) string {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Errorf("decode request: %v", err)
		return ""
	}
	return toStr(toMap(body["variables"])["recaptchaToken"])
}

func writeSuccessfulStream(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"results":[{"data":{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1}}}]}`))
}
