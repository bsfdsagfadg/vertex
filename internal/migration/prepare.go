package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

var (
	ErrNotRequired   = errors.New("migration is not required")
	ErrPlanConflict  = errors.New("migration plan has conflicts")
	ErrPlanExpired   = errors.New("migration plan expired")
	ErrSourceChanged = errors.New("migration source changed after prepare")
)

func (s *Service) Prepare(ctx context.Context) (Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	status, err := s.inspectAndMarkLocked(ctx)
	if err != nil {
		return Plan{}, err
	}
	if !status.Required || status.Marker == nil {
		return Plan{}, ErrNotRequired
	}
	if status.State != StateRequired && status.State != StatePrepared {
		return Plan{}, fmt.Errorf("migration prepare requires Required or Prepared state, got %s", status.State)
	}
	now := s.now().UTC()
	manifest, conflicts, requiredBytes, err := s.buildManifest(ctx)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{
		Version:       1,
		MigrationID:   status.Marker.MigrationID,
		CreatedAt:     now,
		ExpiresAt:     now.Add(s.planTTL),
		Manifest:      manifest,
		Conflicts:     conflicts,
		RequiredBytes: requiredBytes,
	}
	plan.PlanHash, err = hashPlan(plan)
	if err != nil {
		return Plan{}, err
	}
	if err := writeJSONAtomic(filepath.Join(s.controlRoot, planFileName), &plan); err != nil {
		return Plan{}, err
	}
	status.State = StatePrepared
	status.Plan = &plan
	status.Recoverable = false
	status.LastError = ""
	status.FailedFrom = ""
	status.UpdatedAt = now
	if len(conflicts) > 0 {
		status.LastError = ErrPlanConflict.Error()
	}
	if err := s.saveStatus(&status); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func (s *Service) buildManifest(ctx context.Context) ([]ManifestEntry, []Conflict, int64, error) {
	names := []struct {
		name string
		kind string
	}{
		{"config.json", "config"},
		{"models.json", "models"},
		{"api_keys.txt", "api_keys"},
		{"data.db", "database"},
		{"data.db-wal", "database_wal"},
		{"data.db-shm", "database_shm"},
		{"nodes.json", "request_nodes"},
		{"node_health.json", "node_health"},
		{"subscriptions.json", "subscriptions"},
		{"proxy_url_candidates.json", "global_proxies"},
		{".rules_agreed", "state"},
		{"agreed-rules-docker.txt", "state"},
	}
	manifest := make([]ManifestEntry, 0, len(names))
	conflicts := make([]Conflict, 0)
	var requiredBytes int64
	for _, source := range names {
		if err := ctx.Err(); err != nil {
			return nil, nil, 0, err
		}
		path := filepath.Join(s.dataRoot, source.name)
		entry, exists, err := snapshotFile(path, source.name, source.kind)
		if err != nil {
			return nil, nil, 0, err
		}
		if !exists {
			continue
		}
		manifest = append(manifest, entry)
		requiredBytes += entry.Size
		if _, err := os.Lstat(filepath.Join(s.dataRoot, entry.RetirePath)); err == nil {
			conflicts = append(conflicts, Conflict{
				Code: "retire_target_exists", Scope: entry.Scope, Path: entry.RetirePath,
				Detail: "remove or archive the existing .needrm target before preparing again",
			})
		} else if !os.IsNotExist(err) {
			return nil, nil, 0, fmt.Errorf("inspect retirement target %s: %w", entry.RetirePath, err)
		}
	}
	sort.Slice(manifest, func(i, j int) bool { return manifest[i].Path < manifest[j].Path })
	sort.Slice(conflicts, func(i, j int) bool { return conflicts[i].Path < conflicts[j].Path })
	return manifest, conflicts, requiredBytes, nil
}

func snapshotFile(path, relative, kind string) (ManifestEntry, bool, error) {
	before, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ManifestEntry{}, false, nil
		}
		return ManifestEntry{}, false, fmt.Errorf("inspect migration source %s: %w", relative, err)
	}
	if !before.Mode().IsRegular() {
		return ManifestEntry{}, false, fmt.Errorf("migration source %s is not a regular file", relative)
	}
	file, err := os.Open(path)
	if err != nil {
		return ManifestEntry{}, false, fmt.Errorf("open migration source %s: %w", relative, err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil {
		return ManifestEntry{}, false, fmt.Errorf("hash migration source %s: %w", relative, copyErr)
	}
	if closeErr != nil {
		return ManifestEntry{}, false, fmt.Errorf("close migration source %s: %w", relative, closeErr)
	}
	after, err := os.Lstat(path)
	if err != nil {
		return ManifestEntry{}, false, fmt.Errorf("reinspect migration source %s: %w", relative, err)
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return ManifestEntry{}, false, fmt.Errorf("migration source %s changed while preparing", relative)
	}
	return ManifestEntry{
		Scope: "data", Path: filepath.ToSlash(relative), Kind: kind,
		Size: after.Size(), ModTime: after.ModTime().UTC(), SHA256: hex.EncodeToString(hash.Sum(nil)),
		RetirePath: filepath.ToSlash(relative) + ".needrm",
	}, true, nil
}

func hashPlan(plan Plan) (string, error) {
	plan.PlanHash = ""
	data, err := json.Marshal(plan)
	if err != nil {
		return "", fmt.Errorf("marshal migration plan for hash: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func (s *Service) loadPlan() (*Plan, error) {
	plan, err := s.loadPlanFile()
	if err != nil {
		return nil, err
	}
	marker, err := s.loadMarker()
	if err != nil {
		return nil, err
	}
	if marker.MigrationID != plan.MigrationID {
		return nil, errors.New("migration plan does not belong to the active marker")
	}
	return plan, nil
}

func (s *Service) loadPlanFile() (*Plan, error) {
	var plan Plan
	if err := readJSON(filepath.Join(s.controlRoot, planFileName), &plan); err != nil {
		return nil, err
	}
	hash, err := hashPlan(plan)
	if err != nil {
		return nil, err
	}
	if hash != plan.PlanHash {
		return nil, errors.New("migration plan hash is invalid")
	}
	if plan.Version != 1 {
		return nil, errors.New("unsupported migration plan version")
	}
	if err := validatePlanManifest(plan.Manifest); err != nil {
		return nil, err
	}
	if err := validateMigrationID(plan.MigrationID); err != nil {
		return nil, err
	}
	return &plan, nil
}

func validatePlanManifest(entries []ManifestEntry) error {
	allowed := map[string]string{
		"config.json": "config", "models.json": "models", "api_keys.txt": "api_keys",
		"data.db": "database", "data.db-wal": "database_wal", "data.db-shm": "database_shm",
		"nodes.json": "request_nodes", "node_health.json": "node_health",
		"subscriptions.json": "subscriptions", "proxy_url_candidates.json": "global_proxies",
		".rules_agreed": "state", "agreed-rules-docker.txt": "state",
	}
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		kind, ok := allowed[entry.Path]
		if !ok || entry.Kind != kind || entry.Scope != "data" || entry.RetirePath != entry.Path+".needrm" {
			return fmt.Errorf("unexpected migration manifest entry: %s", entry.Path)
		}
		if _, err := safeJoin(".", entry.Path); err != nil {
			return fmt.Errorf("invalid migration manifest path %q: %w", entry.Path, err)
		}
		if _, err := safeJoin(".", entry.RetirePath); err != nil {
			return fmt.Errorf("invalid retirement path %q: %w", entry.RetirePath, err)
		}
		if _, duplicate := seen[entry.Path]; duplicate {
			return fmt.Errorf("duplicate migration manifest entry: %s", entry.Path)
		}
		seen[entry.Path] = struct{}{}
	}
	return nil
}

func (s *Service) validatePlan(ctx context.Context, submittedHash string, now time.Time) (*Plan, error) {
	plan, err := s.loadPlan()
	if err != nil {
		return nil, err
	}
	if submittedHash == "" || submittedHash != plan.PlanHash {
		return nil, errors.New("submitted migration plan hash does not match")
	}
	if !now.Before(plan.ExpiresAt) {
		return nil, ErrPlanExpired
	}
	if len(plan.Conflicts) > 0 {
		return nil, ErrPlanConflict
	}
	for _, expected := range plan.Manifest {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		actual, exists, err := snapshotFile(
			filepath.Join(s.dataRoot, filepath.FromSlash(expected.Path)), expected.Path, expected.Kind,
		)
		if err != nil || !exists || actual.Size != expected.Size ||
			!actual.ModTime.Equal(expected.ModTime) || actual.SHA256 != expected.SHA256 {
			return nil, fmt.Errorf("%w: %s", ErrSourceChanged, expected.Path)
		}
		if _, err := os.Lstat(filepath.Join(s.dataRoot, filepath.FromSlash(expected.RetirePath))); err == nil {
			return nil, fmt.Errorf("%w: retirement target %s now exists", ErrPlanConflict, expected.RetirePath)
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("inspect retirement target %s: %w", expected.RetirePath, err)
		}
	}
	return plan, nil
}
