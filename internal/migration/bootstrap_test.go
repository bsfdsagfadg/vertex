package migration

import (
	"strings"
	"testing"
)

func TestResolveCredentialUsesAdminPasswordWithoutOneTimeToken(t *testing.T) {
	service, err := NewService(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	credential, err := service.ResolveCredential(BootstrapConfig{AdminPassword: "admin-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if credential.Secret != "admin-secret" || credential.Source != "legacy_admin_password" {
		t.Fatalf("unexpected credential: %+v", credential)
	}
}

func TestResolveCredentialRejectsMissingAdminPassword(t *testing.T) {
	service, err := NewService(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ResolveCredential(BootstrapConfig{})
	if err == nil || !strings.Contains(err.Error(), "管理员密码未设置") {
		t.Fatalf("expected clear missing-password error, got %v", err)
	}
}
