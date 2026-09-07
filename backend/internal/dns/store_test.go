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
		)`)
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
		CandidateRecord: CandidateRecordSummary{Name: "api", Type: "A", TTL: 600},
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
	if second.ID != first.ID || second.Kind != JobKindCreateRecordPreview || second.CandidateRecord != request.CandidateRecord {
		t.Fatalf("replayed job = %#v, want ID %q and candidate %#v", second, first.ID, request.CandidateRecord)
	}

	request.RequestHash = "changed-request"
	if _, err := store.CreateChangePreview(context.Background(), request); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed request error = %v, want %v", err, ErrIdempotencyConflict)
	}
}

func TestStoreCreateChangePreviewRejectsUnsafeCandidateSummary(t *testing.T) {
	store := newTestStore(t)
	_, err := store.CreateChangePreview(context.Background(), ChangePreviewRequest{
		Zone: "example.com", CredentialID: 1, ActorID: "local-admin",
		SessionBinding: "session-a", AuthEpoch: "epoch-a", RequestHash: "change-request",
		IdempotencyKey: "change-key", SnapshotHash: "snapshot-a",
		CandidateRecord: CandidateRecordSummary{Name: "api\nsecret", Type: "A", TTL: 600},
	})
	if !errors.Is(err, ErrInvalidChange) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidChange)
	}
}

func TestStoreChangePreviewTerminalStatePersistsSummaryAndReleasesZoneLock(t *testing.T) {
	store := newTestStore(t)
	job, err := store.CreateChangePreview(context.Background(), ChangePreviewRequest{
		Zone: "example.com", CredentialID: 1, ActorID: "local-admin",
		SessionBinding: "session-a", AuthEpoch: "epoch-a", RequestHash: "change-request",
		IdempotencyKey: "change-key", SnapshotHash: "snapshot-a",
		CandidateRecord: CandidateRecordSummary{Name: "api", Type: "A", TTL: 600},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartPreview(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	summary := ChangeSummary{Corrections: 1, Details: []string{"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}}
	job, err = store.FinishChangePreview(context.Background(), job.ID, JobPreviewed, "plan-a", summary, "")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != JobPreviewed || job.PlanHash != "plan-a" || job.ChangeSummary.Corrections != 1 || len(job.ChangeSummary.Details) != 1 {
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
			planHash: "plan-a",
			summary:  ChangeSummary{Corrections: 1, Details: []string{"CREATE api.example.com A 192.0.2.20"}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newTestStore(t)
			job, err := store.CreateChangePreview(context.Background(), ChangePreviewRequest{
				Zone: "example.com", CredentialID: 1, ActorID: "local-admin",
				SessionBinding: "session-a", AuthEpoch: "epoch-a", RequestHash: "change-request",
				IdempotencyKey: "change-key", SnapshotHash: "snapshot-a",
				CandidateRecord: CandidateRecordSummary{Name: "api", Type: "A", TTL: 600},
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
				CandidateRecord: CandidateRecordSummary{Name: "api", Type: "A", TTL: 600},
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
				planHash = "plan-a"
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
