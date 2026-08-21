package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/bsfdsagfadg/vertex/internal/migration/legacy"
	"github.com/bsfdsagfadg/vertex/internal/repository"
)

type StagedFile struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type StagedResult struct {
	Version     int               `json:"version"`
	MigrationID string            `json:"migration_id"`
	PlanHash    string            `json:"plan_hash"`
	Files       []StagedFile      `json:"files"`
	Counts      repository.Counts `json:"counts"`
	ResultHash  string            `json:"result_hash"`
}

func (s *Service) stage(ctx context.Context, plan *Plan) (StagedResult, error) {
	stageRoot, err := s.stagingRoot(plan.MigrationID)
	if err != nil {
		return StagedResult{}, err
	}
	if err := os.MkdirAll(stageRoot, 0o700); err != nil {
		return StagedResult{}, fmt.Errorf("create V2 staging directory: %w", err)
	}
	for _, name := range []string{"config.json", "models.json", "api_keys.txt", "data.db", "data.db-wal", "data.db-shm"} {
		if err := os.Remove(filepath.Join(stageRoot, name)); err != nil && !os.IsNotExist(err) {
			return StagedResult{}, fmt.Errorf("reset staged output %s: %w", name, err)
		}
	}
	stateRoot, err := safeJoin(stageRoot, "state")
	if err != nil {
		return StagedResult{}, err
	}
	if err := os.RemoveAll(stateRoot); err != nil {
		return StagedResult{}, fmt.Errorf("reset staged state: %w", err)
	}
	configData, err := legacy.ConvertConfig(filepath.Join(s.dataRoot, "config.json"))
	if err != nil {
		return StagedResult{}, err
	}
	if err := writeAtomic(filepath.Join(stageRoot, "config.json"), configData); err != nil {
		return StagedResult{}, err
	}
	modelsData, err := legacy.ConvertModels(filepath.Join(s.dataRoot, "models.json"))
	if err != nil {
		return StagedResult{}, err
	}
	if err := writeAtomic(filepath.Join(stageRoot, "models.json"), modelsData); err != nil {
		return StagedResult{}, err
	}
	if err := copyOptionalFile(filepath.Join(s.dataRoot, "api_keys.txt"), filepath.Join(stageRoot, "api_keys.txt")); err != nil {
		return StagedResult{}, err
	}
	if err := stageLegacyState(s.dataRoot, stageRoot); err != nil {
		return StagedResult{}, err
	}
	snapshot, err := legacy.BuildSnapshot(ctx, s.dataRoot)
	if err != nil {
		return StagedResult{}, err
	}
	repo, err := repository.Open(filepath.Join(stageRoot, "data.db"))
	if err != nil {
		return StagedResult{}, err
	}
	closed := false
	defer func() {
		if !closed {
			_ = repo.Close()
		}
	}()
	if err := repo.ImportSnapshot(ctx, snapshot); err != nil {
		return StagedResult{}, err
	}
	counts, err := repo.Verify(ctx)
	if err != nil {
		return StagedResult{}, err
	}
	if err := repo.Checkpoint(ctx); err != nil {
		return StagedResult{}, err
	}
	if err := repo.Close(); err != nil {
		return StagedResult{}, fmt.Errorf("close staged repository: %w", err)
	}
	closed = true
	files, err := snapshotStagedFiles(stageRoot)
	if err != nil {
		return StagedResult{}, err
	}
	result := StagedResult{
		Version: 1, MigrationID: plan.MigrationID, PlanHash: plan.PlanHash,
		Files: files, Counts: counts,
	}
	result.ResultHash, err = hashStagedResult(result)
	if err != nil {
		return StagedResult{}, err
	}
	if err := writeJSONAtomic(filepath.Join(s.controlRoot, "staged-result.json"), &result); err != nil {
		return StagedResult{}, err
	}
	return result, nil
}

func (s *Service) loadStagedResult(plan *Plan) (StagedResult, error) {
	var result StagedResult
	if err := readJSON(filepath.Join(s.controlRoot, "staged-result.json"), &result); err != nil {
		return StagedResult{}, err
	}
	if result.Version != 1 || result.MigrationID != plan.MigrationID || result.PlanHash != plan.PlanHash {
		return StagedResult{}, fmt.Errorf("staged result does not belong to the frozen migration plan")
	}
	hash, err := hashStagedResult(result)
	if err != nil {
		return StagedResult{}, err
	}
	if hash != result.ResultHash {
		return StagedResult{}, fmt.Errorf("staged result hash is invalid")
	}
	if err := validateStagedFiles(result.Files); err != nil {
		return StagedResult{}, err
	}
	return result, nil
}

