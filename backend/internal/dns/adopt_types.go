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

const (
	JobQueued     JobState = "queued"
	JobPreviewing JobState = "previewing"
	JobAdopted    JobState = "adopted"
	JobBlocked    JobState = "blocked"
	JobFailed     JobState = "failed"
)

func (state JobState) terminal() bool {
	return state == JobAdopted || state == JobBlocked || state == JobFailed
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

type AdoptJob struct {
	ID           string    `json:"id"`
	Zone         string    `json:"zone"`
	CredentialID int64     `json:"credential_id"`
	ActorID      string    `json:"actor_id"`
	AuthEpoch    string    `json:"auth_epoch"`
	State        JobState  `json:"state"`
	Version      int64     `json:"version"`
	SnapshotHash string    `json:"snapshot_hash"`
	PlanHash     string    `json:"plan_hash,omitempty"`
	ErrorCode    string    `json:"error_code,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`

	sessionBindingHash string
}
