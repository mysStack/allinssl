package dns

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ALLinSSL/backend/internal/dnscontrol"
	"ALLinSSL/backend/internal/dnsmodel"
)

type fakeSnapshotReader struct {
	snapshot dnsmodel.Snapshot
	err      error
}

func (reader fakeSnapshotReader) ReadZone(context.Context, int64, string) (dnsmodel.Snapshot, error) {
	return reader.snapshot, reader.err
}

type fakePreviewer struct {
	plan         dnscontrol.PreviewPlan
	healthErr    error
	previewErr   error
	healthCalls  int
	previewCalls int
	inputs       []dnscontrol.PreviewInput
}

func (previewer *fakePreviewer) Health(context.Context) (dnscontrol.EngineInfo, error) {
	previewer.healthCalls++
	return dnscontrol.EngineInfo{Version: dnscontrol.ExpectedVersion}, previewer.healthErr
}

func (previewer *fakePreviewer) Preview(_ context.Context, input dnscontrol.PreviewInput) (dnscontrol.PreviewPlan, error) {
	previewer.previewCalls++
	previewer.inputs = append(previewer.inputs, input)
	if len(input.Artifacts.Config) == 0 || len(input.Artifacts.Credentials) == 0 {
		return dnscontrol.PreviewPlan{}, ErrInvalidAdopt
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
	rawDetail := "CREATE api.example.com A 192.0.2.20"
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
		job.ChangeSummary.Details[0] != "sha256:353db4e087eac352c971aadbc0a7e11fee75000908e04104a26a90f5aad79dca" ||
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
	for _, test := range []struct {
		name string
		plan dnscontrol.PreviewPlan
	}{
		{name: "wrong zone", plan: dnscontrol.PreviewPlan{Zone: "other.example", Provider: "ALIDNS", Corrections: 1, Details: []string{"create"}}},
		{name: "wrong provider", plan: dnscontrol.PreviewPlan{Zone: "example.com", Provider: "BIND", Corrections: 1, Details: []string{"create"}}},
		{name: "zero corrections", plan: dnscontrol.PreviewPlan{Zone: "example.com", Provider: "ALIDNS", Corrections: 0, Details: []string{"create"}}},
		{name: "negative corrections", plan: dnscontrol.PreviewPlan{Zone: "example.com", Provider: "ALIDNS", Corrections: -1, Details: []string{"create"}}},
		{name: "missing details", plan: dnscontrol.PreviewPlan{Zone: "example.com", Provider: "ALIDNS", Corrections: 1}},
		{name: "blank detail", plan: dnscontrol.PreviewPlan{Zone: "example.com", Provider: "ALIDNS", Corrections: 1, Details: []string{" \t"}}},
		{name: "invalid utf8 detail", plan: dnscontrol.PreviewPlan{Zone: "example.com", Provider: "ALIDNS", Corrections: 1, Details: []string{string([]byte{0xff})}}},
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
		Zone: "example.com", Provider: "ALIDNS", Corrections: 1, Details: []string{"create"},
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
