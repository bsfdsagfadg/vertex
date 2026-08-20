package migration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const defaultPlanTTL = 15 * time.Minute

type Service struct {
	mu          sync.Mutex
	dataRoot    string
	controlRoot string
	now         func() time.Time
	planTTL     time.Duration
}

func NewService(dataRoot string) (*Service, error) {
	if dataRoot == "" {
		return nil, errors.New("migration data root is empty")
	}
	absRoot, err := filepath.Abs(dataRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve migration data root: %w", err)
	}
	absRoot = filepath.Clean(absRoot)
	return &Service{
		dataRoot:    absRoot,
		controlRoot: filepath.Join(absRoot, controlDirName),
		now:         time.Now,
		planTTL:     defaultPlanTTL,
	}, nil
}

func (s *Service) DataRoot() string { return s.dataRoot }

func (s *Service) MarkerPath() string { return filepath.Join(s.controlRoot, markerFileName) }

func (s *Service) Required() bool {
	_, err := os.Stat(s.MarkerPath())
	return err == nil
}

// InspectAndMark performs the startup read-only layout inspection and, when a
// V1 marker is found, atomically creates the durable business-traffic gate.
func (s *Service) InspectAndMark(ctx context.Context) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inspectAndMarkLocked(ctx)
}

func (s *Service) inspectAndMarkLocked(ctx context.Context) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	layout, err := s.detect(ctx)
	if err != nil {
		return Status{}, err
	}
	now := s.now().UTC()
	marker, err := s.loadMarker()
	if err != nil && !os.IsNotExist(err) {
		return Status{}, err
	}
	if marker == nil && len(layout.Findings) > 0 {
		id, idErr := newMigrationID(now)
		if idErr != nil {
			return Status{}, idErr
		}
		marker = &Marker{
			LayoutVersion: layout.Version,
			DataRootHash:  dataRootFingerprint(s.dataRoot),
			CreatedAt:     now,
			MigrationID:   id,
			FindingCodes:  findingCodes(layout.Findings),
		}
		if err := s.createMarker(marker); err != nil {
			if !errors.Is(err, os.ErrExist) {
				return Status{}, err
			}
			marker, err = s.loadMarker()
			if err != nil {
				return Status{}, err
			}
		}
	}
	if marker == nil {
		return Status{State: StateNotRequired, UpdatedAt: now}, nil
	}
	status, err := s.loadStatus()
	if err != nil && !os.IsNotExist(err) {
		return Status{}, err
	}
	if status == nil {
		status = &Status{
			State:     StateRequired,
			Required:  true,
			Marker:    marker,
			Findings:  layout.Findings,
			UpdatedAt: now,
		}
		if err := s.saveStatus(status); err != nil {
			return Status{}, err
		}
	} else {
		// Marker presence is the sole business-traffic gate. Status can lag a
		// durable marker operation across a crash and must never bypass it.
		status.Required = true
		if status.State == StateCompleted {
			status.State = StateFinalizing
			status.Recoverable = true
			status.FailedFrom = StateFinalizing
			status.LastError = "completion marker still exists; finalization must resume"
		}
		status.Marker = marker
		status.Findings = layout.Findings
		status.UpdatedAt = now
		if err := s.saveStatus(status); err != nil {
			return Status{}, err
		}
	}
	return *status, nil
}

func (s *Service) Status(ctx context.Context) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	return s.inspectAndMarkLocked(ctx)
}

// CurrentStatus is the non-blocking polling view used while Apply owns the
// mutation lock. Status files are atomically replaced, so readers see either
// the previous or next complete state.
func (s *Service) CurrentStatus(ctx context.Context) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	status, err := s.loadStatus()
	if err == nil {
		return *status, nil
	}
	if !os.IsNotExist(err) {
		return Status{}, err
	}
	return s.Status(ctx)
}

func (s *Service) loadMarker() (*Marker, error) {
	var marker Marker
	if err := readJSON(s.MarkerPath(), &marker); err != nil {
		return nil, err
	}
	if validateMigrationID(marker.MigrationID) != nil || marker.DataRootHash != dataRootFingerprint(s.dataRoot) {
		return nil, errors.New("migration marker does not belong to this data root")
	}
	return &marker, nil
}

func (s *Service) createMarker(marker *Marker) error {
	if err := os.MkdirAll(s.controlRoot, 0o700); err != nil {
		return fmt.Errorf("create migration control directory: %w", err)
	}
	data, err := marshalIndented(marker)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(s.MarkerPath(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write migration marker: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync migration marker: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close migration marker: %w", err)
	}
	closed = true
	return syncDirectory(s.controlRoot)
}

func (s *Service) loadStatus() (*Status, error) {
	var status Status
	if err := readJSON(filepath.Join(s.controlRoot, statusFileName), &status); err != nil {
		return nil, err
	}
	return &status, nil
}

func (s *Service) saveStatus(status *Status) error {
	return writeJSONAtomic(filepath.Join(s.controlRoot, statusFileName), status)
}

func marshalIndented(value any) ([]byte, error) {
	data, err := jsonMarshalIndent(value)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
