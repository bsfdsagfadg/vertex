package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const rollbackPlanFileName = "rollback-plan.json"

// PrepareRollback freezes the exact V2 files to archive and V1 .needrm files
// to restore. It is read-only with respect to the active data namespace.
func (s *Service) PrepareRollback(ctx context.Context) (RollbackPlan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return RollbackPlan{}, err
	}
	status, err := s.loadStatus()
	if err != nil {
		return RollbackPlan{}, err
	}
	if status.State != StateCompleted && status.State != StateRollbackReady {
		return RollbackPlan{}, fmt.Errorf("rollback prepare requires Completed state, got %s", status.State)
	}
	if _, err := os.Stat(s.MarkerPath()); err == nil {
		return RollbackPlan{}, errors.New("cannot prepare rollback while migration marker is active")
	} else if !os.IsNotExist(err) {
		return RollbackPlan{}, err
	}
	forward, err := s.loadPlanFile()
	if err != nil {
		return RollbackPlan{}, err
	}
	staged, err := s.loadStagedResult(forward)
	if err != nil {
		return RollbackPlan{}, err
	}
	report, err := s.loadVerifiedReport(forward)
	if err != nil {
		return RollbackPlan{}, err
	}
	now := s.now().UTC()
	plan := RollbackPlan{
		Version: 1, MigrationID: forward.MigrationID, CreatedAt: now,
		ExpiresAt: now.Add(s.planTTL),
	}
	activePaths := make(map[string]struct{}, len(staged.Files))
	for _, expected := range staged.Files {
		activePaths[expected.Path] = struct{}{}
		entry, conflict, err := s.snapshotRollbackEntry(
			expected.Path, expected.Path+".v2rollback", expected.Kind, expected.Size, expected.SHA256, false,
		)
		if err != nil {
			return RollbackPlan{}, err
		}
		plan.ActiveV2 = append(plan.ActiveV2, entry)
		if conflict != nil {
			plan.Conflicts = append(plan.Conflicts, *conflict)
		}
	}
	for _, legacy := range report.Legacy {
		_, targetMovesFirst := activePaths[legacy.Path]
		entry, conflict, err := s.snapshotRollbackEntry(
			legacy.RetirePath, legacy.Path, legacy.Kind, legacy.Size, legacy.SHA256, targetMovesFirst,
		)
		if err != nil {
			return RollbackPlan{}, err
		}
		plan.LegacyV1 = append(plan.LegacyV1, entry)
		if conflict != nil {
			plan.Conflicts = append(plan.Conflicts, *conflict)
		}
		if _, movedFirst := activePaths[legacy.Path]; !movedFirst {
			restoreTarget, joinErr := safeJoin(s.dataRoot, legacy.Path)
			if joinErr != nil {
				return RollbackPlan{}, joinErr
			}
			if _, err := os.Lstat(restoreTarget); err == nil {
				plan.Conflicts = append(plan.Conflicts, Conflict{
					Code: "rollback_restore_target_exists", Scope: "legacy_v1", Path: legacy.Path,
					Detail: "rollback would overwrite an active path not owned by the V2 archive plan",
				})
			} else if !os.IsNotExist(err) {
				return RollbackPlan{}, err
			}
		}
	}
	sort.Slice(plan.ActiveV2, func(i, j int) bool { return plan.ActiveV2[i].Path < plan.ActiveV2[j].Path })
	sort.Slice(plan.LegacyV1, func(i, j int) bool { return plan.LegacyV1[i].Path < plan.LegacyV1[j].Path })
	sort.Slice(plan.Conflicts, func(i, j int) bool {
		if plan.Conflicts[i].Path == plan.Conflicts[j].Path {
			return plan.Conflicts[i].Code < plan.Conflicts[j].Code
		}
		return plan.Conflicts[i].Path < plan.Conflicts[j].Path
	})
	plan.PlanHash, err = hashRollbackPlan(plan)
	if err != nil {
		return RollbackPlan{}, err
	}
	if err := writeJSONAtomic(filepath.Join(s.controlRoot, rollbackPlanFileName), &plan); err != nil {
		return RollbackPlan{}, err
	}
	status.State = StateRollbackReady
	status.Required = false
	status.Recoverable = false
	status.RollbackPlan = &plan
	status.LastError = ""
	status.FailedFrom = ""
	status.UpdatedAt = now
	if len(plan.Conflicts) > 0 {
		status.LastError = ErrPlanConflict.Error()
	}
	if err := s.saveStatus(status); err != nil {
		return RollbackPlan{}, err
	}
	return plan, nil
}

