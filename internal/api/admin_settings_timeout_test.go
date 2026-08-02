package api

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

func TestAdminSettingsNormalizesTimeoutBounds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("VPROXY_CONFIG", path)
	config.InvalidateCache()
	t.Cleanup(config.InvalidateCache)

	adm := &AdminHandler{handler: handler{cfg: config.StaticProvider(config.DefaultConfig())}}
	req := httptest.NewRequest("PUT", "/api/admin/settings", strings.NewReader(
		`{"settings":{"request_timeout":9999,"race_timeout":-10}}`,
	))
	rec := httptest.NewRecorder()
	adm.adminPutSettings(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	raw := map[string]any{}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["request_timeout"] != float64(1800) {
		t.Fatalf("request_timeout=%v, want 1800", raw["request_timeout"])
	}
	if raw["race_timeout"] != float64(0) {
		t.Fatalf("race_timeout=%v, want 0", raw["race_timeout"])
	}
}
