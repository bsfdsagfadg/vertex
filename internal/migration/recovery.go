package migration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Resume continues the frozen plan after a recoverable failure. Plan expiry is
// intentionally ignored: cutover may already be partial, while the original
// hash plus staged and published digests remain mandatory.
func (s *Service) Resume(ctx context.Context, submittedHash string) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status, err := s.loadStatus()
	if err != nil {
		return Status{}, err
	}
	resumeFrom := status.State
	if status.State == StateFailedRecoverable {
		resumeFrom = status.FailedFrom
	} else if status.State != StateFinalizing {
		return Status{}, fmt.Errorf("migration resume requires FailedRecoverable or Finalizing state")
	}
	switch resumeFrom {
	case StateMigrating, StateVerifying, StateRetiringLegacy, StatePublishing, StateFinalizing:
	default:
		return Status{}, fmt.Errorf("migration cannot automatically resume from %s", resumeFrom)
	}
	fail := func(cause error) (Status, error) {
		status.State = StateFailedRecoverable
		status.Required = true
		status.Recoverable = true
		status.FailedFrom = resumeFrom
		status.LastError = cause.Error()
		status.UpdatedAt = s.now().UTC()
		_ = s.saveStatus(status)
		_ = s.appendJournal(JournalEntry{
			At: status.UpdatedAt, State: StateFailedRecoverable,
			Operation: "resume", Status: "failed", Error: cause.Error(),
		})
		return *status, cause
	}
	if resumeFrom == StateFinalizing {
		if status.Plan == nil || submittedHash == "" || submittedHash != status.Plan.PlanHash {
			return Status{}, fmt.Errorf("submitted migration plan hash does not match")
		}
		if err := s.finalize(status, s.now().UTC(), "resume"); err != nil {
			return fail(err)
		}
		return *status, nil
	}
	plan, err := s.loadPlan()
	if err != nil {
		return Status{}, err
	}
	if submittedHash == "" || submittedHash != plan.PlanHash {
		return Status{}, fmt.Errorf("submitted migration plan hash does not match")
	}
	var staged StagedResult
	if resumeFrom == StateMigrating {
		status.State = StateMigrating
		status.UpdatedAt = s.now().UTC()
		if err := s.saveStatus(status); err != nil {
			return fail(err)
		}
		staged, err = s.stage(ctx, plan)
	} else {
		staged, err = s.loadStagedResult(plan)
	}
	if err != nil {
		return fail(err)
	}
	if resumeFrom == StateMigrating || resumeFrom == StateVerifying {
		status.State = StateVerifying
		status.UpdatedAt = s.now().UTC()
		if err := s.saveStatus(status); err != nil {
			return fail(err)
		}
		if err := s.verifyStage(ctx, plan, staged); err != nil {
			return fail(err)
		}
	}
	if err := s.ensureRecoveryReport(plan); err != nil {
		return fail(err)
	}
	status.State = StateRetiringLegacy
	status.UpdatedAt = s.now().UTC()
	if err := s.saveStatus(status); err != nil {
		return fail(err)
	}
	for _, entry := range plan.Manifest {
		if err := s.retire(entry); err != nil {
			return fail(err)
		}
	}
	resumeFrom = StatePublishing
	status.State = StatePublishing
	status.UpdatedAt = s.now().UTC()
	if err := s.saveStatus(status); err != nil {
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
	var report Report
	if err := readJSON(filepath.Join(s.controlRoot, "migration-report.json"), &report); err != nil {
		return fail(err)
	}
	report.CompletedAt = &completedAt
	if err := writeJSONAtomic(filepath.Join(s.controlRoot, "migration-report.json"), &report); err != nil {
		return fail(err)
	}
	if err := s.finalize(status, completedAt, "resume"); err != nil {
		return fail(err)
	}
	return *status, nil
}

func (s *Service) ensureRecoveryReport(plan *Plan) error {
	path := filepath.Join(s.controlRoot, "migration-report.json")
	var existing Report
	if err := readJSON(path, &existing); err == nil {
		if existing.MigrationID != plan.MigrationID || existing.PlanHash != plan.PlanHash {
			return fmt.Errorf("migration report does not match frozen plan")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	report := Report{
		Version: 1, MigrationID: plan.MigrationID, StartedAt: s.now().UTC(),
		PlanHash: plan.PlanHash, Legacy: plan.Manifest,
	}
	for _, entry := range plan.Manifest {
		report.ManualRestore = append(report.ManualRestore, JournalEntry{
			State: StateRetiringLegacy, Operation: "rename", Scope: entry.Scope,
			From: entry.RetirePath, To: entry.Path, Status: "manual_restore",
		})
	}
	return writeJSONAtomic(path, &report)
}
