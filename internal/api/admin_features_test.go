package api

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

// adminLogin 登录管理后台并返回认证用的 session token。
func adminLogin(t *testing.T, srvURL string) string {
	t.Helper()
	resp := doPost(t, srvURL+"/api/admin/login", "", map[string]any{"password": "test-admin-pw"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin login failed: status=%d", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == adminCookieName && c.Value != "" {
			return c.Value
		}
	}
	t.Fatal("no admin cookie found in login response")
	return ""
}

// adminRequest 使用已登录的 cookie 向管理 API 发送请求。
// method 默认 POST，可指定 "PUT" 等。
func adminRequest(t *testing.T, srvURL, path, cookie string, body any) *http.Response {
	t.Helper()
	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("adminRequest marshal: %v", err)
		}
		buf = strings.NewReader(string(b))
	}
	method := http.MethodPost
	if path == "/settings" {
		method = http.MethodPut
	}
	req, err := http.NewRequest(method, srvURL+"/api/admin"+path, buf)
	if err != nil {
		t.Fatalf("adminRequest new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: adminCookieName, Value: cookie})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("adminRequest do: %v", err)
	}
	return resp
}

type trackingDialer struct {
	syncCount atomic.Int64
	entryAddr atomic.Value
}

func (d *trackingDialer) CreateDialer(uri string, reqID string) (func(ctx context.Context, network, addr string) (net.Conn, error), func(), error) {
	var neter net.Dialer
	return neter.DialContext, func() {}, nil
}
func (d *trackingDialer) StopAll() {}
func (d *trackingDialer) GetNextEntrySocksAddr() string {
	if v, ok := d.entryAddr.Load().(string); ok {
		return v
	}
	return ""
}
func (d *trackingDialer) SyncEntryPool() error { d.syncCount.Add(1); return nil }
func (d *trackingDialer) TestEntryProxy(uri string) (func(ctx context.Context, network, addr string) (net.Conn, error), func(), error) {
	var neter net.Dialer
	return neter.DialContext, func() {}, nil
}

func TestAdminFetchSub_EntryPool(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	subSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("vmess://abc123\nvmess://def456"))
	}))
	defer subSrv.Close()

	t.Run("entry_pool_available", func(t *testing.T) {
		td := &trackingDialer{}
		td.entryAddr.Store("127.0.0.1:10000")
		fx := newTestServerCustomMock(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[{"results":[{"data":{}}]}]`))
		}, func(cfg *config.AppConfig) {
			cfg.AdminPassword = "test-admin-pw"
		}, td)

		cookie := adminLogin(t, fx.server.URL)
		resp := adminRequest(t, fx.server.URL, "/subscriptions/fetch", cookie, map[string]any{
			"url": subSrv.URL + "/sub",
		})
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d, want 200", resp.StatusCode)
		}
		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		ok, _ := body["ok"].(bool)
		if !ok {
			t.Errorf("ok=%v, want true; body=%v", ok, body)
		}
	})

	t.Run("empty_pool_direct_fallback", func(t *testing.T) {
		td := &trackingDialer{}
		fx := newTestServerCustomMock(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[{"results":[{"data":{}}]}]`))
		}, func(cfg *config.AppConfig) {
			cfg.AdminPassword = "test-admin-pw"
		}, td)

		cookie := adminLogin(t, fx.server.URL)
		resp := adminRequest(t, fx.server.URL, "/subscriptions/fetch", cookie, map[string]any{
			"url": subSrv.URL + "/sub",
		})
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d, want 200", resp.StatusCode)
		}
		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		ok, _ := body["ok"].(bool)
		if !ok {
			t.Errorf("ok=%v, want true", ok)
		}
	})
}

func TestAdminFeatures_ProxyNodePool(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	entryURI := "socks5://entry:1080"

	t.Run("toggle_trigger_sync", func(t *testing.T) {
		td := &trackingDialer{}
		fx := newTestServerCustomMock(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[{"results":[{"data":{}}]}]`))
		}, func(cfg *config.AppConfig) {
			cfg.AdminPassword = "test-admin-pw"
		}, td)

		cookie := adminLogin(t, fx.server.URL)

		// 导入节点
		resp := adminRequest(t, fx.server.URL, "/proxy-nodes/import", cookie, map[string]any{
			"raw_uri": entryURI,
		})
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("import status=%d, want 200", resp.StatusCode)
		}

		// 启用（toggle, disabled=false）
		resp = adminRequest(t, fx.server.URL, "/proxy-nodes/toggle", cookie, map[string]any{
			"uris":     []string{entryURI},
			"disabled": false,
		})
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("toggle status=%d, want 200", resp.StatusCode)
		}

		// 列表查询（GET）
		req, err := http.NewRequest(http.MethodGet, fx.server.URL+"/api/admin/proxy-nodes", nil)
		if err != nil {
			t.Fatalf("build list request: %v", err)
		}
		req.AddCookie(&http.Cookie{Name: adminCookieName, Value: cookie})
		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("list request: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("list status=%d, want 200", resp.StatusCode)
		}
		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if _, exists := body["nodes"]; !exists {
			t.Errorf("list response missing 'nodes' field")
		}
	})

	t.Run("import_invalid_uri_fails", func(t *testing.T) {
		td := &trackingDialer{}
		fx := newTestServerCustomMock(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[{"results":[{"data":{}}]}]`))
		}, func(cfg *config.AppConfig) {
			cfg.AdminPassword = "test-admin-pw"
		}, td)

		cookie := adminLogin(t, fx.server.URL)
		resp := adminRequest(t, fx.server.URL, "/proxy-nodes/import", cookie, map[string]any{
			"raw_uri": "not-a-valid://uri",
		})
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status=%d, want 400 for invalid URI", resp.StatusCode)
		}
		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		errMap, ok := body["error"].(map[string]any)
		if !ok {
			t.Errorf("expected error map in body, got %T: %v", body["error"], body)
		} else if msg, _ := errMap["message"].(string); msg == "" {
			t.Error("expected error message in body")
		}
	})

	t.Run("delete_disabled_and_dedup", func(t *testing.T) {
		td := &trackingDialer{}
		fx := newTestServerCustomMock(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[{"results":[{"data":{}}]}]`))
		}, func(cfg *config.AppConfig) {
			cfg.AdminPassword = "test-admin-pw"
		}, td)

		cookie := adminLogin(t, fx.server.URL)

		for _, path := range []string{"/proxy-nodes/dedup", "/proxy-nodes/delete-disabled", "/proxy-nodes/batch-delete"} {
			var body any
			if path == "/proxy-nodes/batch-delete" {
				body = map[string]any{"uris": []string{entryURI}}
			}
			resp := adminRequest(t, fx.server.URL, path, cookie, body)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("%s status=%d, want 200", path, resp.StatusCode)
			}
			resp.Body.Close()
		}
	})
}
