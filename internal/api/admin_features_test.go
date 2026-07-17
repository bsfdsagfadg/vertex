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

// failDialer 模拟 ValidateEntryProxy 对特定 URI 返回错误的 dialer。
type failDialer struct {
	failURI string
}

func (d *failDialer) CreateDialer(uri string, reqID string) (func(ctx context.Context, network, addr string) (net.Conn, error), func(), error) {
	var neter net.Dialer
	return neter.DialContext, func() {}, nil
}
func (d *failDialer) StopAll()                        {}
func (d *failDialer) EntryProxySocksAddr() string     { return "" }
func (d *failDialer) SyncEntryProxy(uri string) error { return nil }
func (d *failDialer) TestEntryProxy(uri string) (func(ctx context.Context, network, addr string) (net.Conn, error), func(), error) {
	var neter net.Dialer
	return neter.DialContext, func() {}, nil
}
func (d *failDialer) ValidateEntryProxy(uri string) (io.Closer, string, error) {
	if uri == d.failURI {
		return nil, "", assertAnError{msg: "validation failed for test"}
	}
	return io.NopCloser(nil), "", nil
}
func (d *failDialer) AdoptEntryProxy(_ string, _ io.Closer, _ string) error { return nil }

type assertAnError struct{ msg string }

func (e assertAnError) Error() string { return e.msg }

func TestAdminFetchSub_WithProxyURL(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	subSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("vmess://abc123\nvmess://def456"))
	}))
	defer subSrv.Close()

	t.Run("with_proxy_url", func(t *testing.T) {
		fx := newTestServerCustomMock(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[{"results":[{"data":{}}]}]`))
		}, func(cfg *config.AppConfig) {
			cfg.ProxyURL = "socks5://entry:1080"
		}, &directDialer{})

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
		// count may be 0 if parseImportedNodes can't decode; functional test = request succeeded
		if _, exists := body["count"]; !exists {
			t.Error("response missing 'count' field")
		}
	})

	t.Run("without_proxy_url", func(t *testing.T) {
		fx := newTestServerCustomMock(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[{"results":[{"data":{}}]}]`))
		}, func(cfg *config.AppConfig) {
			cfg.ProxyURL = ""
		}, &directDialer{})

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

type trackingDialer struct {
	validateCount atomic.Int64
	adoptCount    atomic.Int64
	syncCount     atomic.Int64
}

func (d *trackingDialer) CreateDialer(uri string, reqID string) (func(ctx context.Context, network, addr string) (net.Conn, error), func(), error) {
	var neter net.Dialer
	return neter.DialContext, func() {}, nil
}
func (d *trackingDialer) StopAll()                        {}
func (d *trackingDialer) EntryProxySocksAddr() string     { return "" }
func (d *trackingDialer) SyncEntryProxy(uri string) error { d.syncCount.Add(1); return nil }
func (d *trackingDialer) TestEntryProxy(uri string) (func(ctx context.Context, network, addr string) (net.Conn, error), func(), error) {
	var neter net.Dialer
	return neter.DialContext, func() {}, nil
}
func (d *trackingDialer) ValidateEntryProxy(uri string) (io.Closer, string, error) {
	d.validateCount.Add(1)
	return io.NopCloser(nil), "127.0.0.1:10000", nil
}
func (d *trackingDialer) AdoptEntryProxy(_ string, _ io.Closer, _ string) error {
	d.adoptCount.Add(1)
	return nil
}

