package migration

import (
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type BootstrapConfig struct {
	Port            int
	AdminPassword   string
	BackgroundImage string
	FontSize        string
	FontColorType   string
	FontColor       string
	CustomBgPresets []string
	ConfigReadable  bool
}

type Credential struct {
	Secret    string
	Source    string
	TokenPath string
	Created   bool
}

func (s *Service) LoadBootstrapConfig() BootstrapConfig {
	bootstrap := BootstrapConfig{
		Port: 2156, BackgroundImage: "url('background.jpg')", FontSize: "14px",
		FontColorType: "adaptive", FontColor: "#f6f1e9", CustomBgPresets: []string{},
	}
	data, err := os.ReadFile(filepath.Join(s.dataRoot, "config.json"))
	if err != nil {
		return bootstrap
	}
	var raw struct {
		PortAPI         int      `json:"port_api"`
		AdminPassword   string   `json:"admin_password"`
		BackgroundImage string   `json:"background_image"`
		FontSize        string   `json:"font_size"`
		FontColorType   string   `json:"font_color_type"`
		FontColor       string   `json:"font_color"`
		CustomBgPresets []string `json:"custom_bg_presets"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return bootstrap
	}
	bootstrap.ConfigReadable = true
	if raw.PortAPI > 0 && raw.PortAPI <= 65535 {
		bootstrap.Port = raw.PortAPI
	}
	bootstrap.AdminPassword = strings.TrimSpace(raw.AdminPassword)
	if raw.BackgroundImage != "" {
		bootstrap.BackgroundImage = raw.BackgroundImage
	}
	if raw.FontSize != "" {
		bootstrap.FontSize = raw.FontSize
	}
	if raw.FontColorType != "" {
		bootstrap.FontColorType = raw.FontColorType
	}
	if raw.FontColor != "" {
		bootstrap.FontColor = raw.FontColor
	}
	if raw.CustomBgPresets != nil {
		bootstrap.CustomBgPresets = append([]string(nil), raw.CustomBgPresets...)
	}
	return bootstrap
}

func (s *Service) ResolveCredential(bootstrap BootstrapConfig) (Credential, error) {
	if bootstrap.AdminPassword != "" {
		return Credential{Secret: bootstrap.AdminPassword, Source: "legacy_admin_password"}, nil
	}
	if err := os.MkdirAll(s.controlRoot, 0o700); err != nil {
		return Credential{}, fmt.Errorf("create migration control directory: %w", err)
	}
	path := filepath.Join(s.controlRoot, ".migration-token")
	if existing, err := os.ReadFile(path); err == nil {
		secret := strings.TrimSpace(string(existing))
		if len(secret) < 24 {
			return Credential{}, fmt.Errorf("existing migration token is invalid")
		}
		return Credential{Secret: secret, Source: "migration_token", TokenPath: path}, nil
	} else if !os.IsNotExist(err) {
		return Credential{}, fmt.Errorf("read migration token: %w", err)
	}
	random := make([]byte, 24)
	if _, err := cryptorand.Read(random); err != nil {
		return Credential{}, fmt.Errorf("generate migration token: %w", err)
	}
	secret := base64.RawURLEncoding.EncodeToString(random)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return Credential{}, fmt.Errorf("create migration token: %w", err)
	}
	if _, err := file.WriteString(secret + "\n"); err != nil {
		_ = file.Close()
		return Credential{}, fmt.Errorf("write migration token: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return Credential{}, fmt.Errorf("sync migration token: %w", err)
	}
	if err := file.Close(); err != nil {
		return Credential{}, fmt.Errorf("close migration token: %w", err)
	}
	return Credential{Secret: secret, Source: "migration_token", TokenPath: path, Created: true}, nil
}
