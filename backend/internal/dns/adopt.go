package dns

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"ALLinSSL/backend/internal/dnscontrol"
	"ALLinSSL/backend/internal/dnsmodel"
)

const (
	adoptWorkerTimeout       = 90 * time.Second
	workerPersistenceTimeout = 5 * time.Second
)

type Previewer interface {
	Health(context.Context) (dnscontrol.EngineInfo, error)
	Preview(context.Context, dnscontrol.PreviewInput) (dnscontrol.PreviewPlan, error)
}

type zoneSnapshotReader interface {
	ReadZone(context.Context, int64, string) (dnsmodel.Snapshot, error)
}

type AdoptStartInput struct {
	Identity       SessionIdentity
	CredentialID   int64
	Zone           string
	SnapshotHash   string
	IdempotencyKey string
	AdoptAll       bool
}

type CreateRecordPreviewInput struct {
	Identity         SessionIdentity
	CredentialID     int64
	Zone             string
	BaseSnapshotHash string
	Record           CreateRecordInput
	IdempotencyKey   string
}

type AdoptService struct {
	reader             zoneSnapshotReader
	credentials        credentialStore
	store              *Store
	previewer          Previewer
	workerTimeout      time.Duration
	persistenceTimeout time.Duration
	reportWorkerError  func(error)
	startWorker        func(func())
}

func NewAdoptService(reader zoneSnapshotReader, credentials credentialStore, store *Store, previewer Previewer) *AdoptService {
	return &AdoptService{
		reader:             reader,
		credentials:        credentials,
		store:              store,
		previewer:          previewer,
		workerTimeout:      adoptWorkerTimeout,
		persistenceTimeout: workerPersistenceTimeout,
		reportWorkerError: func(err error) {
			log.Printf("DNS Preview worker 持久化失败: %v", err)
		},
		startWorker: func(work func()) { go work() },
	}
}

func (service *AdoptService) Start(ctx context.Context, input AdoptStartInput) (AdoptJob, error) {
	if service == nil || service.reader == nil || service.credentials == nil || service.store == nil || service.previewer == nil || ctx == nil || !input.AdoptAll {
		return AdoptJob{}, ErrInvalidAdopt
	}
	zone, err := dnsmodel.NormalizeZone(input.Zone)
	if err != nil || zone != input.Zone || input.CredentialID <= 0 || input.SnapshotHash == "" || input.Identity.ActorID != defaultDNSActorID || input.Identity.SessionID == "" || input.Identity.AuthEpoch == "" || input.IdempotencyKey == "" {
		return AdoptJob{}, ErrInvalidAdopt
	}
	requestHash, err := adoptRequestHash(input)
	if err != nil {
		return AdoptJob{}, ErrInvalidAdopt
	}
	job, err := service.store.CreateAdopt(ctx, AdoptRequest{
		Zone:           zone,
		CredentialID:   input.CredentialID,
		ActorID:        input.Identity.ActorID,
		SessionBinding: input.Identity.SessionID,
		AuthEpoch:      input.Identity.AuthEpoch,
		RequestHash:    requestHash,
		IdempotencyKey: input.IdempotencyKey,
		SnapshotHash:   input.SnapshotHash,
	})
	if err != nil {
		return AdoptJob{}, err
	}
	if job.created {
		service.startWorker(func() {
			if runErr := service.run(job); runErr != nil {
				service.reportWorkerError(runErr)
			}
		})
	}
	return job, nil
}