func (s *Service) snapshotRollbackEntry(path, target, kind string, size int64, digest string, targetMovesFirst bool) (RollbackEntry, *Conflict, error) {
	source, err := safeJoin(s.dataRoot, path)
	if err != nil {
		return RollbackEntry{}, nil, err
	}
	if _, err := safeJoin(s.dataRoot, target); err != nil {
		return RollbackEntry{}, nil, err
	}
	entry := RollbackEntry{
		Scope: "data", Path: path, TargetPath: target, Kind: kind, Size: size, SHA256: digest,
	}
	actual, exists, err := snapshotFile(source, path, kind)
	if err != nil {
		return RollbackEntry{}, nil, err
	}
	if !exists || actual.Size != size || actual.SHA256 != digest {
		return entry, &Conflict{
			Code: "rollback_source_changed", Scope: "data", Path: path,
			Detail: "rollback source is missing or differs from the verified migration artifact",
		}, nil
	}
	entry.ModTime = actual.ModTime
	if !targetMovesFirst {
		targetPath, _ := safeJoin(s.dataRoot, target)
		if _, err := os.Lstat(targetPath); err == nil {
			return entry, &Conflict{
				Code: "rollback_target_exists", Scope: "data", Path: target,
				Detail: "remove or archive the existing rollback target; it will never be overwritten",
			}, nil
		} else if !os.IsNotExist(err) {
			return RollbackEntry{}, nil, err
		}
	}
	return entry, nil, nil
}

func (s *Service) loadVerifiedReport(plan *Plan) (*Report, error) {
	var report Report
	if err := readJSON(filepath.Join(s.controlRoot, "migration-report.json"), &report); err != nil {
		return nil, err
	}
	if report.Version != 1 || report.MigrationID != plan.MigrationID || report.PlanHash != plan.PlanHash || report.CompletedAt == nil {
		return nil, errors.New("migration report does not describe a completed frozen plan")
	}
	if len(report.Legacy) != len(plan.Manifest) {
		return nil, errors.New("migration report legacy manifest differs from the frozen plan")
	}
	for i := range report.Legacy {
		if report.Legacy[i] != plan.Manifest[i] {
			return nil, errors.New("migration report legacy manifest differs from the frozen plan")
		}
	}
	return &report, nil
}

