package migration

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/repository"
)

func (s *Service) Apply(ctx context.Context, submittedHash string) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now().UTC()
	plan, err := s.validatePlan(ctx, submittedHash, now)
	if err != nil {
		return Status{}, err
	}
	status, err := s.loadStatus()
	if err != nil {
		return Status{}, err
	}
	if status.State != StatePrepared {
		return Status{}, fmt.Errorf("migration apply requires Prepared state, got %s", status.State)
	}
	setState := func(state State) error {
		status.State = state
		status.Required = true
		status.UpdatedAt = s.now().UTC()
		status.LastError = ""
		return s.saveStatus(status)
	}
	fail := func(cause error) (Status, error) {
		failedFrom := status.State
		status.State = StateFailedRecoverable
		status.Required = true
		status.Recoverable = true
		status.LastError = cause.Error()
		status.FailedFrom = failedFrom
		status.UpdatedAt = s.now().UTC()
		_ = s.saveStatus(status)
		_ = s.appendJournal(JournalEntry{At: status.UpdatedAt, State: status.State, Operation: "apply", Status: "failed", Error: cause.Error()})
		return *status, cause
	}
	if err := setState(StateMigrating); err != nil {
		return fail(err)
	}
	staged, err := s.stage(ctx, plan)
	if err != nil {
		return fail(err)
	}
	if err := s.appendJournal(JournalEntry{At: s.now().UTC(), State: StateMigrating, Operation: "stage_v2", Status: "completed"}); err != nil {
		return fail(err)
	}
	if err := setState(StateVerifying); err != nil {
		return fail(err)
	}
	if err := s.verifyStage(ctx, plan, staged); err != nil {
		return fail(err)
	}
	report := Report{
		Version: 1, MigrationID: plan.MigrationID, StartedAt: now,
		PlanHash: plan.PlanHash, Legacy: plan.Manifest,
	}
	for _, entry := range plan.Manifest {
		report.ManualRestore = append(report.ManualRestore, JournalEntry{
			State: StateRetiringLegacy, Operation: "rename", Scope: entry.Scope,
			From: entry.RetirePath, To: entry.Path, Status: "manual_restore",
		})
	}
	if err := writeJSONAtomic(filepath.Join(s.controlRoot, "migration-report.json"), &report); err != nil {
		return fail(err)
	}
	if err := setState(StateRetiringLegacy); err != nil {
		return fail(err)
	}
	for _, entry := range plan.Manifest {
		if err := s.retire(entry); err != nil {
			return fail(err)
		}
	}
	if err := setState(StatePublishing); err != nil {
		return fail(err)
	}
	for _, file := range staged.Files {
		if err := s.publish(plan.MigrationID, file); err != nil {
			return fail(err)
		}
	}
	if err := s.verifyPublished(ctx, staged); err != nil {
		return fail(err)
	}
	completedAt := s.now().UTC()
	report.CompletedAt = &completedAt
	if err := writeJSONAtomic(filepath.Join(s.controlRoot, "migration-report.json"), &report); err != nil {
		return fail(err)
	}
	if err := s.finalize(status, completedAt, "apply"); err != nil {
		return fail(err)
	}
	return *status, nil
}

func (s *Service) finalize(status *Status, completedAt time.Time, operation string) error {
	status.State = StateFinalizing
	status.Required = true
	status.Recoverable = true
	status.FailedFrom = StateFinalizing
	status.LastError = ""
	status.UpdatedAt = completedAt
	if err := s.saveStatus(status); err != nil {
		return err
	}
	if err := s.appendJournal(JournalEntry{
		At: completedAt, State: StateFinalizing, Operation: operation + "_finalize", Status: "intent",
	}); err != nil {
		return err
	}
	if err := s.removeMigrationToken(); err != nil {
		return err
	}
	if err := os.Remove(s.MarkerPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove migration-required marker: %w", err)
	}
	if err := syncDirectory(s.controlRoot); err != nil {
		return err
	}
	if err := s.appendJournal(JournalEntry{
		At: s.now().UTC(), State: StateCompleted, Operation: operation + "_finalize", Status: "completed",
	}); err != nil {
		return err
	}
	status.State = StateCompleted
	status.Required = false
	status.Recoverable = false
	status.LastError = ""
	status.FailedFrom = ""
	status.UpdatedAt = s.now().UTC()
	if err := s.saveStatus(status); err != nil {
		return err
	}
	return nil
}

func (s *Service) removeMigrationToken() error {
	path := filepath.Join(s.controlRoot, ".migration-token")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove migration token: %w", err)
	}
	return nil
}

