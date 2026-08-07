package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

func TestFetchSubWithFallbackDeduplicatesUserAgents(t *testing.T) {
	var mu sync.Mutex
	var received []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		received = append(received, r.Header.Get("User-Agent"))
		mu.Unlock()
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	adm := &AdminHandler{handler: handler{cfg: config.StaticProvider(config.DefaultConfig())}}
	if _, err := adm.fetchSubWithFallback(context.Background(), server.URL, "Clash.Meta"); err == nil {
		t.Fatal("all failed fallback requests should return an error")
	}
	want := []string{"Clash.Meta", "clash-verge/v2.5.2", "v2rayNG/1.8.5"}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(received, want) {
		t.Fatalf("unexpected fallback order: got %v want %v", received, want)
	}
}

func TestSaveSubscriptionRejectsUnknownCustomUA(t *testing.T) {
	t.Setenv("VPROXY_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	if err := config.SaveSubscriptions(config.SubscriptionConfig{}); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/subscriptions/save",
		strings.NewReader(`{"id":"sub-a","name":"A","url":"https://example.com/sub","custom_ua_id":"ua_missing"}`),
	)
	(&AdminHandler{}).adminSaveSubscription(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown custom UA must return 400, got %d: %s", recorder.Code, recorder.Body.String())
	}
}