func (s *Service) verifyStage(ctx context.Context, plan *Plan, result StagedResult) error {
	if result.MigrationID != plan.MigrationID || result.PlanHash != plan.PlanHash {
		return fmt.Errorf("staged result does not belong to the frozen migration plan")
	}
	if err := validateStagedFiles(result.Files); err != nil {
		return err
	}
	hash, err := hashStagedResult(result)
	if err != nil || hash != result.ResultHash {
		return fmt.Errorf("staged result hash is invalid")
	}
	stageRoot, err := s.stagingRoot(plan.MigrationID)
	if err != nil {
		return err
	}
	for _, expected := range result.Files {
		path, err := safeJoin(stageRoot, expected.Path)
		if err != nil {
			return err
		}
		actual, exists, err := snapshotFile(
			path, expected.Path, expected.Kind,
		)
		if err != nil || !exists || actual.Size != expected.Size || actual.SHA256 != expected.SHA256 {
			return fmt.Errorf("staged file verification failed: %s", expected.Path)
		}
	}
	for _, name := range []string{"config.json", "models.json"} {
		data, err := os.ReadFile(filepath.Join(stageRoot, name))
		if err != nil {
			return err
		}
		var value any
		if err := json.Unmarshal(data, &value); err != nil {
			return fmt.Errorf("verify staged %s: %w", name, err)
		}
	}
	repo, err := repository.OpenReadOnly(filepath.Join(stageRoot, "data.db"))
	if err != nil {
		return err
	}
	defer repo.Close()
	counts, err := repo.Verify(ctx)
	if err != nil {
		return err
	}
	if counts != result.Counts {
		return fmt.Errorf("staged repository counts changed during verification")
	}
	return nil
}

func (s *Service) stagingRoot(migrationID string) (string, error) {
	if err := validateMigrationID(migrationID); err != nil {
		return "", err
	}
	return safeJoin(filepath.Join(s.controlRoot, "staging"), migrationID)
}

func hashStagedResult(result StagedResult) (string, error) {
	result.ResultHash = ""
	data, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("marshal staged result for hash: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func validateStagedFiles(files []StagedFile) error {
	allowed := map[string]string{
		"config.json": "config", "models.json": "models", "data.db": "database",
		"api_keys.txt": "api_keys", "tool-state.key": "state", "state/.rules_agreed": "state",
		"state/agreed-rules-docker.txt": "state",
	}
	required := map[string]bool{
		"config.json": false, "models.json": false, "data.db": false, "tool-state.key": false,
	}
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		if _, err := safeJoin(".", file.Path); err != nil {
			return fmt.Errorf("invalid staged path %q: %w", file.Path, err)
		}
		expectedKind, ok := allowed[file.Path]
		decodedHash, hashErr := hex.DecodeString(file.SHA256)
		if !ok || file.Kind != expectedKind || file.Size < 0 || hashErr != nil || len(decodedHash) != sha256.Size {
			return fmt.Errorf("unexpected staged file metadata: %s", file.Path)
		}
		if _, duplicate := seen[file.Path]; duplicate {
			return fmt.Errorf("duplicate staged file: %s", file.Path)
		}
		seen[file.Path] = struct{}{}
		if _, ok := required[file.Path]; ok {
			required[file.Path] = true
		}
	}
	for path, present := range required {
		if !present {
			return fmt.Errorf("required staged file is missing: %s", path)
		}
	}
	return nil
}

func copyOptionalFile(source, target string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s for staging: %w", filepath.Base(source), err)
	}
	return writeAtomic(target, data)
}

func stageLegacyState(dataRoot, stageRoot string) error {
	mappings := []struct{ source, target string }{
		{".rules_agreed", filepath.Join("state", ".rules_agreed")},
		{"agreed-rules-docker.txt", filepath.Join("state", "agreed-rules-docker.txt")},
	}
	for _, mapping := range mappings {
		activeTarget := filepath.Join(dataRoot, mapping.target)
		if _, err := os.Stat(activeTarget); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := copyOptionalFile(filepath.Join(dataRoot, mapping.source), filepath.Join(stageRoot, mapping.target)); err != nil {
			return err
		}
	}
	return nil
}

func snapshotStagedFiles(root string) ([]StagedFile, error) {
	files := make([]StagedFile, 0)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("staged path is not a regular file: %s", path)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		files = append(files, StagedFile{
			Path: filepath.ToSlash(relative), Kind: stagedKind(relative), Size: int64(len(data)),
			SHA256: hex.EncodeToString(sum[:]),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("snapshot staged V2 files: %w", err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func stagedKind(path string) string {
	switch filepath.ToSlash(path) {
	case "config.json":
		return "config"
	case "models.json":
		return "models"
	case "data.db":
		return "database"
	case "api_keys.txt":
		return "api_keys"
	default:
		return "state"
	}
}