func (service *AdoptService) StartCreateRecordPreview(ctx context.Context, input CreateRecordPreviewInput) (AdoptJob, error) {
	if service == nil || service.reader == nil || service.credentials == nil || service.store == nil || service.previewer == nil || ctx == nil {
		return AdoptJob{}, ErrInvalidChange
	}
	zone, err := dnsmodel.NormalizeZone(input.Zone)
	if err != nil || zone != input.Zone || input.CredentialID <= 0 || input.BaseSnapshotHash == "" ||
		input.Identity.ActorID != defaultDNSActorID || input.Identity.SessionID == "" || input.Identity.AuthEpoch == "" ||
		input.IdempotencyKey == "" || !safeSummaryText(input.Record.Name, 253) || strings.TrimSpace(input.Record.Type) == "" {
		return AdoptJob{}, ErrInvalidChange
	}
	requestHash, err := createRecordPreviewRequestHash(input)
	if err != nil {
		return AdoptJob{}, ErrInvalidChange
	}
	auditRecord, err := safeCreateRecordAuditSummary(zone, input.Record)
	if err != nil {
		return AdoptJob{}, err
	}
	job, err := service.store.CreateChangePreview(ctx, ChangePreviewRequest{
		Zone:           zone,
		CredentialID:   input.CredentialID,
		ActorID:        input.Identity.ActorID,
		SessionBinding: input.Identity.SessionID,
		AuthEpoch:      input.Identity.AuthEpoch,
		RequestHash:    requestHash,
		IdempotencyKey: input.IdempotencyKey,
		SnapshotHash:   input.BaseSnapshotHash,
		AuditRecord:    auditRecord,
	})
	if err != nil {
		return AdoptJob{}, err
	}
	if job.created {
		service.startWorker(func() {
			if runErr := service.runCreateRecordPreview(job, input.Record); runErr != nil {
				service.reportWorkerError(runErr)
			}
		})
	}
	if current, getErr := service.store.GetJobForSession(
		ctx, job.ID, input.Identity.ActorID, input.Identity.SessionID, input.Identity.AuthEpoch,
	); getErr == nil {
		return current, nil
	}
	return job, nil
}

func (service *AdoptService) GetJob(ctx context.Context, jobID string, identity SessionIdentity) (AdoptJob, error) {
	if service == nil || service.store == nil {
		return AdoptJob{}, ErrStoreUnavailable
	}
	return service.store.GetJobForSession(ctx, jobID, identity.ActorID, identity.SessionID, identity.AuthEpoch)
}

func (service *AdoptService) Health(ctx context.Context) (dnscontrol.EngineInfo, error) {
	if service == nil || service.previewer == nil || ctx == nil {
		return dnscontrol.EngineInfo{}, ErrStoreUnavailable
	}
	return service.previewer.Health(ctx)
}

func (service *AdoptService) RecoverInterrupted(ctx context.Context) error {
	if service == nil || service.store == nil {
		return ErrStoreUnavailable
	}
	return service.store.FailInterrupted(ctx)
}

func (service *AdoptService) run(job AdoptJob) error {
	ctx, cancel := context.WithTimeout(context.Background(), service.workerTimeout)
	defer cancel()
	if _, err := service.store.StartPreview(ctx, job.ID); err != nil {
		return service.failActiveJob(job.ID, workerErrorCode(ctx, "DNS_PREVIEW_START_FAILED"), err)
	}

	snapshot, err := service.reader.ReadZone(ctx, job.CredentialID, job.Zone)
	if err != nil {
		return service.finish(job.ID, JobFailed, "", workerErrorCode(ctx, "DNS_SNAPSHOT_READ_FAILED"))
	}
	if snapshot.Zone != job.Zone || snapshot.SnapshotHash != job.SnapshotHash {
		return service.finish(job.ID, JobBlocked, "", "DNS_SNAPSHOT_DRIFT")
	}
	if !snapshot.Compatible {
		return service.finish(job.ID, JobBlocked, "", "DNS_INCOMPATIBLE_SNAPSHOT")
	}

	credential, err := service.credentials.Resolve(ctx, job.CredentialID)
	if err != nil {
		return service.finish(job.ID, JobFailed, "", workerErrorCode(ctx, "DNS_CREDENTIAL_UNAVAILABLE"))
	}
	artifacts, err := dnscontrol.GenerateArtifacts(snapshot, dnscontrol.AliDNSCredentials{
		AccessKeyID: credential.accessKeyID, AccessKeySecret: credential.accessKeySecret,
	})
	if err != nil {
		return service.finish(job.ID, JobBlocked, "", "DNS_ARTIFACT_INVALID")
	}
	engineInfo, err := service.previewer.Health(ctx)
	if err != nil || engineInfo.Version != dnscontrol.ExpectedVersion {
		return service.finish(job.ID, JobFailed, "", workerErrorCode(ctx, "DNS_ENGINE_UNAVAILABLE"))
	}
	plan, err := service.previewer.Preview(ctx, dnscontrol.PreviewInput{Zone: job.Zone, Artifacts: artifacts})
	if err != nil {
		return service.finish(job.ID, JobFailed, "", workerErrorCode(ctx, "DNS_PREVIEW_FAILED"))
	}
	if ctx.Err() != nil {
		return service.finish(job.ID, JobFailed, "", "DNS_WORKER_TIMEOUT")
	}
	planHash := previewPlanHash(plan)
	if plan.Zone != job.Zone || plan.Provider != "ALIDNS" || plan.Corrections != 0 {
		return service.finish(job.ID, JobBlocked, planHash, "DNS_NONZERO_CORRECTIONS")
	}
	return service.finish(job.ID, JobAdopted, planHash, "")
}

