package dns

import (
	"errors"
	"time"
)

var (
	ErrStoreUnavailable     = errors.New("DNS_UNAVAILABLE")
	ErrInvalidAdopt         = errors.New("DNS_ADOPT_INVALID")
	ErrZoneBusy             = errors.New("DNS_ZONE_BUSY")
	ErrIdempotencyConflict  = errors.New("DNS_IDEMPOTENCY_CONFLICT")
	ErrJobNotFound          = errors.New("DNS_JOB_NOT_FOUND")
	ErrJobAccessDenied      = errors.New("DNS_JOB_ACCESS_DENIED")
	ErrInvalidJobTransition = errors.New("DNS_JOB_TRANSITION_INVALID")
	ErrInvalidChange        = errors.New("DNS_CHANGE_INVALID")
	ErrProtectedRecord      = errors.New("DNS_PROTECTED_RECORD")
	ErrRecordConflict       = errors.New("DNS_RECORD_CONFLICT")
)

type JobState string

type JobKind string

const (
	JobQueued     JobState = "queued"
	JobPreviewing JobState = "previewing"
	JobAdopted    JobState = "adopted"
	JobPreviewed  JobState = "previewed"
	JobBlocked    JobState = "blocked"
	JobFailed     JobState = "failed"

	JobKindAdopt               JobKind = "adopt"
	JobKindCreateRecordPreview JobKind = "create_record_preview"
)

func (state JobState) terminal() bool {
	return state == JobAdopted || state == JobPreviewed || state == JobBlocked || state == JobFailed
}

type AdoptRequest struct {
	Zone           string
	CredentialID   int64
	ActorID        string
	SessionBinding string
	AuthEpoch      string
	RequestHash    string
	IdempotencyKey string
	SnapshotHash   string
}

type CandidateRecordSummary struct {
	Name string `json:"name"`
	Type string `json:"type"`
	TTL  int64  `json:"ttl"`
}

type ChangeSummary struct {
	Corrections int      `json:"corrections"`
	Details     []string `json:"details"`
}

type ChangePreviewRequest struct {
	Zone           string
	CredentialID   int64
	ActorID        string
	SessionBinding string
	AuthEpoch      string
	RequestHash    string
	IdempotencyKey string
	SnapshotHash   string
	AuditRecord    CandidateRecordSummary
}

type AdoptJob struct {
	ID              string                 `json:"id"`
	Kind            JobKind                `json:"kind"`
	Zone            string                 `json:"zone"`
	CredentialID    int64                  `json:"credential_id"`
	ActorID         string                 `json:"actor_id"`
	AuthEpoch       string                 `json:"auth_epoch"`
	State           JobState               `json:"state"`
	Version         int64                  `json:"version"`
	SnapshotHash    string                 `json:"snapshot_hash"`
	PlanHash        string                 `json:"plan_hash,omitempty"`
	CandidateRecord CandidateRecordSummary `json:"candidate_record,omitempty"`
	ChangeSummary   ChangeSummary          `json:"change_summary,omitempty"`
	ErrorCode       string                 `json:"error_code,omitempty"`
	CreatedAt       time.Time              `json:"created_at"`
	UpdatedAt       time.Time              `json:"updated_at"`

	sessionBindingHash string
	created            bool
	auditRecord        CandidateRecordSummary
}
