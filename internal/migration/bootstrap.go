package migration

import (
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
	Secret string
	Source string
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
	return Credential{}, fmt.Errorf("管理员密码未设置，无法进入迁移控制台")
}