func (service *AdoptService) runCreateRecordPreview(job AdoptJob, input CreateRecordInput) error {
	ctx, cancel := context.WithTimeout(context.Background(), service.workerTimeout)
	defer cancel()
	if _, err := service.store.StartPreview(ctx, job.ID); err != nil {
		return service.failActiveJob(job.ID, workerErrorCode(ctx, "DNS_PREVIEW_START_FAILED"), err)
	}

	adopted, err := service.store.IsAdopted(ctx, job.Zone, job.CredentialID)
	if err != nil {
		return service.finishChange(job.ID, JobFailed, "", ChangeSummary{}, workerErrorCode(ctx, "DNS_ADOPTION_CHECK_FAILED"))
	}
	if !adopted {
		return service.finishChange(job.ID, JobBlocked, "", ChangeSummary{}, "DNS_NOT_ADOPTED")
	}

	snapshot, err := service.reader.ReadZone(ctx, job.CredentialID, job.Zone)
	if err != nil {
		return service.finishChange(job.ID, JobFailed, "", ChangeSummary{}, workerErrorCode(ctx, "DNS_SNAPSHOT_READ_FAILED"))
	}
	if snapshot.Zone != job.Zone || snapshot.SnapshotHash != job.SnapshotHash {
		return service.finishChange(job.ID, JobBlocked, "", ChangeSummary{}, "DNS_REMOTE_DRIFT")
	}
	if !snapshot.Compatible {
		return service.finishChange(job.ID, JobBlocked, "", ChangeSummary{}, "DNS_INCOMPATIBLE_SNAPSHOT")
	}

	candidate, record, err := BuildCreateRecordCandidate(snapshot, input)
	if err != nil {
		return service.finishChange(job.ID, JobBlocked, "", ChangeSummary{}, changePreviewErrorCode(err))
	}
	updatedJob, err := service.store.SetChangePreviewCandidate(ctx, job.ID, CandidateRecordSummary{
		Name: record.Name, Type: record.Type, TTL: record.TTL,
	})
	if err != nil {
		return service.failActiveJob(job.ID, workerErrorCode(ctx, "DNS_CANDIDATE_PERSIST_FAILED"), err)
	}
	job = updatedJob
	credential, err := service.credentials.Resolve(ctx, job.CredentialID)
	if err != nil {
		return service.finishChange(job.ID, JobFailed, "", ChangeSummary{}, workerErrorCode(ctx, "DNS_CREDENTIAL_UNAVAILABLE"))
	}
	artifacts, err := dnscontrol.GenerateArtifacts(candidate, dnscontrol.AliDNSCredentials{
		AccessKeyID: credential.accessKeyID, AccessKeySecret: credential.accessKeySecret,
	})
	if err != nil {
		return service.finishChange(job.ID, JobBlocked, "", ChangeSummary{}, "DNS_ARTIFACT_INVALID")
	}
	engineInfo, err := service.previewer.Health(ctx)
	if err != nil || engineInfo.Version != dnscontrol.ExpectedVersion {
		return service.finishChange(job.ID, JobFailed, "", ChangeSummary{}, workerErrorCode(ctx, "DNS_ENGINE_UNAVAILABLE"))
	}
	plan, err := service.previewer.Preview(ctx, dnscontrol.PreviewInput{Zone: job.Zone, Artifacts: artifacts})
	if err != nil {
		return service.finishChange(job.ID, JobFailed, "", ChangeSummary{}, workerErrorCode(ctx, "DNS_PREVIEW_FAILED"))
	}
	if ctx.Err() != nil {
		return service.finishChange(job.ID, JobFailed, "", ChangeSummary{}, "DNS_WORKER_TIMEOUT")
	}
	planHash := previewPlanHash(plan)
	summary, valid := summarizeCreateRecordPlan(plan, job.Zone)
	if !valid {
		return service.finishChange(job.ID, JobBlocked, planHash, ChangeSummary{}, "DNS_PREVIEW_PLAN_INVALID")
	}
	return service.finishChange(job.ID, JobPreviewed, planHash, summary, "")
}

