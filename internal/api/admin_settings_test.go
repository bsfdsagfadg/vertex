package api

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

// adminGetSettingsRaw 使用已登录 cookie 发起 GET /api/admin/settings，返回解析后的响应体。
func adminGetSettingsRaw(t *testing.T, srvURL, cookie string) map[string]any {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srvURL+"/api/admin/settings", nil)
	if err != nil {
		t.Fatalf("adminGetSettings new request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: adminCookieName, Value: cookie})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("adminGetSettings do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("adminGetSettings status=%d, want 200", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("adminGetSettings decode: %v", err)
	}
	return body
}

func TestAdminSettings_TrailingFixModelsRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	fx := newTestServerCustomMock(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[{"results":[{"data":{}}]}]`))
	}, func(cfg *config.AppConfig) {
		cfg.AdminPassword = "test-admin-pw"
	})

	cookie := adminLogin(t, fx.server.URL)

	t.Run("GET 返回默认 2 条清单", func(t *testing.T) {
		body := adminGetSettingsRaw(t, fx.server.URL, cookie)
		settings, ok := body["settings"].(map[string]any)
		if !ok {
			t.Fatalf("响应缺少 settings 对象: %v", body)
		}
		raw, ok := settings["trailing_fix_models"].([]any)
		if !ok {
			t.Fatalf("settings 缺少 trailing_fix_models 数组, got %v", settings["trailing_fix_models"])
		}
		want := []string{"gemini-3.5-flash-lite", "gemini-3.6-flash"}
		var got []string
		for _, item := range raw {
			got = append(got, item.(string))
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("trailing_fix_models=%v, want %v", got, want)
		}
	})

	t.Run("PUT 归一化去重并 trim 后持久化", func(t *testing.T) {
		resp := adminRequest(t, fx.server.URL, "/settings", cookie, map[string]any{
			"settings": map[string]any{
				"trailing_fix_models": []string{" gemini-3.6-flash ", "gemini-3.6-flash", ""},
			},
		})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PUT status=%d, want 200", resp.StatusCode)
		}

		// admin handler 持有静态 provider 快照，持久化结果须从 config.Load() 验证。
		config.InvalidateCache()
		got := config.Load().TrailingFixModels
		want := []string{"gemini-3.6-flash"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("PUT 后 TrailingFixModels=%v, want %v", got, want)
		}
	})
}