func hashRollbackPlan(plan RollbackPlan) (string, error) {
	plan.PlanHash = ""
	data, err := json.Marshal(plan)
	if err != nil {
		return "", fmt.Errorf("marshal rollback plan for hash: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func (s *Service) loadRollbackPlan() (*RollbackPlan, error) {
	var plan RollbackPlan
	if err := readJSON(filepath.Join(s.controlRoot, rollbackPlanFileName), &plan); err != nil {
		return nil, err
	}
	if plan.Version != 1 || validateMigrationID(plan.MigrationID) != nil {
		return nil, errors.New("invalid rollback plan identity")
	}
	hash, err := hashRollbackPlan(plan)
	if err != nil || hash != plan.PlanHash {
		return nil, errors.New("rollback plan hash is invalid")
	}
	if err := validateRollbackEntries(plan.ActiveV2, ".v2rollback"); err != nil {
		return nil, err
	}
	if err := validateRollbackEntries(plan.LegacyV1, ""); err != nil {
		return nil, err
	}
	forward, err := s.loadPlanFile()
	if err != nil {
		return nil, err
	}
	staged, err := s.loadStagedResult(forward)
	if err != nil {
		return nil, err
	}
	report, err := s.loadVerifiedReport(forward)
	if err != nil {
		return nil, err
	}
	if plan.MigrationID != forward.MigrationID || len(plan.ActiveV2) != len(staged.Files) || len(plan.LegacyV1) != len(report.Legacy) {
		return nil, errors.New("rollback plan does not match the completed migration artifacts")
	}
	for index, file := range staged.Files {
		entry := plan.ActiveV2[index]
		if entry.Path != file.Path || entry.TargetPath != file.Path+".v2rollback" || entry.Kind != file.Kind || entry.Size != file.Size || entry.SHA256 != file.SHA256 {
			return nil, errors.New("rollback V2 manifest does not match staged results")
		}
	}
	for index, legacy := range report.Legacy {
		entry := plan.LegacyV1[index]
		if entry.Path != legacy.RetirePath || entry.TargetPath != legacy.Path || entry.Kind != legacy.Kind || entry.Size != legacy.Size || entry.SHA256 != legacy.SHA256 {
			return nil, errors.New("rollback V1 manifest does not match migration report")
		}
	}
	return &plan, nil
}

func validateRollbackEntries(entries []RollbackEntry, targetSuffix string) error {
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.Scope != "data" || entry.Path == "" || entry.TargetPath == "" || entry.Size < 0 || entry.ModTime.IsZero() {
			return errors.New("invalid rollback entry metadata")
		}
		if _, err := safeJoin(".", entry.Path); err != nil {
			return err
		}
		if _, err := safeJoin(".", entry.TargetPath); err != nil {
			return err
		}
		if targetSuffix != "" && entry.TargetPath != entry.Path+targetSuffix {
			return fmt.Errorf("invalid V2 rollback target: %s", entry.TargetPath)
		}
		if targetSuffix == "" && (!strings.HasSuffix(entry.Path, ".needrm") || strings.TrimSuffix(entry.Path, ".needrm") != entry.TargetPath) {
			return fmt.Errorf("invalid V1 restore target: %s", entry.TargetPath)
		}
		decoded, err := hex.DecodeString(entry.SHA256)
		if err != nil || len(decoded) != sha256.Size {
			return fmt.Errorf("invalid rollback digest: %s", entry.Path)
		}
		if _, duplicate := seen[entry.Path]; duplicate {
			return fmt.Errorf("duplicate rollback entry: %s", entry.Path)
		}
		seen[entry.Path] = struct{}{}
	}
	return nil
}

func (s *Service) ApplyRollback(ctx context.Context, submittedHash string) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status, err := s.loadStatus()
	if err != nil {
		return Status{}, err
	}
	if status.State != StateRollbackReady {
		return Status{}, fmt.Errorf("rollback apply requires RollbackReady state, got %s", status.State)
	}
	plan, err := s.loadRollbackPlan()
	if err != nil {
		return Status{}, err
	}
	if submittedHash == "" || submittedHash != plan.PlanHash || !s.now().UTC().Before(plan.ExpiresAt) {
		return Status{}, errors.New("submitted rollback plan is missing, expired, or changed")
	}
	if len(plan.Conflicts) > 0 {
		return Status{}, ErrPlanConflict
	}
	if err := s.validateRollbackSources(ctx, plan); err != nil {
		return Status{}, err
	}
	if status.Marker == nil || status.Marker.MigrationID != plan.MigrationID {
		return Status{}, errors.New("rollback status has no matching migration marker identity")
	}
	if err := s.createMarker(status.Marker); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return Status{}, err
		}
		existing, loadErr := s.loadMarker()
		if loadErr != nil || existing.MigrationID != plan.MigrationID {
			return Status{}, errors.New("active migration marker does not match rollback plan")
		}
	}
	return s.applyRollbackLocked(ctx, status, plan)
}

func (s *Service) ResumeRollback(ctx context.Context, submittedHash string) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status, err := s.loadStatus()
	if err != nil {
		return Status{}, err
	}
	if status.State != StateFailedRecoverable || status.FailedFrom != StateRollingBack {
		return Status{}, errors.New("rollback resume requires a recoverable RollingBack failure")
	}
	plan, err := s.loadRollbackPlan()
	if err != nil {
		return Status{}, err
	}
	if submittedHash == "" || submittedHash != plan.PlanHash {
		return Status{}, errors.New("submitted rollback plan hash does not match")
	}
	return s.applyRollbackLocked(ctx, status, plan)
}

func (s *Service) validateRollbackSources(ctx context.Context, plan *RollbackPlan) error {
	activePaths := make(map[string]struct{}, len(plan.ActiveV2))
	for _, entry := range plan.ActiveV2 {
		activePaths[entry.Path] = struct{}{}
	}
	entries := append(append([]RollbackEntry{}, plan.ActiveV2...), plan.LegacyV1...)
	for index, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		path, err := safeJoin(s.dataRoot, entry.Path)
		if err != nil {
			return err
		}
		actual, exists, err := snapshotFile(path, entry.Path, entry.Kind)
		if err != nil || !exists || actual.Size != entry.Size || actual.SHA256 != entry.SHA256 || !actual.ModTime.Equal(entry.ModTime) {
			return fmt.Errorf("rollback source changed after prepare: %s", entry.Path)
		}
		target, err := safeJoin(s.dataRoot, entry.TargetPath)
		if err != nil {
			return err
		}
		_, targetMovesFirst := activePaths[entry.TargetPath]
		isLegacy := index >= len(plan.ActiveV2)
		_, statErr := os.Lstat(target)
		if statErr == nil {
			if isLegacy && targetMovesFirst {
				continue
			}
			return fmt.Errorf("rollback target now exists: %s", entry.TargetPath)
		}
		if !os.IsNotExist(statErr) {
			return statErr
		}
	}
	return nil
}

