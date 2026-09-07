package dns

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"ALLinSSL/backend/internal/dnscontrol"
	"ALLinSSL/backend/internal/dnsmodel"
)

const testWorkerTimeout = 100 * time.Millisecond

type fakeSnapshotReader struct {
	snapshot dnsmodel.Snapshot
	err      error
}

func (reader fakeSnapshotReader) ReadZone(context.Context, int64, string) (dnsmodel.Snapshot, error) {
	return reader.snapshot, reader.err
}

type timeoutSnapshotReader struct {
	snapshot dnsmodel.Snapshot
}

func (reader timeoutSnapshotReader) ReadZone(ctx context.Context, _ int64, _ string) (dnsmodel.Snapshot, error) {
	<-ctx.Done()
	return reader.snapshot, nil
}

type fakePreviewer struct {
	plan              dnscontrol.PreviewPlan
	healthErr         error
	previewErr        error
	waitForCancel     bool
	returnAfterCancel bool
	healthCalls       int
	previewCalls      int
	inputs            []dnscontrol.PreviewInput
}

func (previewer *fakePreviewer) Health(context.Context) (dnscontrol.EngineInfo, error) {
	previewer.healthCalls++
	return dnscontrol.EngineInfo{Version: dnscontrol.ExpectedVersion}, previewer.healthErr
}

func (previewer *fakePreviewer) Preview(ctx context.Context, input dnscontrol.PreviewInput) (dnscontrol.PreviewPlan, error) {
	previewer.previewCalls++
	previewer.inputs = append(previewer.inputs, input)
	if len(input.Artifacts.Config) == 0 || len(input.Artifacts.Credentials) == 0 {
		return dnscontrol.PreviewPlan{}, ErrInvalidAdopt
	}
	if previewer.waitForCancel {
		<-ctx.Done()
		return dnscontrol.PreviewPlan{}, ctx.Err()
	}
	if previewer.returnAfterCancel {
		<-ctx.Done()
	}
	return previewer.plan, previewer.previewErr
}