func (s *Service) retire(entry ManifestEntry) error {
	source, err := safeJoin(s.dataRoot, entry.Path)
	if err != nil {
		return err
	}
	target, err := safeJoin(s.dataRoot, entry.RetirePath)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(target); err == nil {
		matches, matchErr := fileMatches(target, entry.Size, entry.SHA256)
		if matchErr != nil || !matches {
			return fmt.Errorf("retirement target conflict: %s", entry.RetirePath)
		}
		if _, sourceErr := os.Lstat(source); os.IsNotExist(sourceErr) {
			return nil
		}
		return fmt.Errorf("both migration source and retirement target exist: %s", entry.Path)
	} else if !os.IsNotExist(err) {
		return err
	}
	if matches, err := fileMatches(source, entry.Size, entry.SHA256); err != nil || !matches {
		return fmt.Errorf("migration source no longer matches manifest: %s", entry.Path)
	}
	if err := s.appendJournal(JournalEntry{
		At: s.now().UTC(), State: StateRetiringLegacy, Operation: "rename", Scope: entry.Scope,
		From: entry.Path, To: entry.RetirePath, Status: "intent",
	}); err != nil {
		return err
	}
	if err := os.Rename(source, target); err != nil {
		return fmt.Errorf("retire legacy %s: %w", entry.Path, err)
	}
	if err := syncDirectory(filepath.Dir(source)); err != nil {
		return err
	}
	return s.appendJournal(JournalEntry{
		At: s.now().UTC(), State: StateRetiringLegacy, Operation: "rename", Scope: entry.Scope,
		From: entry.Path, To: entry.RetirePath, Status: "completed",
	})
}

func (s *Service) publish(migrationID string, file StagedFile) error {
	stageRoot, err := s.stagingRoot(migrationID)
	if err != nil {
		return err
	}
	source, err := safeJoin(stageRoot, file.Path)
	if err != nil {
		return err
	}
	target, err := safeJoin(s.dataRoot, file.Path)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(target); err == nil {
		matches, matchErr := fileMatches(target, file.Size, file.SHA256)
		if matchErr == nil && matches {
			if _, sourceErr := os.Lstat(source); os.IsNotExist(sourceErr) {
				return nil
			}
		}
		return fmt.Errorf("publishing would overwrite active path: %s", file.Path)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	if err := s.appendJournal(JournalEntry{
		At: s.now().UTC(), State: StatePublishing, Operation: "rename", Scope: "data",
		From: filepath.ToSlash(filepath.Join(controlDirName, "staging", migrationID, file.Path)),
		To:   file.Path, Status: "intent",
	}); err != nil {
		return err
	}
	if err := os.Rename(source, target); err != nil {
		return fmt.Errorf("publish V2 %s: %w", file.Path, err)
	}
	if err := syncDirectory(filepath.Dir(source)); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Dir(target)); err != nil {
		return err
	}
	return s.appendJournal(JournalEntry{
		At: s.now().UTC(), State: StatePublishing, Operation: "rename", Scope: "data",
		From: filepath.ToSlash(filepath.Join(controlDirName, "staging", migrationID, file.Path)),
		To:   file.Path, Status: "completed",
	})
}

func (s *Service) verifyPublished(ctx context.Context, staged StagedResult) error {
	for _, expected := range staged.Files {
		path, err := safeJoin(s.dataRoot, expected.Path)
		if err != nil {
			return err
		}
		matches, err := fileMatches(
			path, expected.Size, expected.SHA256,
		)
		if err != nil || !matches {
			return fmt.Errorf("published V2 file verification failed: %s", expected.Path)
		}
	}
	repo, err := repository.OpenReadOnly(filepath.Join(s.dataRoot, "data.db"))
	if err != nil {
		return err
	}
	defer repo.Close()
	counts, err := repo.Verify(ctx)
	if err != nil {
		return err
	}
	if counts != staged.Counts {
		return errors.New("published repository counts differ from staged repository")
	}
	return nil
}

func fileMatches(path string, size int64, expectedHash string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if int64(len(data)) != size {
		return false, nil
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) == expectedHash, nil
}

func (s *Service) appendJournal(entry JournalEntry) error {
	path := filepath.Join(s.controlRoot, "journal.jsonl")
	if err := os.MkdirAll(s.controlRoot, 0o700); err != nil {
		return err
	}
	var lastSequence int64
	readFile, err := os.Open(path)
	if err == nil {
		scanner := bufio.NewScanner(readFile)
		for scanner.Scan() {
			var existing JournalEntry
			if err := json.Unmarshal(scanner.Bytes(), &existing); err != nil {
				_ = readFile.Close()
				return fmt.Errorf("decode migration journal: %w", err)
			}
			if existing.Sequence <= lastSequence {
				_ = readFile.Close()
				return errors.New("migration journal sequence is not strictly increasing")
			}
			lastSequence = existing.Sequence
		}
		if err := scanner.Err(); err != nil {
			_ = readFile.Close()
			return fmt.Errorf("read migration journal: %w", err)
		}
		if err := readFile.Close(); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	entry.Sequence = lastSequence + 1
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open migration journal: %w", err)
	}
	defer file.Close()
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("append migration journal: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync migration journal: %w", err)
	}
	return nil
}
