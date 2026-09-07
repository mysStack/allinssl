package dns

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"ALLinSSL/backend/internal/dnscontrol"
	"ALLinSSL/backend/internal/dnsmodel"
)

const adoptWorkerTimeout = 90 * time.Second

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
	reader      zoneSnapshotReader
	credentials credentialStore
	store       *Store
	previewer   Previewer
	startWorker func(func())
}

func NewAdoptService(reader zoneSnapshotReader, credentials credentialStore, store *Store, previewer Previewer) *AdoptService {
	return &AdoptService{
		reader:      reader,
		credentials: credentials,
		store:       store,
		previewer:   previewer,
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
	service.startWorker(func() { service.run(job) })
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
	job, err := service.store.CreateChangePreview(ctx, ChangePreviewRequest{
		Zone:           zone,
		CredentialID:   input.CredentialID,
		ActorID:        input.Identity.ActorID,
		SessionBinding: input.Identity.SessionID,
		AuthEpoch:      input.Identity.AuthEpoch,
		RequestHash:    requestHash,
		IdempotencyKey: input.IdempotencyKey,
		SnapshotHash:   input.BaseSnapshotHash,
		CandidateRecord: CandidateRecordSummary{
			Name: strings.TrimSpace(input.Record.Name), Type: strings.ToUpper(strings.TrimSpace(input.Record.Type)), TTL: input.Record.TTL,
		},
	})
	if err != nil {
		return AdoptJob{}, err
	}
	service.startWorker(func() { service.runCreateRecordPreview(job, input.Record) })
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

func (service *AdoptService) run(job AdoptJob) {
	ctx, cancel := context.WithTimeout(context.Background(), adoptWorkerTimeout)
	defer cancel()
	if _, err := service.store.StartPreview(ctx, job.ID); err != nil {
		return
	}

	snapshot, err := service.reader.ReadZone(ctx, job.CredentialID, job.Zone)
	if err != nil {
		service.finish(ctx, job.ID, JobFailed, "", "DNS_SNAPSHOT_READ_FAILED")
		return
	}
	if snapshot.Zone != job.Zone || snapshot.SnapshotHash != job.SnapshotHash {
		service.finish(ctx, job.ID, JobBlocked, "", "DNS_SNAPSHOT_DRIFT")
		return
	}
	if !snapshot.Compatible {
		service.finish(ctx, job.ID, JobBlocked, "", "DNS_INCOMPATIBLE_SNAPSHOT")
		return
	}

	credential, err := service.credentials.Resolve(ctx, job.CredentialID)
	if err != nil {
		service.finish(ctx, job.ID, JobFailed, "", "DNS_CREDENTIAL_UNAVAILABLE")
		return
	}
	artifacts, err := dnscontrol.GenerateArtifacts(snapshot, dnscontrol.AliDNSCredentials{
		AccessKeyID: credential.accessKeyID, AccessKeySecret: credential.accessKeySecret,
	})
	if err != nil {
		service.finish(ctx, job.ID, JobBlocked, "", "DNS_ARTIFACT_INVALID")
		return
	}
	engineInfo, err := service.previewer.Health(ctx)
	if err != nil || engineInfo.Version != dnscontrol.ExpectedVersion {
		service.finish(ctx, job.ID, JobFailed, "", "DNS_ENGINE_UNAVAILABLE")
		return
	}
	plan, err := service.previewer.Preview(ctx, dnscontrol.PreviewInput{Zone: job.Zone, Artifacts: artifacts})
	if err != nil {
		service.finish(ctx, job.ID, JobFailed, "", "DNS_PREVIEW_FAILED")
		return
	}
	planHash := previewPlanHash(plan)
	if plan.Zone != job.Zone || plan.Provider != "ALIDNS" || plan.Corrections != 0 {
		service.finish(ctx, job.ID, JobBlocked, planHash, "DNS_NONZERO_CORRECTIONS")
		return
	}
	service.finish(ctx, job.ID, JobAdopted, planHash, "")
}

func (service *AdoptService) runCreateRecordPreview(job AdoptJob, input CreateRecordInput) {
	ctx, cancel := context.WithTimeout(context.Background(), adoptWorkerTimeout)
	defer cancel()
	if _, err := service.store.StartPreview(ctx, job.ID); err != nil {
		return
	}

	adopted, err := service.store.IsAdopted(ctx, job.Zone, job.CredentialID)
	if err != nil {
		service.finishChange(ctx, job.ID, JobFailed, "", ChangeSummary{}, "DNS_ADOPTION_CHECK_FAILED")
		return
	}
	if !adopted {
		service.finishChange(ctx, job.ID, JobBlocked, "", ChangeSummary{}, "DNS_NOT_ADOPTED")
		return
	}

	snapshot, err := service.reader.ReadZone(ctx, job.CredentialID, job.Zone)
	if err != nil {
		service.finishChange(ctx, job.ID, JobFailed, "", ChangeSummary{}, "DNS_SNAPSHOT_READ_FAILED")
		return
	}
	if snapshot.Zone != job.Zone || snapshot.SnapshotHash != job.SnapshotHash {
		service.finishChange(ctx, job.ID, JobBlocked, "", ChangeSummary{}, "DNS_REMOTE_DRIFT")
		return
	}
	if !snapshot.Compatible {
		service.finishChange(ctx, job.ID, JobBlocked, "", ChangeSummary{}, "DNS_INCOMPATIBLE_SNAPSHOT")
		return
	}

	candidate, _, err := BuildCreateRecordCandidate(snapshot, input)
	if err != nil {
		service.finishChange(ctx, job.ID, JobBlocked, "", ChangeSummary{}, changePreviewErrorCode(err))
		return
	}
	credential, err := service.credentials.Resolve(ctx, job.CredentialID)
	if err != nil {
		service.finishChange(ctx, job.ID, JobFailed, "", ChangeSummary{}, "DNS_CREDENTIAL_UNAVAILABLE")
		return
	}
	artifacts, err := dnscontrol.GenerateArtifacts(candidate, dnscontrol.AliDNSCredentials{
		AccessKeyID: credential.accessKeyID, AccessKeySecret: credential.accessKeySecret,
	})
	if err != nil {
		service.finishChange(ctx, job.ID, JobBlocked, "", ChangeSummary{}, "DNS_ARTIFACT_INVALID")
		return
	}
	engineInfo, err := service.previewer.Health(ctx)
	if err != nil || engineInfo.Version != dnscontrol.ExpectedVersion {
		service.finishChange(ctx, job.ID, JobFailed, "", ChangeSummary{}, "DNS_ENGINE_UNAVAILABLE")
		return
	}
	plan, err := service.previewer.Preview(ctx, dnscontrol.PreviewInput{Zone: job.Zone, Artifacts: artifacts})
	if err != nil {
		service.finishChange(ctx, job.ID, JobFailed, "", ChangeSummary{}, "DNS_PREVIEW_FAILED")
		return
	}
	planHash := previewPlanHash(plan)
	summary, valid := summarizeCreateRecordPlan(plan, job.Zone)
	if !valid {
		service.finishChange(ctx, job.ID, JobBlocked, planHash, ChangeSummary{}, "DNS_PREVIEW_PLAN_INVALID")
		return
	}
	service.finishChange(ctx, job.ID, JobPreviewed, planHash, summary, "")
}

func (service *AdoptService) finish(ctx context.Context, jobID string, state JobState, planHash, errorCode string) {
	_, _ = service.store.FinishAdopt(ctx, jobID, state, planHash, errorCode)
}

func (service *AdoptService) finishChange(ctx context.Context, jobID string, state JobState, planHash string, summary ChangeSummary, errorCode string) {
	_, _ = service.store.FinishChangePreview(ctx, jobID, state, planHash, summary, errorCode)
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