func (s *Service) applyRollbackLocked(ctx context.Context, status *Status, plan *RollbackPlan) (Status, error) {
	status.State = StateRollingBack
	status.Required = true
	status.Recoverable = true
	status.FailedFrom = StateRollingBack
	status.LastError = ""
	status.UpdatedAt = s.now().UTC()
	if err := s.saveStatus(status); err != nil {
		return *status, err
	}
	fail := func(cause error) (Status, error) {
		status.State = StateFailedRecoverable
		status.Required = true
		status.Recoverable = true
		status.FailedFrom = StateRollingBack
		status.LastError = cause.Error()
		status.UpdatedAt = s.now().UTC()
		_ = s.saveStatus(status)
		_ = s.appendJournal(JournalEntry{
			At: status.UpdatedAt, State: StateFailedRecoverable,
			Operation: "rollback", Status: "failed", Error: cause.Error(),
		})
		return *status, cause
	}
	for _, entry := range plan.ActiveV2 {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if err := s.rollbackRename(entry, "archive_v2"); err != nil {
			return fail(err)
		}
	}
	for _, entry := range plan.LegacyV1 {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if err := s.rollbackRename(entry, "restore_v1"); err != nil {
			return fail(err)
		}
	}
	for _, entry := range append(append([]RollbackEntry{}, plan.ActiveV2...), plan.LegacyV1...) {
		target, err := safeJoin(s.dataRoot, entry.TargetPath)
		if err != nil {
			return fail(err)
		}
		matches, err := fileMatches(target, entry.Size, entry.SHA256)
		if err != nil || !matches {
			return fail(fmt.Errorf("rollback verification failed: %s", entry.TargetPath))
		}
	}
	status.State = StateRollbackPrepared
	status.Required = true
	status.Recoverable = false
	status.FailedFrom = ""
	status.LastError = ""
	status.UpdatedAt = s.now().UTC()
	if err := s.saveStatus(status); err != nil {
		return fail(err)
	}
	if err := s.appendJournal(JournalEntry{
		At: status.UpdatedAt, State: StateRollbackPrepared,
		Operation: "rollback", Status: "completed",
	}); err != nil {
		return fail(err)
	}
	return *status, nil
}

func (s *Service) rollbackRename(entry RollbackEntry, operation string) error {
	source, err := safeJoin(s.dataRoot, entry.Path)
	if err != nil {
		return err
	}
	target, err := safeJoin(s.dataRoot, entry.TargetPath)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(target); err == nil {
		matches, matchErr := fileMatches(target, entry.Size, entry.SHA256)
		if matchErr != nil || !matches {
			return fmt.Errorf("rollback target conflict: %s", entry.TargetPath)
		}
		if _, sourceErr := os.Lstat(source); os.IsNotExist(sourceErr) {
			return nil
		}
		return fmt.Errorf("both rollback source and target exist: %s", entry.Path)
	} else if !os.IsNotExist(err) {
		return err
	}
	actual, exists, err := snapshotFile(source, entry.Path, entry.Kind)
	if err != nil || !exists || actual.Size != entry.Size || actual.SHA256 != entry.SHA256 || !actual.ModTime.Equal(entry.ModTime) {
		return fmt.Errorf("rollback source no longer matches plan: %s", entry.Path)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	if err := s.appendJournal(JournalEntry{
		At: s.now().UTC(), State: StateRollingBack, Operation: operation,
		Scope: entry.Scope, From: entry.Path, To: entry.TargetPath, Status: "intent",
	}); err != nil {
		return err
	}
	if err := os.Rename(source, target); err != nil {
		return fmt.Errorf("%s %s: %w", operation, entry.Path, err)
	}
	if err := syncDirectory(filepath.Dir(source)); err != nil {
		return err
	}
	if filepath.Dir(target) != filepath.Dir(source) {
		if err := syncDirectory(filepath.Dir(target)); err != nil {
			return err
		}
	}
	return s.appendJournal(JournalEntry{
		At: s.now().UTC(), State: StateRollingBack, Operation: operation,
		Scope: entry.Scope, From: entry.Path, To: entry.TargetPath, Status: "completed",
	})
}
