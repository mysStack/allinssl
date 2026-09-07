package dns

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = database.Close() })

	store := NewStore(database)
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestStoreEnsureSchemaMigratesChangePreviewColumnsWithoutReplacingJobs(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = database.Close() })

	_, err = database.Exec(`
		CREATE TABLE dns_change_jobs (
			id TEXT PRIMARY KEY,
			zone TEXT NOT NULL,
			credential_id INTEGER NOT NULL,
			actor_id TEXT NOT NULL,
			session_binding_hash TEXT NOT NULL,
			auth_epoch TEXT NOT NULL,
			state TEXT NOT NULL,
			version INTEGER NOT NULL,
			snapshot_hash TEXT NOT NULL,
			plan_hash TEXT NOT NULL DEFAULT '',
			error_code TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		INSERT INTO dns_change_jobs (
			id, zone, credential_id, actor_id, session_binding_hash, auth_epoch, state, version,
			snapshot_hash, created_at, updated_at
		) VALUES (
			'legacy-job', 'example.com', 7, 'local-admin', 'binding-hash', 'epoch-a', 'adopted', 3,
			'snapshot-a', '2026-09-07T00:00:00Z', '2026-09-07T00:00:01Z'
		);
		CREATE TABLE dns_audit_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			job_id TEXT NOT NULL,
			action TEXT NOT NULL,
			state TEXT NOT NULL,
			created_at TEXT NOT NULL
		);
		INSERT INTO dns_audit_logs (job_id, action, state, created_at)
		VALUES ('legacy-job', 'finished', 'adopted', '2026-09-07T00:00:01Z')`)
	if err != nil {
		t.Fatal(err)
	}

	store := NewStore(database)
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("second migration failed: %v", err)
	}

	var id, kind, candidateRecordSummary, changeSummary string
	if err := database.QueryRow(`
		SELECT id, kind, candidate_record_summary, change_summary
		FROM dns_change_jobs WHERE id = 'legacy-job'`,
	).Scan(&id, &kind, &candidateRecordSummary, &changeSummary); err != nil {
		t.Fatal(err)
	}
	if id != "legacy-job" || kind != "adopt" || candidateRecordSummary != "" || changeSummary != "" {
		t.Fatalf("migrated job = id:%q kind:%q candidate:%q changes:%q", id, kind, candidateRecordSummary, changeSummary)
	}

	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM dns_change_jobs`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("job count = %d, want 1", count)
	}

	var auditJobID, auditAction, auditState, auditZone, auditRecordType, auditRecordName, auditPlanHash, auditErrorCode string
	if err := database.QueryRow(`
		SELECT job_id, action, state, zone, record_type, record_name, plan_hash, error_code
		FROM dns_audit_logs WHERE id = 1`,
	).Scan(&auditJobID, &auditAction, &auditState, &auditZone, &auditRecordType, &auditRecordName, &auditPlanHash, &auditErrorCode); err != nil {
		t.Fatal(err)
	}
	if auditJobID != "legacy-job" || auditAction != "finished" || auditState != "adopted" || auditZone != "" || auditRecordType != "" || auditRecordName != "" || auditPlanHash != "" || auditErrorCode != "" {
		t.Fatalf("legacy audit changed: job=%q action=%q state=%q zone=%q type=%q name=%q plan=%q error=%q", auditJobID, auditAction, auditState, auditZone, auditRecordType, auditRecordName, auditPlanHash, auditErrorCode)
	}
}

func TestStoreRejectsSecondActiveZoneLock(t *testing.T) {
	store := newTestStore(t)
	_, err := store.CreateAdopt(context.Background(), AdoptRequest{
		Zone: "example.com", CredentialID: 1, ActorID: "local-admin",
		SessionBinding: "session-a", AuthEpoch: "epoch-a", RequestHash: "request-a",
		IdempotencyKey: "key-a", SnapshotHash: "snapshot-a",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.CreateAdopt(context.Background(), AdoptRequest{
		Zone: "example.com", CredentialID: 2, ActorID: "local-admin",
		SessionBinding: "session-b", AuthEpoch: "epoch-b", RequestHash: "request-b",
		IdempotencyKey: "key-b", SnapshotHash: "snapshot-b",
	})
	if !errors.Is(err, ErrZoneBusy) {
		t.Fatalf("error = %v, want %v", err, ErrZoneBusy)
	}
}

func TestStoreTerminalJobReleasesZoneLockAndWritesAudit(t *testing.T) {
	store := newTestStore(t)
	job, err := store.CreateAdopt(context.Background(), AdoptRequest{
		Zone: "example.com", CredentialID: 1, ActorID: "local-admin",
		SessionBinding: "session-a", AuthEpoch: "epoch-a", RequestHash: "request-a",
		IdempotencyKey: "key-a", SnapshotHash: "snapshot-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartPreview(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishAdopt(context.Background(), job.ID, JobBlocked, "", "DNS_NONZERO_CORRECTIONS"); err != nil {
		t.Fatal(err)
	}

	if _, err := store.CreateAdopt(context.Background(), AdoptRequest{
		Zone: "example.com", CredentialID: 1, ActorID: "local-admin",
		SessionBinding: "session-b", AuthEpoch: "epoch-b", RequestHash: "request-b",
		IdempotencyKey: "key-b", SnapshotHash: "snapshot-b",
	}); err != nil {
		t.Fatalf("zone lock was not released: %v", err)
	}

	var auditCount int
	if err := store.database.QueryRow(`SELECT COUNT(*) FROM dns_audit_logs WHERE job_id = ?`, job.ID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 3 {
		t.Fatalf("audit count = %d, want 3", auditCount)
	}
}

func TestStoreFinishAdoptKeepsChangePreviewColumnsEmpty(t *testing.T) {
	store := newTestStore(t)
	job, err := store.CreateAdopt(context.Background(), AdoptRequest{
		Zone: "example.com", CredentialID: 1, ActorID: "local-admin",
		SessionBinding: "session-a", AuthEpoch: "epoch-a", RequestHash: "request-a",
		IdempotencyKey: "key-a", SnapshotHash: "snapshot-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartPreview(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishAdopt(context.Background(), job.ID, JobAdopted, "plan-a", ""); err != nil {
		t.Fatal(err)
	}

	var kind, candidateRecordSummary, changeSummary string
	if err := store.database.QueryRow(`
		SELECT kind, candidate_record_summary, change_summary
		FROM dns_change_jobs WHERE id = ?`, job.ID,
	).Scan(&kind, &candidateRecordSummary, &changeSummary); err != nil {
		t.Fatal(err)
	}
	if kind != "adopt" || candidateRecordSummary != "" || changeSummary != "" {
		t.Fatalf("adopt columns = kind:%q candidate:%q changes:%q", kind, candidateRecordSummary, changeSummary)
	}
}

func TestStoreRejectsJobReadFromDifferentSession(t *testing.T) {
	store := newTestStore(t)
	job, err := store.CreateAdopt(context.Background(), AdoptRequest{
		Zone: "example.com", CredentialID: 1, ActorID: "local-admin",
		SessionBinding: "session-a", AuthEpoch: "epoch-a", RequestHash: "request-a",
		IdempotencyKey: "key-a", SnapshotHash: "snapshot-a",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.GetJobForSession(context.Background(), job.ID, "local-admin", "session-b", "epoch-a")
	if !errors.Is(err, ErrJobAccessDenied) {
		t.Fatalf("error = %v, want %v", err, ErrJobAccessDenied)
	}
}

func TestStoreReplaysMatchingIdempotencyKeyAndRejectsChangedRequest(t *testing.T) {
	store := newTestStore(t)
	request := AdoptRequest{
		Zone: "example.com", CredentialID: 1, ActorID: "local-admin",
		SessionBinding: "session-a", AuthEpoch: "epoch-a", RequestHash: "request-a",
		IdempotencyKey: "key-a", SnapshotHash: "snapshot-a",
	}
	first, err := store.CreateAdopt(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateAdopt(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Fatalf("replayed job = %q, want %q", second.ID, first.ID)
	}

	request.RequestHash = "request-b"
	_, err = store.CreateAdopt(context.Background(), request)
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("error = %v, want %v", err, ErrIdempotencyConflict)
	}
}

func TestStoreCreateAdoptRejectsIdempotencyReplayOutsideOriginalSession(t *testing.T) {
	for _, test := range []struct {
		name           string
		sessionBinding string
		authEpoch      string
	}{
		{name: "different session binding", sessionBinding: "session-b", authEpoch: "epoch-a"},
		{name: "different auth epoch", sessionBinding: "session-a", authEpoch: "epoch-b"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newTestStore(t)
			request := AdoptRequest{
				Zone: "example.com", CredentialID: 1, ActorID: "local-admin",
				SessionBinding: "session-a", AuthEpoch: "epoch-a", RequestHash: "adopt-request",
				IdempotencyKey: "adopt-key", SnapshotHash: "snapshot-a",
			}
			if _, err := store.CreateAdopt(context.Background(), request); err != nil {
				t.Fatal(err)
			}

			request.SessionBinding = test.sessionBinding
			request.AuthEpoch = test.authEpoch
			if _, err := store.CreateAdopt(context.Background(), request); !errors.Is(err, ErrIdempotencyConflict) {
				t.Fatalf("replay error = %v, want %v", err, ErrIdempotencyConflict)
			}
		})
	}
}

func TestStoreCreateChangePreviewUsesSeparateIdempotencyActionAndSharedZoneLock(t *testing.T) {
	store := newTestStore(t)
	adoptJob, err := store.CreateAdopt(context.Background(), AdoptRequest{
		Zone: "example.com", CredentialID: 1, ActorID: "local-admin",
		SessionBinding: "session-a", AuthEpoch: "epoch-a", RequestHash: "adopt-request",
		IdempotencyKey: "shared-key", SnapshotHash: "snapshot-a",
	})
	if err != nil {
		t.Fatal(err)
	}

	request := ChangePreviewRequest{
		Zone: "example.com", CredentialID: 1, ActorID: "local-admin",
		SessionBinding: "session-a", AuthEpoch: "epoch-a", RequestHash: "change-request",
		IdempotencyKey: "shared-key", SnapshotHash: "snapshot-a",
		AuditRecord: CandidateRecordSummary{Name: "api", Type: "A", TTL: 600},
	}
	if _, err := store.CreateChangePreview(context.Background(), request); !errors.Is(err, ErrZoneBusy) {
		t.Fatalf("change preview error = %v, want %v", err, ErrZoneBusy)
	}

	if _, err := store.StartPreview(context.Background(), adoptJob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishAdopt(context.Background(), adoptJob.ID, JobBlocked, "", "DNS_TEST_BLOCKED"); err != nil {
		t.Fatal(err)
	}

	first, err := store.CreateChangePreview(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateChangePreview(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.Kind != JobKindCreateRecordPreview || second.CandidateRecord != (CandidateRecordSummary{}) {
		t.Fatalf("replayed job = %#v, want ID %q without an unvalidated candidate summary", second, first.ID)
	}

	request.RequestHash = "changed-request"
	if _, err := store.CreateChangePreview(context.Background(), request); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed request error = %v, want %v", err, ErrIdempotencyConflict)
	}
}

func TestStoreCreateChangePreviewRejectsIdempotencyReplayOutsideOriginalSession(t *testing.T) {
	for _, test := range []struct {
		name           string
		sessionBinding string
		authEpoch      string
	}{
		{name: "different session binding", sessionBinding: "session-b", authEpoch: "epoch-a"},
		{name: "different auth epoch", sessionBinding: "session-a", authEpoch: "epoch-b"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newTestStore(t)
			request := ChangePreviewRequest{
				Zone: "example.com", CredentialID: 1, ActorID: "local-admin",
				SessionBinding: "session-a", AuthEpoch: "epoch-a", RequestHash: "change-request",
				IdempotencyKey: "change-key", SnapshotHash: "snapshot-a",
				AuditRecord: CandidateRecordSummary{Name: "api", Type: "A", TTL: 600},
			}
			if _, err := store.CreateChangePreview(context.Background(), request); err != nil {
				t.Fatal(err)
			}

			request.SessionBinding = test.sessionBinding
			request.AuthEpoch = test.authEpoch
			if _, err := store.CreateChangePreview(context.Background(), request); !errors.Is(err, ErrIdempotencyConflict) {
				t.Fatalf("replay error = %v, want %v", err, ErrIdempotencyConflict)
			}
		})
	}
}

func TestStoreSetChangePreviewCandidateRejectsUnsafeSummary(t *testing.T) {
	store := newTestStore(t)
	job, err := store.CreateChangePreview(context.Background(), ChangePreviewRequest{
		Zone: "example.com", CredentialID: 1, ActorID: "local-admin",
		SessionBinding: "session-a", AuthEpoch: "epoch-a", RequestHash: "change-request",
		IdempotencyKey: "change-key", SnapshotHash: "snapshot-a",
		AuditRecord: CandidateRecordSummary{Name: "api", Type: "A", TTL: 600},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartPreview(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	for _, summary := range []CandidateRecordSummary{
		{Name: "api\nsecret", Type: "A", TTL: 600},
		{Name: "Api", Type: "A", TTL: 600},
		{Name: "api", Type: "TXT;DROP", TTL: 600},
		{Name: "api/../../secret", Type: "A", TTL: 600},
		{Name: "api", Type: "A", TTL: 599},
	} {
		if _, err := store.SetChangePreviewCandidate(context.Background(), job.ID, summary); !errors.Is(err, ErrInvalidChange) {
			t.Fatalf("summary %#v error = %v, want %v", summary, err, ErrInvalidChange)
		}
	}
	var persisted string
	if err := store.database.QueryRow(`SELECT candidate_record_summary FROM dns_change_jobs WHERE id = ?`, job.ID).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != "" {
		t.Fatalf("unsafe candidate persisted: %q", persisted)
	}
}

func TestStoreChangePreviewTerminalStatePersistsSummaryAndReleasesZoneLock(t *testing.T) {
	store := newTestStore(t)
	job, err := store.CreateChangePreview(context.Background(), ChangePreviewRequest{
		Zone: "example.com", CredentialID: 1, ActorID: "local-admin",
		SessionBinding: "session-a", AuthEpoch: "epoch-a", RequestHash: "change-request",
		IdempotencyKey: "change-key", SnapshotHash: "snapshot-a",
		AuditRecord: CandidateRecordSummary{Name: "api", Type: "A", TTL: 600},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartPreview(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetChangePreviewCandidate(context.Background(), job.ID, CandidateRecordSummary{Name: "api", Type: "A", TTL: 600}); err != nil {
		t.Fatal(err)
	}
	summary := ChangeSummary{Corrections: 1, Details: []string{"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}}
	job, err = store.FinishChangePreview(context.Background(), job.ID, JobPreviewed, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", summary, "")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != JobPreviewed || job.PlanHash != "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" || job.ChangeSummary.Corrections != 1 || len(job.ChangeSummary.Details) != 1 {
		t.Fatalf("finished job = %#v", job)
	}

	if _, err := store.CreateAdopt(context.Background(), AdoptRequest{
		Zone: "example.com", CredentialID: 1, ActorID: "local-admin",
		SessionBinding: "session-b", AuthEpoch: "epoch-b", RequestHash: "adopt-request",
		IdempotencyKey: "adopt-key", SnapshotHash: "snapshot-b",
	}); err != nil {
		t.Fatalf("previewed job did not release Zone lock: %v", err)
	}
}

func TestStoreChangePreviewRejectsUnsafeSuccessfulSummary(t *testing.T) {
	for _, test := range []struct {
		name     string
		planHash string
		summary  ChangeSummary
	}{
		{
			name: "missing plan hash",
			summary: ChangeSummary{
				Corrections: 1,
				Details:     []string{"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
			},
		},
		{
			name:     "raw provider detail",
			planHash: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			summary:  ChangeSummary{Corrections: 1, Details: []string{"CREATE api.example.com A 192.0.2.20"}},
		},
		{
			name:     "malformed plan hash",
			planHash: "plan-a\nunsafe",
			summary: ChangeSummary{
				Corrections: 1,
				Details:     []string{"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newTestStore(t)
			job, err := store.CreateChangePreview(context.Background(), ChangePreviewRequest{
				Zone: "example.com", CredentialID: 1, ActorID: "local-admin",
				SessionBinding: "session-a", AuthEpoch: "epoch-a", RequestHash: "change-request",
				IdempotencyKey: "change-key", SnapshotHash: "snapshot-a",
				AuditRecord: CandidateRecordSummary{Name: "api", Type: "A", TTL: 600},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.StartPreview(context.Background(), job.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := store.FinishChangePreview(context.Background(), job.ID, JobPreviewed, test.planHash, test.summary, ""); !errors.Is(err, ErrInvalidChange) {
				t.Fatalf("error = %v, want %v", err, ErrInvalidChange)
			}
		})
	}
}

func TestStoreChangePreviewEveryTerminalStateReleasesZoneLockAndWritesAudit(t *testing.T) {
	for _, state := range []JobState{JobPreviewed, JobBlocked, JobFailed} {
		t.Run(string(state), func(t *testing.T) {
			store := newTestStore(t)
			job, err := store.CreateChangePreview(context.Background(), ChangePreviewRequest{
				Zone: "example.com", CredentialID: 1, ActorID: "local-admin",
				SessionBinding: "session-a", AuthEpoch: "epoch-a", RequestHash: "change-request",
				IdempotencyKey: "change-key", SnapshotHash: "snapshot-a",
				AuditRecord: CandidateRecordSummary{Name: "api", Type: "A", TTL: 600},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.StartPreview(context.Background(), job.ID); err != nil {
				t.Fatal(err)
			}
			summary := ChangeSummary{}
			planHash := ""
			errorCode := "DNS_TEST_TERMINAL"
			if state == JobPreviewed {
				if _, err := store.SetChangePreviewCandidate(context.Background(), job.ID, CandidateRecordSummary{Name: "api", Type: "A", TTL: 600}); err != nil {
					t.Fatal(err)
				}
				planHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
				errorCode = ""
				summary = ChangeSummary{
					Corrections: 1,
					Details:     []string{"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
				}
			}
			if _, err := store.FinishChangePreview(context.Background(), job.ID, state, planHash, summary, errorCode); err != nil {
				t.Fatal(err)
			}

			if _, err := store.CreateAdopt(context.Background(), AdoptRequest{
				Zone: "example.com", CredentialID: 1, ActorID: "local-admin",
				SessionBinding: "session-b", AuthEpoch: "epoch-b", RequestHash: "adopt-request",
				IdempotencyKey: "adopt-key", SnapshotHash: "snapshot-b",
			}); err != nil {
				t.Fatalf("%s job did not release Zone lock: %v", state, err)
			}

			var auditCount int
			if err := store.database.QueryRow(`
				SELECT COUNT(*) FROM dns_audit_logs
				WHERE job_id = ? AND (
					(action = 'created' AND state = 'queued') OR
					(action = 'previewing' AND state = 'previewing') OR
					(action = 'finished' AND state = ?)
				)`, job.ID, state,
			).Scan(&auditCount); err != nil {
				t.Fatal(err)
			}
			if auditCount != 3 {
				t.Fatalf("audit count = %d, want 3", auditCount)
			}
		})
	}
}

func TestStoreFailInterruptedPreservesChangePreviewAuditContextAndReleasesLock(t *testing.T) {
	store := newTestStore(t)
	job, err := store.CreateChangePreview(context.Background(), ChangePreviewRequest{
		Zone: "example.com", CredentialID: 1, ActorID: "local-admin",
		SessionBinding: "session-a", AuthEpoch: "epoch-a", RequestHash: "change-request",
		IdempotencyKey: "change-key", SnapshotHash: "snapshot-a",
		AuditRecord: CandidateRecordSummary{Name: "api", Type: "TXT", TTL: 600},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FailInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}

	var zone, recordType, recordName, errorCode string
	if err := store.database.QueryRow(`
		SELECT zone, record_type, record_name, error_code
		FROM dns_audit_logs WHERE job_id = ? AND action = 'interrupted'`, job.ID,
	).Scan(&zone, &recordType, &recordName, &errorCode); err != nil {
		t.Fatal(err)
	}
	if zone != "example.com" || recordType != "TXT" || recordName != "api" || errorCode != "DNS_INTERRUPTED" {
		t.Fatalf("interrupted audit = zone:%q type:%q name:%q error:%q", zone, recordType, recordName, errorCode)
	}
	if _, err := store.CreateAdopt(context.Background(), AdoptRequest{
		Zone: "example.com", CredentialID: 1, ActorID: "local-admin",
		SessionBinding: "session-b", AuthEpoch: "epoch-b", RequestHash: "adopt-request",
		IdempotencyKey: "adopt-key", SnapshotHash: "snapshot-b",
	}); err != nil {
		t.Fatalf("interrupted change preview left Zone lock held: %v", err)
	}
}

func TestStoreIsAdoptedRequiresMatchingZoneAndCredential(t *testing.T) {
	store := newTestStore(t)
	job, err := store.CreateAdopt(context.Background(), AdoptRequest{
		Zone: "example.com", CredentialID: 1, ActorID: "local-admin",
		SessionBinding: "session-a", AuthEpoch: "epoch-a", RequestHash: "adopt-request",
		IdempotencyKey: "adopt-key", SnapshotHash: "snapshot-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartPreview(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishAdopt(context.Background(), job.ID, JobAdopted, "plan-a", ""); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name         string
		zone         string
		credentialID int64
		want         bool
	}{
		{name: "same credential", zone: "example.com", credentialID: 1, want: true},
		{name: "different credential", zone: "example.com", credentialID: 2, want: false},
		{name: "different zone", zone: "other.example", credentialID: 1, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := store.IsAdopted(context.Background(), test.zone, test.credentialID)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("IsAdopted(%q, %d) = %t, want %t", test.zone, test.credentialID, got, test.want)
			}
		})
	}
}
