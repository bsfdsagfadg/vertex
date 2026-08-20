package migration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/glebarez/go-sqlite"
)

func (s *Service) detect(ctx context.Context) (Layout, error) {
	layout := Layout{Version: 2, Fresh: true}
	legacyFiles := []struct {
		name string
		code string
		kind string
	}{
		{"nodes.json", "legacy_nodes_json", "request_nodes"},
		{"node_health.json", "legacy_node_health_json", "node_health"},
		{"subscriptions.json", "legacy_subscriptions_json", "subscriptions"},
		{"proxy_url_candidates.json", "legacy_proxy_candidates_json", "global_proxies"},
		{".rules_agreed", "legacy_rules_marker", "state"},
		{"agreed-rules-docker.txt", "legacy_docker_rules_marker", "state"},
	}
	for _, candidate := range legacyFiles {
		path := filepath.Join(s.dataRoot, candidate.name)
		exists, err := regularFileExists(path)
		if err != nil {
			return Layout{}, err
		}
		if exists {
			layout.Fresh = false
			layout.Findings = append(layout.Findings, Finding{
				Code: candidate.code, Scope: "data", Path: candidate.name, Detail: candidate.kind,
			})
		}
	}

	for _, name := range []string{"config.json", "models.json", "api_keys.txt", "data.db"} {
		exists, err := regularFileExists(filepath.Join(s.dataRoot, name))
		if err != nil {
			return Layout{}, err
		}
		if exists {
			layout.Fresh = false
		}
	}

	configFindings, err := detectLegacyConfig(filepath.Join(s.dataRoot, "config.json"))
	if err != nil {
		return Layout{}, err
	}
	layout.Findings = append(layout.Findings, configFindings...)

	modelFindings, err := detectLegacyModels(filepath.Join(s.dataRoot, "models.json"))
	if err != nil {
		return Layout{}, err
	}
	layout.Findings = append(layout.Findings, modelFindings...)

	dbPath := filepath.Join(s.dataRoot, "data.db")
	if exists, err := regularFileExists(dbPath); err != nil {
		return Layout{}, err
	} else if exists {
		dbFindings, dbErr := detectLegacyDatabase(ctx, dbPath)
		if dbErr != nil {
			return Layout{}, dbErr
		}
		layout.Findings = append(layout.Findings, dbFindings...)
	}
	return layout, nil
}

func regularFileExists(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("inspect %s: %w", filepath.Base(path), err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("migration source %s is not a regular file", filepath.Base(path))
	}
	return true, nil
}

func detectLegacyConfig(path string) ([]Finding, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read config for migration detection: %w", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return []Finding{{Code: "config_parse_error", Scope: "data", Path: "config.json"}}, nil
	}
	findings := make([]Finding, 0, 2)
	if _, ok := raw["telemetry_enabled"]; ok {
		findings = append(findings, Finding{Code: "legacy_telemetry_field", Scope: "data", Path: "config.json"})
	}
	if value, ok := raw["proxy_url"]; ok {
		var proxyURL string
		if json.Unmarshal(value, &proxyURL) == nil && strings.TrimSpace(proxyURL) != "" {
			findings = append(findings, Finding{Code: "legacy_proxy_url", Scope: "data", Path: "config.json"})
		}
	}
	return findings, nil
}

func detectLegacyModels(path string) ([]Finding, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read models for migration detection: %w", err)
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return []Finding{{Code: "models_parse_error", Scope: "data", Path: "models.json"}}, nil
	}
	if trimmed[0] == '[' {
		return []Finding{{Code: "legacy_models_array", Scope: "data", Path: "models.json"}}, nil
	}
	var envelope struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(trimmed, &envelope); err != nil {
		return []Finding{{Code: "models_parse_error", Scope: "data", Path: "models.json"}}, nil
	}
	if envelope.Version < 2 {
		return []Finding{{Code: "legacy_models_version", Scope: "data", Path: "models.json"}}, nil
	}
	return nil, nil
}

func detectLegacyDatabase(ctx context.Context, path string) ([]Finding, error) {
	dsn := "file:" + filepath.ToSlash(path) + "?mode=ro"
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database for migration detection: %w", err)
	}
	defer database.Close()
	var version int
	err = database.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(version), 0)
		FROM schema_migrations
	`).Scan(&version)
	if err == nil && version >= 2 {
		return nil, nil
	}
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "no such table") {
		return nil, fmt.Errorf("inspect database schema version: %w", err)
	}
	return []Finding{{Code: "legacy_database_schema", Scope: "data", Path: "data.db"}}, nil
}
