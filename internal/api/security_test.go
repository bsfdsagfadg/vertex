package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateSubscriptionURLBlocksPrivateDestinations(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1/sub",
		"http://10.0.0.8/sub",
		"http://[::1]/sub",
		"https://metadata.google.internal/compute",
		"ftp://example.com/sub",
		"https://user:pass@example.com/sub",
	} {
		if err := validateSubscriptionURL(raw); err == nil {
			t.Errorf("validateSubscriptionURL(%q) accepted a forbidden destination", raw)
		}
	}
	if err := validateSubscriptionURL("https://example.com/sub"); err != nil {
		t.Fatalf("public HTTPS subscription rejected: %v", err)
	}
}

func TestAdminCookieMutationRequiresSameOrigin(t *testing.T) {
	token := issueAdminToken()
	t.Cleanup(func() { dropAdminToken(token) })
	adm := &AdminHandler{}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/subscriptions/save", nil)
	request.AddCookie(&http.Cookie{Name: adminCookieName, Value: token})
	recorder := httptest.NewRecorder()
	adm.handleAdminAPI(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin cookie mutation got %d, want 403", recorder.Code)
	}
}