func TestAdminFeatures_ProxyNodeEnableDisable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	entryURI := "socks5://entry:1080"

	t.Run("enable_ok", func(t *testing.T) {
		td := &trackingDialer{}
		fx := newTestServerCustomMock(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[{"results":[{"data":{}}]}]`))
		}, func(cfg *config.AppConfig) {
			cfg.ProxyURL = ""
			cfg.AdminPassword = "test-admin-pw"
			cfg.ProxyURLCandidates = []config.ProxyCandidate{
				{RawURI: entryURI, Name: "entry", Type: "socks5"},
			}
		}, td)

		cookie := adminLogin(t, fx.server.URL)
		resp := adminRequest(t, fx.server.URL, "/proxy-nodes/enable", cookie, map[string]any{
			"raw_uri": entryURI,
		})
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d, want 200", resp.StatusCode)
		}
		if td.validateCount.Load() != 1 {
			t.Errorf("ValidateEntryProxy called %d times, want 1", td.validateCount.Load())
		}
		if td.adoptCount.Load() != 1 {
			t.Errorf("AdoptEntryProxy called %d times, want 1", td.adoptCount.Load())
		}
	})

	t.Run("enable_invalid_uri_fails", func(t *testing.T) {
		fd := &failDialer{failURI: "socks5://bad:1080"}
		fx := newTestServerCustomMock(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[{"results":[{"data":{}}]}]`))
		}, func(cfg *config.AppConfig) {
			cfg.ProxyURL = ""
			cfg.AdminPassword = "test-admin-pw"
			cfg.ProxyURLCandidates = []config.ProxyCandidate{
				{RawURI: "socks5://bad:1080", Name: "bad", Type: "socks5"},
			}
		}, fd)

		cookie := adminLogin(t, fx.server.URL)
		resp := adminRequest(t, fx.server.URL, "/proxy-nodes/enable", cookie, map[string]any{
			"raw_uri": "socks5://bad:1080",
		})
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("status=%d, want 500 for invalid URI", resp.StatusCode)
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

	t.Run("disable_ok", func(t *testing.T) {
		td := &trackingDialer{}
		fx := newTestServerCustomMock(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[{"results":[{"data":{}}]}]`))
		}, func(cfg *config.AppConfig) {
			cfg.ProxyURL = "socks5://entry:1080"
			cfg.AdminPassword = "test-admin-pw"
		}, td)

		cookie := adminLogin(t, fx.server.URL)
		resp := adminRequest(t, fx.server.URL, "/proxy-nodes/disable", cookie, nil)
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d, want 200", resp.StatusCode)
		}
		if td.syncCount.Load() != 1 {
			t.Errorf("SyncEntryProxy called %d times, want 1", td.syncCount.Load())
		}
	})

	t.Run("put_settings_proxy_url_enable", func(t *testing.T) {
		td := &trackingDialer{}
		fx := newTestServerCustomMock(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[{"results":[{"data":{}}]}]`))
		}, func(cfg *config.AppConfig) {
			cfg.ProxyURL = ""
			cfg.AdminPassword = "test-admin-pw"
		}, td)

		cookie := adminLogin(t, fx.server.URL)
		resp := adminRequest(t, fx.server.URL, "/settings", cookie, map[string]any{
			"settings": map[string]any{"proxy_url": "socks5://entry:1080"},
		})
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d, want 200", resp.StatusCode)
		}
		if td.validateCount.Load() != 1 {
			t.Errorf("ValidateEntryProxy called %d times, want 1", td.validateCount.Load())
		}
		if td.adoptCount.Load() != 1 {
			t.Errorf("AdoptEntryProxy called %d times, want 1", td.adoptCount.Load())
		}
	})

	t.Run("put_settings_proxy_url_disable", func(t *testing.T) {
		td := &trackingDialer{}
		fx := newTestServerCustomMock(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[{"results":[{"data":{}}]}]`))
		}, func(cfg *config.AppConfig) {
			cfg.ProxyURL = "socks5://entry:1080"
			cfg.AdminPassword = "test-admin-pw"
		}, td)

		cookie := adminLogin(t, fx.server.URL)
		resp := adminRequest(t, fx.server.URL, "/settings", cookie, map[string]any{
			"settings": map[string]any{"proxy_url": ""},
		})
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d, want 200", resp.StatusCode)
		}
		if td.syncCount.Load() != 1 {
			t.Errorf("SyncEntryProxy called %d times, want 1", td.syncCount.Load())
		}
		if td.validateCount.Load() != 0 {
			t.Errorf("ValidateEntryProxy called %d times, want 0 for disable path", td.validateCount.Load())
		}
	})
}
