package api

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

func TestAdminModelsV2RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	t.Setenv("VPROXY_MODELS", path)
	config.InvalidateModelsCache()
	t.Cleanup(config.InvalidateModelsCache)

	adm := &AdminHandler{handler: handler{cfg: config.StaticProvider(config.DefaultConfig())}}
	body := `{"models":[{"id":"custom-model","enabled":false,"fake_stream_enabled":false,"trailing_fix_enabled":true}],"alias_map":{"custom":"custom-model"}}`
	req := httptest.NewRequest("PUT", "/api/admin/models", strings.NewReader(body))
	rec := httptest.NewRecorder()
	adm.adminPutModels(rec, req)
	if rec.Code != 200 {
		t.Fatalf("PUT status=%d body=%s", rec.Code, rec.Body.String())
	}

	entry, ok := config.LookupModel("custom-model")
	if !ok || entry.Enabled || entry.FakeStreamEnabled || !entry.TrailingFixEnabled {
		t.Fatalf("保存后的模型状态错误: %+v, ok=%v", entry, ok)
	}
	if config.ResolveModelName("custom") != "custom-model" {
		t.Fatal("别名没有保存")
	}

	getRec := httptest.NewRecorder()
	adm.adminGetModels(getRec, httptest.NewRequest("GET", "/api/admin/models", nil))
	if getRec.Code != 200 {
		t.Fatalf("GET status=%d", getRec.Code)
	}
	var response struct {
		Version int                 `json:"version"`
		Models  []config.ModelEntry `json:"models"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Version != 2 {
		t.Fatalf("version=%d", response.Version)
	}
}

func TestAdminModelsAcceptsLegacyStringList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	t.Setenv("VPROXY_MODELS", path)
	config.InvalidateModelsCache()
	t.Cleanup(config.InvalidateModelsCache)

	adm := &AdminHandler{handler: handler{cfg: config.StaticProvider(config.DefaultConfig())}}
	req := httptest.NewRequest("PUT", "/api/admin/models", strings.NewReader(`{"models":["legacy-model","gemini-3.6-flash"],"alias_map":{}}`))
	rec := httptest.NewRecorder()
	adm.adminPutModels(rec, req)
	if rec.Code != 200 {
		t.Fatalf("PUT legacy status=%d body=%s", rec.Code, rec.Body.String())
	}
	entry, ok := config.LookupModel("legacy-model")
	if !ok || !entry.Enabled || !entry.FakeStreamEnabled {
		t.Fatalf("legacy 模型默认状态错误: %+v, ok=%v", entry, ok)
	}
	gemini36, ok := config.LookupModel("gemini-3.6-flash")
	if !ok || !gemini36.TrailingFixEnabled {
		t.Fatalf("legacy 3.6 Flash 未应用内置尾部修复默认: %+v", gemini36)
	}
}
