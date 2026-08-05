package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/nodes"
)

func TestAdminTestNodeDisablesUnsupportedAndUnparseableURIs(t *testing.T) {
	adm := &AdminHandler{}
	cases := []struct {
		name string
		uri  string
	}{
		{"unsupported transport", "vless://uuid@example.com:443?type=xhttp"},
		{"legacy clash uri", "clash://" + base64.StdEncoding.EncodeToString([]byte(`{"name":"demo","type":"ss","server":"example.com","port":8388}`))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"raw_uri":%q,"auto_disable":true}`, tc.uri)
			req := httptest.NewRequest(http.MethodPost, "/api/admin/nodes/test", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			adm.adminTestNode(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			var resp map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp["ok"] != false || resp["disabled"] != true {
				t.Fatalf("expected ok=false disabled=true, got %#v", resp)
			}
			errStr, _ := resp["error"].(string)
			if errStr == "" {
				t.Fatalf("expected non-empty error reason")
			}
			// 不支持/不可解析路径写 healthMap 记录原因
			h := nodes.LoadHealth()[tc.uri]
			if h == nil || h.LastTestError == "" {
				t.Fatalf("expected health error recorded, got %#v", h)
			}
		})
	}
}

func TestAdminTestProxyNodeSkipsUnsupportedURI(t *testing.T) {
	adm := &AdminHandler{}
	body := `{"raw_uri":"vless://uuid@example.com:443?type=xhttp"}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/proxy-nodes/test", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	adm.adminTestProxyNode(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["ok"] != false {
		t.Fatalf("expected ok=false, got %#v", resp)
	}
	errStr, _ := resp["error"].(string)
	if !strings.Contains(errStr, "unsupported") {
		t.Fatalf("expected unsupported reason, got %q", errStr)
	}
}
