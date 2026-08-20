package migration

import "time"

// State is the durable V1 to V2 migration state. The marker file, rather than
// this value alone, is the startup gate for business traffic.
type State string

const (
	StateNotRequired       State = "NotRequired"
	StateRequired          State = "Required"
	StatePrepared          State = "Prepared"
	StateMigrating         State = "Migrating"
	StateVerifying         State = "Verifying"
	StateRetiringLegacy    State = "RetiringLegacy"
	StatePublishing        State = "Publishing"
	StateFinalizing        State = "Finalizing"
	StateCompleted         State = "Completed"
	StateFailedRecoverable State = "FailedRecoverable"
	StateRollbackReady     State = "RollbackReady"
	StateRollingBack       State = "RollingBack"
	StateRollbackPrepared  State = "RollbackPrepared"
)

const (
	controlDirName = ".v2-migration"
	markerFileName = ".migration-required"
	statusFileName = "status.json"
	planFileName   = "plan.json"
)

type Finding struct {
	Code   string `json:"code"`
	Scope  string `json:"scope"`
	Path   string `json:"path,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type Layout struct {
	Version  int       `json:"version"`
	Fresh    bool      `json:"fresh"`
	Findings []Finding `json:"findings,omitempty"`
}

type Marker struct {
	LayoutVersion int       `json:"layout_version"`
	DataRootHash  string    `json:"data_root_hash"`
	CreatedAt     time.Time `json:"created_at"`
	MigrationID   string    `json:"migration_id"`
	FindingCodes  []string  `json:"finding_codes"`
}

type ManifestEntry struct {
	Scope      string    `json:"scope"`
	Path       string    `json:"path"`
	Kind       string    `json:"kind"`
	Size       int64     `json:"size"`
	ModTime    time.Time `json:"mtime"`
	SHA256     string    `json:"sha256"`
	RetirePath string    `json:"retire_path"`
}

type Conflict struct {
	Code   string `json:"code"`
	Scope  string `json:"scope"`
	Path   string `json:"path"`
	Detail string `json:"detail"`
}

type Plan struct {
	Version       int             `json:"version"`
	MigrationID   string          `json:"migration_id"`
	CreatedAt     time.Time       `json:"created_at"`
	ExpiresAt     time.Time       `json:"expires_at"`
	Manifest      []ManifestEntry `json:"manifest"`
	Conflicts     []Conflict      `json:"conflicts,omitempty"`
	RequiredBytes int64           `json:"required_bytes"`
	PlanHash      string          `json:"plan_hash"`
}

type JournalEntry struct {
	Sequence  int64     `json:"sequence"`
	At        time.Time `json:"at"`
	State     State     `json:"state"`
	Operation string    `json:"operation"`
	Scope     string    `json:"scope,omitempty"`
	From      string    `json:"from,omitempty"`
	To        string    `json:"to,omitempty"`
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
}

type Report struct {
	Version       int             `json:"version"`
	MigrationID   string          `json:"migration_id"`
	StartedAt     time.Time       `json:"started_at"`
	CompletedAt   *time.Time      `json:"completed_at,omitempty"`
	PlanHash      string          `json:"plan_hash"`
	Legacy        []ManifestEntry `json:"legacy"`
	ManualRestore []JournalEntry  `json:"manual_restore"`
}

type RollbackEntry struct {
	Scope      string    `json:"scope"`
	Path       string    `json:"path"`
	TargetPath string    `json:"target_path"`
	Kind       string    `json:"kind"`
	Size       int64     `json:"size"`
	ModTime    time.Time `json:"mtime"`
	SHA256     string    `json:"sha256"`
}

type RollbackPlan struct {
	Version     int             `json:"version"`
	MigrationID string          `json:"migration_id"`
	CreatedAt   time.Time       `json:"created_at"`
	ExpiresAt   time.Time       `json:"expires_at"`
	ActiveV2    []RollbackEntry `json:"active_v2"`
	LegacyV1    []RollbackEntry `json:"legacy_v1"`
	Conflicts   []Conflict      `json:"conflicts,omitempty"`
	PlanHash    string          `json:"plan_hash"`
}

type Status struct {
	State        State         `json:"state"`
	Required     bool          `json:"required"`
	Recoverable  bool          `json:"recoverable"`
	Marker       *Marker       `json:"marker,omitempty"`
	Plan         *Plan         `json:"plan,omitempty"`
	RollbackPlan *RollbackPlan `json:"rollback_plan,omitempty"`
	Findings     []Finding     `json:"findings,omitempty"`
	LastError    string        `json:"last_error,omitempty"`
	FailedFrom   State         `json:"failed_from,omitempty"`
	UpdatedAt    time.Time     `json:"updated_at"`
}
