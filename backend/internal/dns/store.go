package dns

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type Store struct {
	database *sql.DB
}

func NewStore(database *sql.DB) *Store {
	return &Store{database: database}
}

func (store *Store) EnsureSchema(ctx context.Context) error {
	if store == nil || store.database == nil || ctx == nil {
		return ErrStoreUnavailable
	}

	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return storeError(ctx, err)
	}
	defer transaction.Rollback()

	_, err = transaction.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS dns_zones (
			zone TEXT PRIMARY KEY,
			credential_id INTEGER NOT NULL,
			adopted_job_id TEXT NOT NULL,
			adopted_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS dns_change_jobs (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL DEFAULT 'adopt',
			zone TEXT NOT NULL,
			credential_id INTEGER NOT NULL,
			actor_id TEXT NOT NULL,
			session_binding_hash TEXT NOT NULL,
			auth_epoch TEXT NOT NULL,
			state TEXT NOT NULL,
			version INTEGER NOT NULL,
			snapshot_hash TEXT NOT NULL,
			plan_hash TEXT NOT NULL DEFAULT '',
			candidate_record_summary TEXT NOT NULL DEFAULT '',
			change_summary TEXT NOT NULL DEFAULT '',
			error_code TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS dns_zone_locks (
			zone TEXT PRIMARY KEY,
			job_id TEXT NOT NULL UNIQUE,
			created_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS dns_audit_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			job_id TEXT NOT NULL,
			action TEXT NOT NULL,
			state TEXT NOT NULL,
			created_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS dns_idempotency_keys (
			actor_id TEXT NOT NULL,
			action TEXT NOT NULL,
			idempotency_key TEXT NOT NULL,
			request_hash TEXT NOT NULL,
			job_id TEXT NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY (actor_id, action, idempotency_key)
		);
		CREATE INDEX IF NOT EXISTS dns_change_jobs_zone_idx ON dns_change_jobs (zone, state);
		CREATE INDEX IF NOT EXISTS dns_audit_logs_job_idx ON dns_audit_logs (job_id, id);
	`)
	if err != nil {
		return storeError(ctx, err)
	}
	if err := ensureChangeJobColumns(ctx, transaction); err != nil {
		return storeError(ctx, err)
	}
	if err := transaction.Commit(); err != nil {
		return storeError(ctx, err)
	}
	return nil
}

func ensureChangeJobColumns(ctx context.Context, transaction *sql.Tx) error {
	rows, err := transaction.QueryContext(ctx, `PRAGMA table_info(dns_change_jobs)`)
	if err != nil {
		return err
	}
	columns := make(map[string]bool)
	for rows.Next() {
		var position, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&position, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return err
		}
		columns[name] = true
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}

	additions := []struct {
		name       string
		definition string
	}{
		{"kind", `kind TEXT NOT NULL DEFAULT 'adopt'`},
		{"candidate_record_summary", `candidate_record_summary TEXT NOT NULL DEFAULT ''`},
		{"change_summary", `change_summary TEXT NOT NULL DEFAULT ''`},
	}
	for _, addition := range additions {
		if columns[addition.name] {
			continue
		}
		if _, err := transaction.ExecContext(ctx, `ALTER TABLE dns_change_jobs ADD COLUMN `+addition.definition); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) CreateAdopt(ctx context.Context, request AdoptRequest) (AdoptJob, error) {
	if store == nil || store.database == nil || ctx == nil {
		return AdoptJob{}, ErrStoreUnavailable
	}
	request, valid := normalizeAdoptRequest(request)
	if !valid {
		return AdoptJob{}, ErrInvalidAdopt
	}

	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return AdoptJob{}, storeError(ctx, err)
	}
	defer transaction.Rollback()

	var existingHash, existingJobID string
	err = transaction.QueryRowContext(ctx, `
		SELECT request_hash, job_id FROM dns_idempotency_keys
		WHERE actor_id = ? AND action = 'bind_zone' AND idempotency_key = ?`, request.ActorID, request.IdempotencyKey,
	).Scan(&existingHash, &existingJobID)
	switch {
	case err == nil:
		if existingHash != request.RequestHash {
			return AdoptJob{}, ErrIdempotencyConflict
		}
		job, getErr := store.getJob(ctx, transaction, existingJobID)
		if getErr != nil {
			return AdoptJob{}, getErr
		}
		if err := transaction.Commit(); err != nil {
			return AdoptJob{}, storeError(ctx, err)
		}
		return job, nil
	case !errors.Is(err, sql.ErrNoRows):
		return AdoptJob{}, storeError(ctx, err)
	}

	jobID, err := newJobID()
	if err != nil {
		return AdoptJob{}, ErrStoreUnavailable
	}
	now := time.Now().UTC()
	job := AdoptJob{
		ID:                 jobID,
		Kind:               JobKindAdopt,
		Zone:               request.Zone,
		CredentialID:       request.CredentialID,
		ActorID:            request.ActorID,
		AuthEpoch:          request.AuthEpoch,
		State:              JobQueued,
		Version:            1,
		SnapshotHash:       request.SnapshotHash,
		CreatedAt:          now,
		UpdatedAt:          now,
		sessionBindingHash: hashSessionBinding(request.SessionBinding),
	}
	timestamp := formatStoreTime(now)
	_, err = transaction.ExecContext(ctx, `
		INSERT INTO dns_change_jobs (
			id, kind, zone, credential_id, actor_id, session_binding_hash, auth_epoch, state, version,
			snapshot_hash, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.Kind, job.Zone, job.CredentialID, job.ActorID, job.sessionBindingHash, job.AuthEpoch, job.State,
		job.Version, job.SnapshotHash, timestamp, timestamp,
	)
	if err != nil {
		return AdoptJob{}, storeError(ctx, err)
	}
	_, err = transaction.ExecContext(ctx, `INSERT INTO dns_zone_locks (zone, job_id, created_at) VALUES (?, ?, ?)`, job.Zone, job.ID, timestamp)
	if err != nil {
		if isUniqueConstraint(err) {
			return AdoptJob{}, ErrZoneBusy
		}
		return AdoptJob{}, storeError(ctx, err)
	}
	_, err = transaction.ExecContext(ctx, `
		INSERT INTO dns_idempotency_keys (actor_id, action, idempotency_key, request_hash, job_id, created_at)
		VALUES (?, 'bind_zone', ?, ?, ?, ?)`,
		request.ActorID, request.IdempotencyKey, request.RequestHash, job.ID, timestamp,
	)
	if err != nil {
		return AdoptJob{}, storeError(ctx, err)
	}
	_, err = transaction.ExecContext(ctx, `INSERT INTO dns_audit_logs (job_id, action, state, created_at) VALUES (?, 'created', ?, ?)`, job.ID, job.State, timestamp)
	if err != nil {
		return AdoptJob{}, storeError(ctx, err)
	}
	if err := transaction.Commit(); err != nil {
		return AdoptJob{}, storeError(ctx, err)
	}
	return job, nil
}

func (store *Store) CreateChangePreview(ctx context.Context, request ChangePreviewRequest) (AdoptJob, error) {
	if store == nil || store.database == nil || ctx == nil {
		return AdoptJob{}, ErrStoreUnavailable
	}
	request, valid := normalizeChangePreviewRequest(request)
	if !valid {
		return AdoptJob{}, ErrInvalidChange
	}
	candidateRecord, err := json.Marshal(request.CandidateRecord)
	if err != nil {
		return AdoptJob{}, ErrInvalidChange
	}

	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return AdoptJob{}, storeError(ctx, err)
	}
	defer transaction.Rollback()

	var existingHash, existingJobID string
	err = transaction.QueryRowContext(ctx, `
		SELECT request_hash, job_id FROM dns_idempotency_keys
		WHERE actor_id = ? AND action = 'create_record_preview' AND idempotency_key = ?`, request.ActorID, request.IdempotencyKey,
	).Scan(&existingHash, &existingJobID)
	switch {
	case err == nil:
		if existingHash != request.RequestHash {
			return AdoptJob{}, ErrIdempotencyConflict
		}
		job, getErr := store.getJob(ctx, transaction, existingJobID)
		if getErr != nil {
			return AdoptJob{}, getErr
		}
		if err := transaction.Commit(); err != nil {
			return AdoptJob{}, storeError(ctx, err)
		}
		return job, nil
	case !errors.Is(err, sql.ErrNoRows):
		return AdoptJob{}, storeError(ctx, err)
	}

	jobID, err := newJobID()
	if err != nil {
		return AdoptJob{}, ErrStoreUnavailable
	}
	now := time.Now().UTC()
	job := AdoptJob{
		ID:                 jobID,
		Kind:               JobKindCreateRecordPreview,
		Zone:               request.Zone,
		CredentialID:       request.CredentialID,
		ActorID:            request.ActorID,
		AuthEpoch:          request.AuthEpoch,
		State:              JobQueued,
		Version:            1,
		SnapshotHash:       request.SnapshotHash,
		CandidateRecord:    request.CandidateRecord,
		CreatedAt:          now,
		UpdatedAt:          now,
		sessionBindingHash: hashSessionBinding(request.SessionBinding),
	}
	timestamp := formatStoreTime(now)
	_, err = transaction.ExecContext(ctx, `
		INSERT INTO dns_change_jobs (
			id, kind, zone, credential_id, actor_id, session_binding_hash, auth_epoch, state, version,
			snapshot_hash, candidate_record_summary, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.Kind, job.Zone, job.CredentialID, job.ActorID, job.sessionBindingHash, job.AuthEpoch, job.State,
		job.Version, job.SnapshotHash, string(candidateRecord), timestamp, timestamp,
	)
	if err != nil {
		return AdoptJob{}, storeError(ctx, err)
	}
	_, err = transaction.ExecContext(ctx, `INSERT INTO dns_zone_locks (zone, job_id, created_at) VALUES (?, ?, ?)`, job.Zone, job.ID, timestamp)
	if err != nil {
		if isUniqueConstraint(err) {
			return AdoptJob{}, ErrZoneBusy
		}
		return AdoptJob{}, storeError(ctx, err)
	}
	_, err = transaction.ExecContext(ctx, `
		INSERT INTO dns_idempotency_keys (actor_id, action, idempotency_key, request_hash, job_id, created_at)
		VALUES (?, 'create_record_preview', ?, ?, ?, ?)`,
		request.ActorID, request.IdempotencyKey, request.RequestHash, job.ID, timestamp,
	)
	if err != nil {
		return AdoptJob{}, storeError(ctx, err)
	}
	_, err = transaction.ExecContext(ctx, `INSERT INTO dns_audit_logs (job_id, action, state, created_at) VALUES (?, 'created', ?, ?)`, job.ID, job.State, timestamp)
	if err != nil {
		return AdoptJob{}, storeError(ctx, err)
	}
	if err := transaction.Commit(); err != nil {
		return AdoptJob{}, storeError(ctx, err)
	}
	return job, nil
}

func (store *Store) StartPreview(ctx context.Context, jobID string) (AdoptJob, error) {
	if store == nil || store.database == nil || ctx == nil || jobID == "" {
		return AdoptJob{}, ErrStoreUnavailable
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return AdoptJob{}, storeError(ctx, err)
	}
	defer transaction.Rollback()

	job, err := store.getJob(ctx, transaction, jobID)
	if err != nil {
		return AdoptJob{}, err
	}
	if job.State != JobQueued {
		return AdoptJob{}, ErrInvalidJobTransition
	}
	now := time.Now().UTC()
	_, err = transaction.ExecContext(ctx, `
		UPDATE dns_change_jobs
		SET state = ?, version = version + 1, updated_at = ?
		WHERE id = ? AND state = ?`, JobPreviewing, formatStoreTime(now), job.ID, JobQueued,
	)
	if err != nil {
		return AdoptJob{}, storeError(ctx, err)
	}
	_, err = transaction.ExecContext(ctx, `INSERT INTO dns_audit_logs (job_id, action, state, created_at) VALUES (?, 'previewing', ?, ?)`, job.ID, JobPreviewing, formatStoreTime(now))
	if err != nil {
		return AdoptJob{}, storeError(ctx, err)
	}
	job, err = store.getJob(ctx, transaction, job.ID)
	if err != nil {
		return AdoptJob{}, err
	}
	if err := transaction.Commit(); err != nil {
		return AdoptJob{}, storeError(ctx, err)
	}
	return job, nil
}

func (store *Store) FinishAdopt(ctx context.Context, jobID string, state JobState, planHash, errorCode string) (AdoptJob, error) {
	if state != JobAdopted && state != JobBlocked && state != JobFailed {
		return AdoptJob{}, ErrInvalidAdopt
	}
	return store.finishJob(ctx, jobID, JobKindAdopt, state, planHash, ChangeSummary{}, errorCode)
}

func (store *Store) FinishChangePreview(ctx context.Context, jobID string, state JobState, planHash string, summary ChangeSummary, errorCode string) (AdoptJob, error) {
	if state != JobPreviewed && state != JobBlocked && state != JobFailed {
		return AdoptJob{}, ErrInvalidChange
	}
	if state == JobPreviewed && (planHash == "" || !validStoredChangeSummary(summary)) {
		return AdoptJob{}, ErrInvalidChange
	}
	return store.finishJob(ctx, jobID, JobKindCreateRecordPreview, state, planHash, summary, errorCode)
}

func validStoredChangeSummary(summary ChangeSummary) bool {
	if summary.Corrections <= 0 || len(summary.Details) == 0 {
		return false
	}
	for _, detail := range summary.Details {
		digest, found := strings.CutPrefix(detail, "sha256:")
		if !found || len(digest) != sha256.Size*2 {
			return false
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return false
		}
	}
	return true
}

func (store *Store) finishJob(ctx context.Context, jobID string, kind JobKind, state JobState, planHash string, summary ChangeSummary, errorCode string) (AdoptJob, error) {
	if store == nil || store.database == nil || ctx == nil || jobID == "" || !state.terminal() {
		return AdoptJob{}, ErrStoreUnavailable
	}
	changeSummary := ""
	if state == JobPreviewed {
		encodedSummary, err := json.Marshal(summary)
		if err != nil {
			return AdoptJob{}, ErrStoreUnavailable
		}
		changeSummary = string(encodedSummary)
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return AdoptJob{}, storeError(ctx, err)
	}
	defer transaction.Rollback()

	job, err := store.getJob(ctx, transaction, jobID)
	if err != nil {
		return AdoptJob{}, err
	}
	if job.State != JobPreviewing || job.Kind != kind {
		return AdoptJob{}, ErrInvalidJobTransition
	}
	now := time.Now().UTC()
	timestamp := formatStoreTime(now)
	_, err = transaction.ExecContext(ctx, `
		UPDATE dns_change_jobs
		SET state = ?, version = version + 1, plan_hash = ?, change_summary = ?, error_code = ?, updated_at = ?
		WHERE id = ? AND state = ?`, state, planHash, changeSummary, errorCode, timestamp, job.ID, JobPreviewing,
	)
	if err != nil {
		return AdoptJob{}, storeError(ctx, err)
	}
	if state == JobAdopted {
		_, err = transaction.ExecContext(ctx, `
			INSERT INTO dns_zones (zone, credential_id, adopted_job_id, adopted_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(zone) DO UPDATE SET credential_id = excluded.credential_id, adopted_job_id = excluded.adopted_job_id, adopted_at = excluded.adopted_at`,
			job.Zone, job.CredentialID, job.ID, timestamp,
		)
		if err != nil {
			return AdoptJob{}, storeError(ctx, err)
		}
	}
	_, err = transaction.ExecContext(ctx, `DELETE FROM dns_zone_locks WHERE zone = ? AND job_id = ?`, job.Zone, job.ID)
	if err != nil {
		return AdoptJob{}, storeError(ctx, err)
	}
	_, err = transaction.ExecContext(ctx, `INSERT INTO dns_audit_logs (job_id, action, state, created_at) VALUES (?, 'finished', ?, ?)`, job.ID, state, timestamp)
	if err != nil {
		return AdoptJob{}, storeError(ctx, err)
	}
	job, err = store.getJob(ctx, transaction, job.ID)
	if err != nil {
		return AdoptJob{}, err
	}
	if err := transaction.Commit(); err != nil {
		return AdoptJob{}, storeError(ctx, err)
	}
	return job, nil
}

func (store *Store) IsAdopted(ctx context.Context, zone string, credentialID int64) (bool, error) {
	if store == nil || store.database == nil || ctx == nil {
		return false, ErrStoreUnavailable
	}
	zone = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(zone)), ".")
	if zone == "" || credentialID <= 0 {
		return false, ErrInvalidAdopt
	}
	var count int
	if err := store.database.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM dns_zones WHERE zone = ? AND credential_id = ?`, zone, credentialID,
	).Scan(&count); err != nil {
		return false, storeError(ctx, err)
	}
	return count == 1, nil
}

func (store *Store) GetJobForSession(ctx context.Context, jobID, actorID, sessionBinding, authEpoch string) (AdoptJob, error) {
	if store == nil || store.database == nil || ctx == nil || jobID == "" || actorID == "" || sessionBinding == "" || authEpoch == "" {
		return AdoptJob{}, ErrJobAccessDenied
	}
	job, err := store.getJob(ctx, store.database, jobID)
	if err != nil {
		return AdoptJob{}, err
	}
	if subtle.ConstantTimeCompare([]byte(job.ActorID), []byte(actorID)) != 1 ||
		subtle.ConstantTimeCompare([]byte(job.AuthEpoch), []byte(authEpoch)) != 1 ||
		subtle.ConstantTimeCompare([]byte(job.sessionBindingHash), []byte(hashSessionBinding(sessionBinding))) != 1 {
		return AdoptJob{}, ErrJobAccessDenied
	}
	return job, nil
}

func (store *Store) FailInterrupted(ctx context.Context) error {
	if store == nil || store.database == nil || ctx == nil {
		return ErrStoreUnavailable
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return storeError(ctx, err)
	}
	defer transaction.Rollback()

	rows, err := transaction.QueryContext(ctx, `SELECT id, zone FROM dns_change_jobs WHERE state IN (?, ?)`, JobQueued, JobPreviewing)
	if err != nil {
		return storeError(ctx, err)
	}
	type interruptedJob struct{ id, zone string }
	jobs := make([]interruptedJob, 0)
	for rows.Next() {
		var job interruptedJob
		if err := rows.Scan(&job.id, &job.zone); err != nil {
			_ = rows.Close()
			return storeError(ctx, err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Close(); err != nil {
		return storeError(ctx, err)
	}
	if err := rows.Err(); err != nil {
		return storeError(ctx, err)
	}

	timestamp := formatStoreTime(time.Now().UTC())
	for _, job := range jobs {
		_, err = transaction.ExecContext(ctx, `
			UPDATE dns_change_jobs
			SET state = ?, version = version + 1, error_code = ?, updated_at = ?
			WHERE id = ? AND state IN (?, ?)`, JobFailed, "DNS_INTERRUPTED", timestamp, job.id, JobQueued, JobPreviewing,
		)
		if err != nil {
			return storeError(ctx, err)
		}
		_, err = transaction.ExecContext(ctx, `DELETE FROM dns_zone_locks WHERE zone = ? AND job_id = ?`, job.zone, job.id)
		if err != nil {
			return storeError(ctx, err)
		}
		_, err = transaction.ExecContext(ctx, `INSERT INTO dns_audit_logs (job_id, action, state, created_at) VALUES (?, 'interrupted', ?, ?)`, job.id, JobFailed, timestamp)
		if err != nil {
			return storeError(ctx, err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return storeError(ctx, err)
	}
	return nil
}

func normalizeAdoptRequest(request AdoptRequest) (AdoptRequest, bool) {
	request.Zone = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(request.Zone)), ".")
	for _, value := range []string{request.Zone, request.ActorID, request.SessionBinding, request.AuthEpoch, request.RequestHash, request.IdempotencyKey, request.SnapshotHash} {
		if value == "" || len(value) > 512 || strings.TrimSpace(value) != value {
			return AdoptRequest{}, false
		}
	}
	if request.CredentialID <= 0 || !strings.Contains(request.Zone, ".") {
		return AdoptRequest{}, false
	}
	return request, true
}

func normalizeChangePreviewRequest(request ChangePreviewRequest) (ChangePreviewRequest, bool) {
	request.Zone = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(request.Zone)), ".")
	request.CandidateRecord.Name = strings.TrimSpace(request.CandidateRecord.Name)
	request.CandidateRecord.Type = strings.ToUpper(strings.TrimSpace(request.CandidateRecord.Type))
	for _, value := range []string{
		request.Zone, request.ActorID, request.SessionBinding, request.AuthEpoch, request.RequestHash,
		request.IdempotencyKey, request.SnapshotHash, request.CandidateRecord.Name, request.CandidateRecord.Type,
	} {
		if value == "" || len(value) > 512 || strings.TrimSpace(value) != value {
			return ChangePreviewRequest{}, false
		}
	}
	if request.CredentialID <= 0 || request.CandidateRecord.TTL <= 0 || !strings.Contains(request.Zone, ".") {
		return ChangePreviewRequest{}, false
	}
	if !safeSummaryText(request.CandidateRecord.Name, 253) || !safeSummaryText(request.CandidateRecord.Type, 16) {
		return ChangePreviewRequest{}, false
	}
	return request, true
}

func (store *Store) getJob(ctx context.Context, querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, jobID string) (AdoptJob, error) {
	if store == nil || ctx == nil || jobID == "" {
		return AdoptJob{}, ErrJobNotFound
	}
	var job AdoptJob
	var candidateRecord, changeSummary, createdAt, updatedAt string
	err := querier.QueryRowContext(ctx, `
		SELECT id, kind, zone, credential_id, actor_id, session_binding_hash, auth_epoch, state, version,
		       snapshot_hash, plan_hash, candidate_record_summary, change_summary, error_code, created_at, updated_at
		FROM dns_change_jobs WHERE id = ?`, jobID,
	).Scan(
		&job.ID, &job.Kind, &job.Zone, &job.CredentialID, &job.ActorID, &job.sessionBindingHash, &job.AuthEpoch,
		&job.State, &job.Version, &job.SnapshotHash, &job.PlanHash, &candidateRecord, &changeSummary,
		&job.ErrorCode, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return AdoptJob{}, ErrJobNotFound
	}
	if err != nil {
		return AdoptJob{}, storeError(ctx, err)
	}
	if candidateRecord != "" {
		if err := json.Unmarshal([]byte(candidateRecord), &job.CandidateRecord); err != nil {
			return AdoptJob{}, ErrStoreUnavailable
		}
	}
	if changeSummary != "" {
		if err := json.Unmarshal([]byte(changeSummary), &job.ChangeSummary); err != nil {
			return AdoptJob{}, ErrStoreUnavailable
		}
	}
	job.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return AdoptJob{}, ErrStoreUnavailable
	}
	job.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return AdoptJob{}, ErrStoreUnavailable
	}
	return job, nil
}

func newJobID() (string, error) {
	bytes := make([]byte, 24)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func hashSessionBinding(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func formatStoreTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func isUniqueConstraint(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}

func storeError(ctx context.Context, err error) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		return nil
	}
	return ErrStoreUnavailable
}