func markZoneAdopted(t *testing.T, service *AdoptService, credentialID int64, snapshotHash string) {
	t.Helper()
	job, err := service.store.CreateAdopt(context.Background(), AdoptRequest{
		Zone: "example.com", CredentialID: credentialID, ActorID: "local-admin", SessionBinding: "adopt-session",
		AuthEpoch: "adopt-epoch", RequestHash: "adopt-request", IdempotencyKey: "adopt-key", SnapshotHash: snapshotHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.store.StartPreview(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.store.FinishAdopt(context.Background(), job.ID, JobAdopted, "adopt-plan", ""); err != nil {
		t.Fatal(err)
	}
}

func createRecordPreviewInput(snapshot dnsmodel.Snapshot, key string) CreateRecordPreviewInput {
	return CreateRecordPreviewInput{
		Identity: testIdentity(), CredentialID: 1, Zone: "example.com", BaseSnapshotHash: snapshot.SnapshotHash,
		Record: CreateRecordInput{Name: "api", Type: "A", TTL: 600, Value: "192.0.2.20"}, IdempotencyKey: key,
	}
}

func testSnapshot(t *testing.T) dnsmodel.Snapshot {
	t.Helper()
	snapshot, err := dnsmodel.BuildSnapshot("example.com", nil, dnsmodel.Limits{MinTTL: 600, MaxTTL: 86400, Known: true})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func testIdentity() SessionIdentity {
	return SessionIdentity{ActorID: "local-admin", SessionID: "session-a", AuthEpoch: "epoch-a", CSRFToken: "csrf-a"}
}

func newAdoptService(t *testing.T, reader zoneSnapshotReader, previewer Previewer) *AdoptService {
	t.Helper()
	service := NewAdoptService(reader, fakeCredentialStore{credential: Credential{accessKeyID: "id", accessKeySecret: "secret"}}, newTestStore(t), previewer)
	service.startWorker = func(work func()) { work() }
	return service
}

func TestAdoptMarksZoneAdoptedOnlyAfterZeroDifferencePreview(t *testing.T) {
	snapshot := testSnapshot(t)
	previewer := &fakePreviewer{plan: dnscontrol.PreviewPlan{Zone: "example.com", Provider: "ALIDNS", Corrections: 0}}
	service := newAdoptService(t, fakeSnapshotReader{snapshot: snapshot}, previewer)

	job, err := service.Start(context.Background(), AdoptStartInput{
		Identity: testIdentity(), CredentialID: 1, Zone: "example.com", SnapshotHash: snapshot.SnapshotHash,
		AdoptAll: true, IdempotencyKey: "key-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	job, err = service.GetJob(context.Background(), job.ID, testIdentity())
	if err != nil {
		t.Fatal(err)
	}
	if job.State != JobAdopted {
		t.Fatalf("job state = %q, want %q", job.State, JobAdopted)
	}
	if previewer.healthCalls != 1 || previewer.previewCalls != 1 {
		t.Fatalf("preview calls = health:%d preview:%d", previewer.healthCalls, previewer.previewCalls)
	}
}

func TestAdoptBlocksNonzeroPreviewWithoutPersistingZone(t *testing.T) {
	snapshot := testSnapshot(t)
	previewer := &fakePreviewer{plan: dnscontrol.PreviewPlan{Zone: "example.com", Provider: "ALIDNS", Corrections: 1}}
	service := newAdoptService(t, fakeSnapshotReader{snapshot: snapshot}, previewer)

	job, err := service.Start(context.Background(), AdoptStartInput{
		Identity: testIdentity(), CredentialID: 1, Zone: "example.com", SnapshotHash: snapshot.SnapshotHash,
		AdoptAll: true, IdempotencyKey: "key-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	job, err = service.GetJob(context.Background(), job.ID, testIdentity())
	if err != nil {
		t.Fatal(err)
	}
	if job.State != JobBlocked || job.ErrorCode != "DNS_NONZERO_CORRECTIONS" {
		t.Fatalf("job = %#v", job)
	}
	var zoneCount int
	if err := service.store.database.QueryRow(`SELECT COUNT(*) FROM dns_zones WHERE zone = ?`, "example.com").Scan(&zoneCount); err != nil {
		t.Fatal(err)
	}
	if zoneCount != 0 {
		t.Fatalf("adopted zone count = %d, want 0", zoneCount)
	}
}

func TestAdoptRecoveryFailsInterruptedJobAndReleasesLock(t *testing.T) {
	snapshot := testSnapshot(t)
	service := newAdoptService(t, fakeSnapshotReader{snapshot: snapshot}, &fakePreviewer{})
	job, err := service.store.CreateAdopt(context.Background(), AdoptRequest{
		Zone: "example.com", CredentialID: 1, ActorID: "local-admin", SessionBinding: "session-a",
		AuthEpoch: "epoch-a", RequestHash: "request-a", IdempotencyKey: "key-a", SnapshotHash: snapshot.SnapshotHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RecoverInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, err = service.GetJob(context.Background(), job.ID, testIdentity())
	if err != nil {
		t.Fatal(err)
	}
	if job.State != JobFailed || job.ErrorCode != "DNS_INTERRUPTED" {
		t.Fatalf("recovered job = %#v", job)
	}
	if _, err := service.store.CreateAdopt(context.Background(), AdoptRequest{
		Zone: "example.com", CredentialID: 1, ActorID: "local-admin", SessionBinding: "session-b",
		AuthEpoch: "epoch-b", RequestHash: "request-b", IdempotencyKey: "key-b", SnapshotHash: snapshot.SnapshotHash,
	}); err != nil {
		t.Fatalf("interrupted lock was not released: %v", err)
	}
}

func TestCreateRecordPreviewPersistsSafeSummaryAndUsesFullFreshSnapshot(t *testing.T) {
	existing := dnsmodel.Record{Name: "www", Type: "A", TTL: 600, Value: "192.0.2.10", Line: "default", Status: "ENABLE"}
	snapshot, err := dnsmodel.BuildSnapshot("example.com", []dnsmodel.Record{existing}, dnsmodel.Limits{MinTTL: 600, MaxTTL: 86400, Known: true})
	if err != nil {
		t.Fatal(err)
	}
	rawDetail := "CREATE Api.Example.com. a 192.0.2.20"
	previewer := &fakePreviewer{plan: dnscontrol.PreviewPlan{
		Zone: "example.com", Provider: "ALIDNS", Corrections: 1, Details: []string{rawDetail},
	}}
	service := newAdoptService(t, fakeSnapshotReader{snapshot: snapshot}, previewer)
	markZoneAdopted(t, service, 1, snapshot.SnapshotHash)

	created, err := service.StartCreateRecordPreview(context.Background(), createRecordPreviewInput(snapshot, "record-a"))
	if err != nil {
		t.Fatal(err)
	}
	if created.State != JobPreviewed {
		t.Fatalf("created state = %q, want %q", created.State, JobPreviewed)
	}
	job, err := service.GetJob(context.Background(), created.ID, testIdentity())
	if err != nil {
		t.Fatal(err)
	}
	if job.State != JobPreviewed || job.Kind != JobKindCreateRecordPreview || job.PlanHash == "" {
		t.Fatalf("job = %#v", job)
	}
	if job.CandidateRecord != (CandidateRecordSummary{Name: "api", Type: "A", TTL: 600}) {
		t.Fatalf("candidate summary = %#v", job.CandidateRecord)
	}
	if job.ChangeSummary.Corrections != 1 || len(job.ChangeSummary.Details) != 1 ||
		job.ChangeSummary.Details[0] != "sha256:04b8e77b13444108e55a00708bee84206f6de1b01b4328663ca1edb7d0f64683" ||
		strings.Contains(job.ChangeSummary.Details[0], rawDetail) {
		t.Fatalf("unsafe change summary = %#v", job.ChangeSummary)
	}
	if len(previewer.inputs) != 1 {
		t.Fatalf("preview inputs = %d, want 1", len(previewer.inputs))
	}
	config := string(previewer.inputs[0].Artifacts.Config)
	if !strings.Contains(config, `A("www", "192.0.2.10")`) || !strings.Contains(config, `A("api", "192.0.2.20")`) {
		t.Fatalf("preview did not use full candidate snapshot: %s", config)
	}
}

func TestCreateRecordPreviewAcceptsTXTValueContainingSpaces(t *testing.T) {
	snapshot := testSnapshot(t)
	value := "v=spf1 include:mail.example.com ~all"
	previewer := &fakePreviewer{plan: dnscontrol.PreviewPlan{
		Zone: "example.com", Provider: "ALIDNS", Corrections: 1, Details: []string{"CREATE txt.example.com. TXT " + value},
	}}
	service := newAdoptService(t, fakeSnapshotReader{snapshot: snapshot}, previewer)
	markZoneAdopted(t, service, 1, snapshot.SnapshotHash)
	input := createRecordPreviewInput(snapshot, "record-txt-with-spaces")
	input.Record = CreateRecordInput{Name: "txt", Type: "TXT", TTL: 600, Value: value}

	job, err := service.StartCreateRecordPreview(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != JobPreviewed {
		t.Fatalf("job = %#v", job)
	}
}

func TestCreateRecordPreviewPersistsOnlyValidatedCanonicalCandidateSummary(t *testing.T) {
	snapshot := testSnapshot(t)
	previewer := &fakePreviewer{plan: dnscontrol.PreviewPlan{
		Zone: "example.com", Provider: "ALIDNS", Corrections: 1, Details: []string{"CREATE api.example.com. A 192.0.2.20"},
	}}
	service := newAdoptService(t, fakeSnapshotReader{snapshot: snapshot}, previewer)
	markZoneAdopted(t, service, 1, snapshot.SnapshotHash)
	input := createRecordPreviewInput(snapshot, "record-canonical")
	input.Record.Name = "Api.Example.com."

	job, err := service.StartCreateRecordPreview(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != JobPreviewed {
		t.Fatalf("job = %#v", job)
	}
	if job.CandidateRecord != (CandidateRecordSummary{Name: "api", Type: "A", TTL: 600}) {
		t.Fatalf("candidate summary = %#v", job.CandidateRecord)
	}

	var persisted string
	if err := service.store.database.QueryRow(`
		SELECT candidate_record_summary FROM dns_change_jobs WHERE id = ?`, job.ID,
	).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(persisted, "Api.Example.com.") {
		t.Fatalf("candidate summary retained raw input: %q", persisted)
	}
}

func TestCreateRecordPreviewDoesNotPersistSummaryForInvalidFullCandidate(t *testing.T) {
	existing := dnsmodel.Record{Name: "api", Type: "A", TTL: 600, Value: "192.0.2.20", Line: "default", Status: "ENABLE"}
	snapshot, err := dnsmodel.BuildSnapshot("example.com", []dnsmodel.Record{existing}, dnsmodel.Limits{MinTTL: 600, MaxTTL: 86400, Known: true})
	if err != nil {
		t.Fatal(err)
	}
	service := newAdoptService(t, fakeSnapshotReader{snapshot: snapshot}, &fakePreviewer{})
	markZoneAdopted(t, service, 1, snapshot.SnapshotHash)

	job, err := service.StartCreateRecordPreview(context.Background(), createRecordPreviewInput(snapshot, "record-conflict"))
	if err != nil {
		t.Fatal(err)
	}
	if job.State != JobBlocked || job.ErrorCode != "DNS_RECORD_CONFLICT" {
		t.Fatalf("job = %#v", job)
	}

	var persisted string
	if err := service.store.database.QueryRow(`
		SELECT candidate_record_summary FROM dns_change_jobs WHERE id = ?`, job.ID,
	).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != "" || job.CandidateRecord != (CandidateRecordSummary{}) {
		t.Fatalf("invalid candidate summary persisted: column=%q job=%#v", persisted, job.CandidateRecord)
	}
	var auditRows int
	if err := service.store.database.QueryRow(`
		SELECT COUNT(*) FROM dns_audit_logs
		WHERE job_id = ? AND zone = 'example.com' AND record_type = 'A' AND record_name = 'api' AND error_code = 'DNS_RECORD_CONFLICT'`, job.ID,
	).Scan(&auditRows); err != nil {
		t.Fatal(err)
	}
	if auditRows != 1 {
		t.Fatalf("blocked candidate audit rows = %d, want 1", auditRows)
	}
}

func TestCreateRecordPreviewTimeoutPersistsFailureAndReleasesZoneLock(t *testing.T) {
	snapshot := testSnapshot(t)
	service := newAdoptService(t, fakeSnapshotReader{snapshot: snapshot}, &fakePreviewer{waitForCancel: true})
	service.workerTimeout = testWorkerTimeout
	markZoneAdopted(t, service, 1, snapshot.SnapshotHash)

	job, err := service.StartCreateRecordPreview(context.Background(), createRecordPreviewInput(snapshot, "record-timeout"))
	if err != nil {
		t.Fatal(err)
	}
	if job.State != JobFailed || job.ErrorCode != "DNS_WORKER_TIMEOUT" {
		t.Fatalf("timed out job = %#v", job)
	}
	if _, err := service.store.CreateAdopt(context.Background(), AdoptRequest{
		Zone: "example.com", CredentialID: 1, ActorID: "local-admin", SessionBinding: "session-b",
		AuthEpoch: "epoch-b", RequestHash: "after-timeout", IdempotencyKey: "after-timeout", SnapshotHash: snapshot.SnapshotHash,
	}); err != nil {
		t.Fatalf("timeout left Zone lock held: %v", err)
	}
}

func TestCreateRecordPreviewRejectsPlanReturnedAfterWorkerCancellation(t *testing.T) {
	snapshot := testSnapshot(t)
	previewer := &fakePreviewer{
		returnAfterCancel: true,
		plan:              dnscontrol.PreviewPlan{Zone: "example.com", Provider: "ALIDNS", Corrections: 1, Details: []string{"CREATE api.example.com. A 192.0.2.20"}},
	}
	service := newAdoptService(t, fakeSnapshotReader{snapshot: snapshot}, previewer)
	service.workerTimeout = testWorkerTimeout
	markZoneAdopted(t, service, 1, snapshot.SnapshotHash)

	job, err := service.StartCreateRecordPreview(context.Background(), createRecordPreviewInput(snapshot, "record-cancel"))
	if err != nil {
		t.Fatal(err)
	}
	if job.State != JobFailed || job.ErrorCode != "DNS_WORKER_TIMEOUT" {
		t.Fatalf("canceled job = %#v", job)
	}
	if _, err := service.store.CreateAdopt(context.Background(), AdoptRequest{
		Zone: "example.com", CredentialID: 1, ActorID: "local-admin", SessionBinding: "session-b",
		AuthEpoch: "epoch-b", RequestHash: "after-cancel", IdempotencyKey: "after-cancel", SnapshotHash: snapshot.SnapshotHash,
	}); err != nil {
		t.Fatalf("canceled worker left Zone lock held: %v", err)
	}
}

func TestCreateRecordPreviewTimeoutBeforeCandidatePersistenceReleasesZoneLock(t *testing.T) {
	snapshot := testSnapshot(t)
	service := newAdoptService(t, timeoutSnapshotReader{snapshot: snapshot}, &fakePreviewer{})
	service.workerTimeout = testWorkerTimeout
	markZoneAdopted(t, service, 1, snapshot.SnapshotHash)

	job, err := service.StartCreateRecordPreview(context.Background(), createRecordPreviewInput(snapshot, "record-read-timeout"))
	if err != nil {
		t.Fatal(err)
	}
	if job.State != JobFailed || job.ErrorCode != "DNS_WORKER_TIMEOUT" {
		t.Fatalf("timed out job = %#v", job)
	}
	if _, err := service.store.CreateAdopt(context.Background(), AdoptRequest{
		Zone: "example.com", CredentialID: 1, ActorID: "local-admin", SessionBinding: "session-b",
		AuthEpoch: "epoch-b", RequestHash: "after-read-timeout", IdempotencyKey: "after-read-timeout", SnapshotHash: snapshot.SnapshotHash,
	}); err != nil {
		t.Fatalf("pre-persistence timeout left Zone lock held: %v", err)
	}
}

func TestCreateRecordPreviewSurfacesUnpersistedWorkerFailure(t *testing.T) {
	snapshot := testSnapshot(t)
	service := newAdoptService(t, fakeSnapshotReader{snapshot: snapshot}, &fakePreviewer{})
	markZoneAdopted(t, service, 1, snapshot.SnapshotHash)
	var workerErr error
	service.reportWorkerError = func(err error) { workerErr = err }
	service.startWorker = func(work func()) {
		if err := service.store.database.Close(); err != nil {
			t.Fatal(err)
		}
		work()
	}

	job, err := service.StartCreateRecordPreview(context.Background(), createRecordPreviewInput(snapshot, "record-persist-failure"))
	if err != nil {
		t.Fatal(err)
	}
	if job.State != JobQueued {
		t.Fatalf("job state = %q, want queued because terminal persistence failed", job.State)
	}
	if !errors.Is(workerErr, ErrStoreUnavailable) {
		t.Fatalf("reported worker error = %v, want %v", workerErr, ErrStoreUnavailable)
	}
}

func TestCreateRecordPreviewTerminalPersistenceValidationFailureFailsSafely(t *testing.T) {
	snapshot := testSnapshot(t)
	service := newAdoptService(t, fakeSnapshotReader{snapshot: snapshot}, &fakePreviewer{})
	job, err := service.store.CreateChangePreview(context.Background(), ChangePreviewRequest{
		Zone: "example.com", CredentialID: 1, ActorID: "local-admin",
		SessionBinding: "session-a", AuthEpoch: "epoch-a", RequestHash: "change-request",
		IdempotencyKey: "change-key", SnapshotHash: snapshot.SnapshotHash,
		AuditRecord: CandidateRecordSummary{Name: "api", Type: "A", TTL: 600},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.store.StartPreview(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}

	if err := service.finishChange(job.ID, JobPreviewed,
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ChangeSummary{Corrections: 1, Details: []string{"unsafe raw detail"}}, "",
	); err != nil {
		t.Fatal(err)
	}
	job, err = service.store.GetJobForSession(context.Background(), job.ID, "local-admin", "session-a", "epoch-a")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != JobFailed || job.ErrorCode != "DNS_TERMINAL_PERSIST_FAILED" {
		t.Fatalf("fallback job = %#v", job)
	}
	if _, err := service.store.CreateAdopt(context.Background(), AdoptRequest{
		Zone: "example.com", CredentialID: 1, ActorID: "local-admin", SessionBinding: "session-b",
		AuthEpoch: "epoch-b", RequestHash: "after-fallback", IdempotencyKey: "after-fallback", SnapshotHash: snapshot.SnapshotHash,
	}); err != nil {
		t.Fatalf("terminal persistence fallback left Zone lock held: %v", err)
	}
}

func TestCreateRecordPreviewAuditPersistsSafeContext(t *testing.T) {
	snapshot := testSnapshot(t)
	previewer := &fakePreviewer{plan: dnscontrol.PreviewPlan{
		Zone: "example.com", Provider: "BIND", Corrections: 1, Details: []string{"unsafe provider detail"},
	}}
	service := newAdoptService(t, fakeSnapshotReader{snapshot: snapshot}, previewer)
	markZoneAdopted(t, service, 1, snapshot.SnapshotHash)
	input := createRecordPreviewInput(snapshot, "record-audit")
	input.Record.Name = "Audit.Example.com."
	input.Record.Type = "TXT"
	input.Record.Value = "sensitive-audit-value"

	job, err := service.StartCreateRecordPreview(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != JobBlocked || job.ErrorCode != "DNS_PREVIEW_PLAN_INVALID" || job.PlanHash == "" {
		t.Fatalf("job = %#v", job)
	}

	var zone, recordType, recordName, planHash, errorCode string
	if err := service.store.database.QueryRow(`
		SELECT zone, record_type, record_name, plan_hash, error_code
		FROM dns_audit_logs WHERE job_id = ? AND action = 'finished'`, job.ID,
	).Scan(&zone, &recordType, &recordName, &planHash, &errorCode); err != nil {
		t.Fatal(err)
	}
	if zone != "example.com" || recordType != "TXT" || recordName != "audit" || planHash != job.PlanHash || errorCode != job.ErrorCode {
		t.Fatalf("audit context = zone:%q type:%q name:%q plan:%q error:%q", zone, recordType, recordName, planHash, errorCode)
	}
	var contextualRows int
	if err := service.store.database.QueryRow(`
		SELECT COUNT(*) FROM dns_audit_logs
		WHERE job_id = ? AND zone = 'example.com' AND record_type = 'TXT' AND record_name = 'audit'`, job.ID,
	).Scan(&contextualRows); err != nil {
		t.Fatal(err)
	}
	if contextualRows != 3 {
		t.Fatalf("audit rows with safe Zone/record context = %d, want 3", contextualRows)
	}
	var leaked int
	if err := service.store.database.QueryRow(`
		SELECT COUNT(*) FROM dns_audit_logs
		WHERE job_id = ? AND (zone LIKE '%sensitive-audit-value%' OR record_type LIKE '%sensitive-audit-value%' OR record_name LIKE '%sensitive-audit-value%' OR plan_hash LIKE '%sensitive-audit-value%' OR error_code LIKE '%sensitive-audit-value%')`, job.ID,
	).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Fatalf("audit retained record value in %d rows", leaked)
	}
}

func TestCreateRecordPreviewRequiresAdoptionWithSameCredential(t *testing.T) {
	for _, test := range []struct {
		name              string
		adoptCredentialID int64
		requestCredential int64
	}{
		{name: "not adopted", requestCredential: 1},
		{name: "different credential", adoptCredentialID: 2, requestCredential: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := testSnapshot(t)
			previewer := &fakePreviewer{plan: dnscontrol.PreviewPlan{
				Zone: "example.com", Provider: "ALIDNS", Corrections: 1, Details: []string{"create"},
			}}
			service := newAdoptService(t, fakeSnapshotReader{snapshot: snapshot}, previewer)
			if test.adoptCredentialID > 0 {
				markZoneAdopted(t, service, test.adoptCredentialID, snapshot.SnapshotHash)
			}
			input := createRecordPreviewInput(snapshot, "record-a")
			input.CredentialID = test.requestCredential

			created, err := service.StartCreateRecordPreview(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			job, err := service.GetJob(context.Background(), created.ID, testIdentity())
			if err != nil {
				t.Fatal(err)
			}
			if job.State != JobBlocked || job.ErrorCode != "DNS_NOT_ADOPTED" || previewer.previewCalls != 0 {
				t.Fatalf("job = %#v, preview calls = %d", job, previewer.previewCalls)
			}
		})
	}
}

func TestCreateRecordPreviewBlocksFreshSnapshotDriftAndIncompatibility(t *testing.T) {
	for _, test := range []struct {
		name      string
		reader    func(*testing.T) dnsmodel.Snapshot
		errorCode string
	}{
		{
			name: "snapshot drift",
			reader: func(t *testing.T) dnsmodel.Snapshot {
				record := dnsmodel.Record{Name: "changed", Type: "A", TTL: 600, Value: "192.0.2.30", Line: "default", Status: "ENABLE"}
				snapshot, err := dnsmodel.BuildSnapshot("example.com", []dnsmodel.Record{record}, dnsmodel.Limits{MinTTL: 600, MaxTTL: 86400, Known: true})
				if err != nil {
					t.Fatal(err)
				}
				return snapshot
			},
			errorCode: "DNS_REMOTE_DRIFT",
		},
		{
			name: "incompatible snapshot",
			reader: func(t *testing.T) dnsmodel.Snapshot {
				record := dnsmodel.Record{Name: "disabled", Type: "A", TTL: 600, Value: "192.0.2.30", Line: "default", Status: "DISABLE"}
				snapshot, err := dnsmodel.BuildSnapshot("example.com", []dnsmodel.Record{record}, dnsmodel.Limits{MinTTL: 600, MaxTTL: 86400, Known: true})
				if err != nil {
					t.Fatal(err)
				}
				return snapshot
			},
			errorCode: "DNS_INCOMPATIBLE_SNAPSHOT",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := testSnapshot(t)
			fresh := test.reader(t)
			previewer := &fakePreviewer{}
			service := newAdoptService(t, fakeSnapshotReader{snapshot: fresh}, previewer)
			markZoneAdopted(t, service, 1, base.SnapshotHash)
			input := createRecordPreviewInput(base, "record-a")
			if test.errorCode == "DNS_INCOMPATIBLE_SNAPSHOT" {
				input.BaseSnapshotHash = fresh.SnapshotHash
			}

			created, err := service.StartCreateRecordPreview(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			job, err := service.GetJob(context.Background(), created.ID, testIdentity())
			if err != nil {
				t.Fatal(err)
			}
			if job.State != JobBlocked || job.ErrorCode != test.errorCode || previewer.previewCalls != 0 {
				t.Fatalf("job = %#v, preview calls = %d", job, previewer.previewCalls)
			}
		})
	}
}

func TestCreateRecordPreviewRejectsUnsafeProviderPlans(t *testing.T) {
	matchingDetail := "CREATE api.example.com. A 192.0.2.20"
	for _, test := range []struct {
		name string
		plan dnscontrol.PreviewPlan
	}{
		{name: "wrong zone", plan: dnscontrol.PreviewPlan{Zone: "other.example", Provider: "ALIDNS", Corrections: 1, Details: []string{matchingDetail}}},
		{name: "wrong provider", plan: dnscontrol.PreviewPlan{Zone: "example.com", Provider: "BIND", Corrections: 1, Details: []string{matchingDetail}}},
		{name: "zero corrections", plan: dnscontrol.PreviewPlan{Zone: "example.com", Provider: "ALIDNS", Corrections: 0, Details: []string{matchingDetail}}},
		{name: "negative corrections", plan: dnscontrol.PreviewPlan{Zone: "example.com", Provider: "ALIDNS", Corrections: -1, Details: []string{matchingDetail}}},
		{name: "missing details", plan: dnscontrol.PreviewPlan{Zone: "example.com", Provider: "ALIDNS", Corrections: 1}},
		{name: "blank detail", plan: dnscontrol.PreviewPlan{Zone: "example.com", Provider: "ALIDNS", Corrections: 1, Details: []string{" \t"}}},
		{name: "invalid utf8 detail", plan: dnscontrol.PreviewPlan{Zone: "example.com", Provider: "ALIDNS", Corrections: 1, Details: []string{string([]byte{0xff})}}},
		{name: "unknown output", plan: dnscontrol.PreviewPlan{Zone: "example.com", Provider: "ALIDNS", Corrections: 1, Details: []string{"create"}}},
		{name: "missing owner and type", plan: dnscontrol.PreviewPlan{Zone: "example.com", Provider: "ALIDNS", Corrections: 1, Details: []string{"CREATE"}}},
		{name: "missing type", plan: dnscontrol.PreviewPlan{Zone: "example.com", Provider: "ALIDNS", Corrections: 1, Details: []string{"CREATE api.example.com."}}},
		{name: "missing payload", plan: dnscontrol.PreviewPlan{Zone: "example.com", Provider: "ALIDNS", Corrections: 1, Details: []string{"CREATE api.example.com. A"}}},
		{name: "double separator", plan: dnscontrol.PreviewPlan{Zone: "example.com", Provider: "ALIDNS", Corrections: 1, Details: []string{"CREATE  api.example.com. A 192.0.2.20"}}},
		{name: "delete", plan: dnscontrol.PreviewPlan{Zone: "example.com", Provider: "ALIDNS", Corrections: 1, Details: []string{"DELETE api.example.com. A 192.0.2.20"}}},
		{name: "modify", plan: dnscontrol.PreviewPlan{Zone: "example.com", Provider: "ALIDNS", Corrections: 1, Details: []string{"MODIFY api.example.com. A 192.0.2.20"}}},
		{name: "ttl change", plan: dnscontrol.PreviewPlan{Zone: "example.com", Provider: "ALIDNS", Corrections: 1, Details: []string{"TTL api.example.com. A 600 -> 300"}}},
		{name: "unrelated owner", plan: dnscontrol.PreviewPlan{Zone: "example.com", Provider: "ALIDNS", Corrections: 1, Details: []string{"CREATE other.example.com. A 192.0.2.20"}}},
		{name: "wrong type", plan: dnscontrol.PreviewPlan{Zone: "example.com", Provider: "ALIDNS", Corrections: 1, Details: []string{"CREATE api.example.com. AAAA 2001:db8::1"}}},
		{name: "relative owner", plan: dnscontrol.PreviewPlan{Zone: "example.com", Provider: "ALIDNS", Corrections: 1, Details: []string{"CREATE api A 192.0.2.20"}}},
		{name: "wrong payload", plan: dnscontrol.PreviewPlan{Zone: "example.com", Provider: "ALIDNS", Corrections: 1, Details: []string{"CREATE api.example.com. A 192.0.2.30"}}},
		{name: "extra token", plan: dnscontrol.PreviewPlan{Zone: "example.com", Provider: "ALIDNS", Corrections: 1, Details: []string{"CREATE api.example.com. A 192.0.2.20 unexpected"}}},
		{name: "count mismatch", plan: dnscontrol.PreviewPlan{Zone: "example.com", Provider: "ALIDNS", Corrections: 2, Details: []string{matchingDetail}}},
		{name: "detail count mismatch", plan: dnscontrol.PreviewPlan{Zone: "example.com", Provider: "ALIDNS", Corrections: 1, Details: []string{matchingDetail, matchingDetail}}},
		{name: "extra detail", plan: dnscontrol.PreviewPlan{Zone: "example.com", Provider: "ALIDNS", Corrections: 2, Details: []string{matchingDetail, "CREATE other.example.com. A 192.0.2.30"}}},
		{name: "duplicate detail", plan: dnscontrol.PreviewPlan{Zone: "example.com", Provider: "ALIDNS", Corrections: 2, Details: []string{matchingDetail, matchingDetail}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := testSnapshot(t)
			previewer := &fakePreviewer{plan: test.plan}
			service := newAdoptService(t, fakeSnapshotReader{snapshot: snapshot}, previewer)
			markZoneAdopted(t, service, 1, snapshot.SnapshotHash)

			created, err := service.StartCreateRecordPreview(context.Background(), createRecordPreviewInput(snapshot, "record-a"))
			if err != nil {
				t.Fatal(err)
			}
			job, err := service.GetJob(context.Background(), created.ID, testIdentity())
			if err != nil {
				t.Fatal(err)
			}
			if job.State != JobBlocked || job.ErrorCode != "DNS_PREVIEW_PLAN_INVALID" {
				t.Fatalf("job = %#v", job)
			}
		})
	}
}

func TestCreateRecordPreviewIdempotencyReplaysSameRequestAndRejectsChanges(t *testing.T) {
	snapshot := testSnapshot(t)
	previewer := &fakePreviewer{plan: dnscontrol.PreviewPlan{
		Zone: "example.com", Provider: "ALIDNS", Corrections: 1, Details: []string{"CREATE api.example.com. A 192.0.2.20"},
	}}
	service := newAdoptService(t, fakeSnapshotReader{snapshot: snapshot}, previewer)
	markZoneAdopted(t, service, 1, snapshot.SnapshotHash)
	input := createRecordPreviewInput(snapshot, "record-a")

	first, err := service.StartCreateRecordPreview(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.StartCreateRecordPreview(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || previewer.previewCalls != 1 {
		t.Fatalf("second job = %q, first = %q, preview calls = %d", second.ID, first.ID, previewer.previewCalls)
	}

	input.Record.Value = "192.0.2.21"
	if _, err := service.StartCreateRecordPreview(context.Background(), input); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed request error = %v, want %v", err, ErrIdempotencyConflict)
	}
}
