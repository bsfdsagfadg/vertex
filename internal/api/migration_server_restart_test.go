package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/buildinfo"
	"github.com/bsfdsagfadg/vertex/internal/migration"
)

func TestMigrationRestartRoutesTerminalStates(t *testing.T) {
	tests := []struct {
		name         string
		state        migration.State
		wantStatus   int
		wantMode     string
		wantRestart  bool
		wantRollback bool
	}{
		{name: "completed", state: migration.StateCompleted, wantStatus: http.StatusAccepted, wantMode: "restart_v2", wantRestart: true},
		{name: "rollback prepared", state: migration.StateRollbackPrepared, wantStatus: http.StatusAccepted, wantMode: "exit_for_v1", wantRollback: true},
		{name: "nonterminal", state: migration.StatePrepared, wantStatus: http.StatusConflict},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			controlRoot := filepath.Join(root, ".v2-migration")
			if err := os.MkdirAll(controlRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			statusData, err := json.Marshal(migration.Status{State: test.state, UpdatedAt: time.Now()})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(controlRoot, "status.json"), statusData, 0o600); err != nil {
				t.Fatal(err)
			}
			service, err := migration.NewService(root)
			if err != nil {
				t.Fatal(err)
			}
			restarted := make(chan struct{}, 1)
			rolledBack := make(chan struct{}, 1)
			server := NewMigrationServer(service, buildinfo.BuildInfo{}, migration.BootstrapConfig{}, migration.Credential{},
				WithRestartRequested(func() { restarted <- struct{}{} }),
				WithRollbackPrepared(func() { rolledBack <- struct{}{} }),
			)

			handler := server.requireAdmin(server.requireSafeMutation(server.handleRestart))
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "http://example.test/api/admin/migration/restart", nil)
			request.Header.Set("X-VProxy-Action", "migration")
			request.Header.Set("Origin", "http://example.test")
			request.AddCookie(&http.Cookie{Name: adminCookieName, Value: issueAdminToken()})
			handler(recorder, request)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if test.wantMode != "" && !containsJSONMode(recorder.Body.Bytes(), test.wantMode) {
				t.Fatalf("response does not contain mode %q: %s", test.wantMode, recorder.Body.String())
			}
			assertCallback(t, restarted, test.wantRestart)
			assertCallback(t, rolledBack, test.wantRollback)
		})
	}
}

func TestMigrationRestartRejectsUnauthenticatedOrCrossOriginRequest(t *testing.T) {
	service, err := migration.NewService(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := NewMigrationServer(service, buildinfo.BuildInfo{}, migration.BootstrapConfig{}, migration.Credential{})
	handler := server.requireAdmin(server.requireSafeMutation(server.handleRestart))
	for _, test := range []struct {
		name    string
		request *http.Request
		want    int
	}{
		{name: "unauthenticated", request: httptest.NewRequest(http.MethodPost, "http://example.test/api/admin/migration/restart", nil), want: http.StatusUnauthorized},
		{name: "cross origin", request: func() *http.Request {
			req := httptest.NewRequest(http.MethodPost, "http://example.test/api/admin/migration/restart", nil)
			req.Header.Set("X-VProxy-Action", "migration")
			req.Header.Set("Origin", "https://attacker.test")
			req.AddCookie(&http.Cookie{Name: adminCookieName, Value: issueAdminToken()})
			return req
		}(), want: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler(recorder, test.request)
			if recorder.Code != test.want {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func containsJSONMode(data []byte, want string) bool {
	var response struct {
		Mode string `json:"mode"`
	}
	return json.Unmarshal(data, &response) == nil && response.Mode == want
}

func assertCallback(t *testing.T, callback <-chan struct{}, want bool) {
	t.Helper()
	timer := time.NewTimer(350 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-callback:
		if !want {
			t.Fatal("unexpected lifecycle callback")
		}
	case <-timer.C:
		if want {
			t.Fatal("expected lifecycle callback")
		}
	}
}