func (service *AdoptService) finish(jobID string, state JobState, planHash, errorCode string) error {
	ctx, cancel := context.WithTimeout(context.Background(), service.persistenceTimeout)
	defer cancel()
	_, err := service.store.FinishAdopt(ctx, jobID, state, planHash, errorCode)
	if err == nil {
		return nil
	}
	return service.failActiveJob(jobID, "DNS_TERMINAL_PERSIST_FAILED", err)
}

func (service *AdoptService) finishChange(jobID string, state JobState, planHash string, summary ChangeSummary, errorCode string) error {
	ctx, cancel := context.WithTimeout(context.Background(), service.persistenceTimeout)
	defer cancel()
	_, err := service.store.FinishChangePreview(ctx, jobID, state, planHash, summary, errorCode)
	if err == nil {
		return nil
	}
	return service.failActiveJob(jobID, "DNS_TERMINAL_PERSIST_FAILED", err)
}

func (service *AdoptService) failActiveJob(jobID, errorCode string, cause error) error {
	ctx, cancel := context.WithTimeout(context.Background(), service.persistenceTimeout)
	defer cancel()
	_, err := service.store.FailActiveJob(ctx, jobID, errorCode)
	if err == nil {
		return nil
	}
	return errors.Join(cause, err)
}

func workerErrorCode(ctx context.Context, fallback string) string {
	if ctx.Err() != nil {
		return "DNS_WORKER_TIMEOUT"
	}
	return fallback
}

func safeCreateRecordAuditSummary(zone string, input CreateRecordInput) (CandidateRecordSummary, error) {
	snapshot, err := dnsmodel.BuildSnapshot(zone, nil, dnsmodel.Limits{MinTTL: 600, MaxTTL: 86400, Known: true})
	if err != nil {
		return CandidateRecordSummary{}, ErrInvalidChange
	}
	_, record, err := BuildCreateRecordCandidate(snapshot, input)
	if err != nil {
		return CandidateRecordSummary{}, err
	}
	return CandidateRecordSummary{Name: record.Name, Type: record.Type, TTL: record.TTL}, nil
}

func adoptRequestHash(input AdoptStartInput) (string, error) {
	encoded, err := json.Marshal(struct {
		CredentialID int64  `json:"credential_id"`
		Zone         string `json:"zone"`
		SnapshotHash string `json:"snapshot_hash"`
		AdoptAll     bool   `json:"adopt_all"`
	}{input.CredentialID, input.Zone, input.SnapshotHash, input.AdoptAll})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func createRecordPreviewRequestHash(input CreateRecordPreviewInput) (string, error) {
	encoded, err := json.Marshal(struct {
		CredentialID     int64             `json:"credential_id"`
		Zone             string            `json:"zone"`
		BaseSnapshotHash string            `json:"base_snapshot_hash"`
		Record           CreateRecordInput `json:"record"`
	}{input.CredentialID, input.Zone, input.BaseSnapshotHash, input.Record})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func summarizeCreateRecordPlan(plan dnscontrol.PreviewPlan, zone string) (ChangeSummary, bool) {
	if plan.Zone != zone || plan.Provider != "ALIDNS" || plan.Corrections <= 0 || len(plan.Details) == 0 {
		return ChangeSummary{}, false
	}
	details := make([]string, 0, len(plan.Details))
	for _, detail := range plan.Details {
		if !safeSummaryText(detail, 4096) {
			return ChangeSummary{}, false
		}
		digest := sha256.Sum256([]byte(detail))
		details = append(details, "sha256:"+hex.EncodeToString(digest[:]))
	}
	return ChangeSummary{Corrections: plan.Corrections, Details: details}, true
}

func safeSummaryText(value string, maximumBytes int) bool {
	if value == "" || len(value) > maximumBytes || !utf8.ValidString(value) || strings.TrimSpace(value) == "" {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func changePreviewErrorCode(err error) string {
	switch err {
	case ErrProtectedRecord:
		return "DNS_PROTECTED_RECORD"
	case ErrRecordConflict:
		return "DNS_RECORD_CONFLICT"
	default:
		return "DNS_CHANGE_INVALID"
	}
}

func previewPlanHash(plan dnscontrol.PreviewPlan) string {
	encoded, _ := json.Marshal(struct {
		Zone        string   `json:"zone"`
		Provider    string   `json:"provider"`
		Corrections int      `json:"corrections"`
		Details     []string `json:"details"`
	}{plan.Zone, plan.Provider, plan.Corrections, plan.Details})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
