package dns

import (
	"context"
	"errors"
	"testing"

	"ALLinSSL/backend/internal/dnsmodel"
)

type fakeZoneManager struct {
	snapshots []dnsmodel.Snapshot
	add       []dnsmodel.Record
	updates   []recordUpdate
	deletes   []string
	statuses  []recordStatus
	loadBalances []recordUpdate
	addErr    error
}

func (f *fakeZoneManager) ListZones(context.Context) ([]ZoneSummary, error) {
	return nil, nil
}

func (f *fakeZoneManager) ReadZone(context.Context, string) (dnsmodel.Snapshot, error) {
	snapshot := f.snapshots[0]
	f.snapshots = f.snapshots[1:]
	return snapshot, nil
}

func (f *fakeZoneManager) AddRecord(_ context.Context, _ string, record dnsmodel.Record) error {
	f.add = append(f.add, record)
	return f.addErr
}

func (f *fakeZoneManager) UpdateRecord(_ context.Context, _ string, recordID string, record dnsmodel.Record) error {
	f.updates = append(f.updates, recordUpdate{id: recordID, record: record})
	return nil
}

func (f *fakeZoneManager) DeleteRecord(_ context.Context, _ string, recordID string) error {
	f.deletes = append(f.deletes, recordID)
	return nil
}

func (f *fakeZoneManager) SetRecordStatus(_ context.Context, _ string, recordID, status string) error {
	f.statuses = append(f.statuses, recordStatus{id: recordID, status: status})
	return nil
}

func (f *fakeZoneManager) SetRecordLoadBalancing(_ context.Context, _ string, recordID string, record dnsmodel.Record) error {
	f.loadBalances = append(f.loadBalances, recordUpdate{id: recordID, record: record})
	return nil
}

type recordUpdate struct {
	id     string
	record dnsmodel.Record
}

type recordStatus struct {
	id     string
	status string
}

func TestServiceCreateRecordNormalizesThenRefreshesZone(t *testing.T) {
	before := testRecordSnapshot(t, dnsmodel.Record{ProviderRecordID: "1", Name: "www", Type: "A", TTL: 600, Value: "192.0.2.10", Line: "default", Status: "ENABLE"})
	after := testRecordSnapshot(t,
		dnsmodel.Record{ProviderRecordID: "1", Name: "www", Type: "A", TTL: 600, Value: "192.0.2.10", Line: "default", Status: "ENABLE"},
		dnsmodel.Record{ProviderRecordID: "2", Name: "api", Type: "A", TTL: 600, Value: "192.0.2.20", Line: "default", Status: "ENABLE"},
	)
	manager := &fakeZoneManager{snapshots: []dnsmodel.Snapshot{before, after, after}}
	service := NewService(fakeCredentialStore{credential: Credential{accessKeyID: "id", accessKeySecret: "secret"}}, &fakeReaderFactory{reader: manager})

	snapshot, err := service.CreateRecord(context.Background(), 1, "example.com", RecordInput{Name: "API", Type: "a", TTL: 600, Value: "192.0.2.20", Line: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if len(manager.add) != 1 || manager.add[0].Name != "api" || manager.add[0].Type != "A" || manager.add[0].Value != "192.0.2.20" || manager.add[0].LoadBalancingPolicy != "round_robin" {
		t.Fatalf("add requests = %#v", manager.add)
	}
	if len(manager.loadBalances) != 1 || manager.loadBalances[0].id != "2" || manager.loadBalances[0].record.LoadBalancingPolicy != "round_robin" {
		t.Fatalf("load-balancing requests = %#v", manager.loadBalances)
	}
	if len(snapshot.Records) != 2 || len(manager.snapshots) != 0 {
		t.Fatalf("snapshot records = %d, remaining reads = %d", len(snapshot.Records), len(manager.snapshots))
	}
}

func TestServiceUpdatesDeletesAndChangesStatusOnlyForCurrentZoneRecord(t *testing.T) {
	before := testRecordSnapshot(t, dnsmodel.Record{ProviderRecordID: "1", Name: "www", Type: "A", TTL: 600, Value: "192.0.2.10", Line: "default", Status: "ENABLE"})
	afterUpdate := testRecordSnapshot(t, dnsmodel.Record{ProviderRecordID: "1", Name: "www", Type: "A", TTL: 600, Value: "192.0.2.20", Line: "default", Status: "ENABLE"})
	afterDelete := testRecordSnapshot(t)
	afterDisable := testRecordSnapshot(t, dnsmodel.Record{ProviderRecordID: "1", Name: "www", Type: "A", TTL: 600, Value: "192.0.2.10", Line: "default", Status: "DISABLE"})
	manager := &fakeZoneManager{snapshots: []dnsmodel.Snapshot{before, afterUpdate, before, afterDelete, before, afterDisable}}
	service := NewService(fakeCredentialStore{credential: Credential{accessKeyID: "id", accessKeySecret: "secret"}}, &fakeReaderFactory{reader: manager})

	if _, err := service.UpdateRecord(context.Background(), 1, "example.com", "1", RecordInput{Name: "www", Type: "A", TTL: 600, Value: "192.0.2.20", Line: "default"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DeleteRecord(context.Background(), 1, "example.com", "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetRecordStatus(context.Background(), 1, "example.com", "1", "DISABLE"); err != nil {
		t.Fatal(err)
	}

	if len(manager.updates) != 1 || manager.updates[0].id != "1" || manager.updates[0].record.Value != "192.0.2.20" {
		t.Fatalf("updates = %#v", manager.updates)
	}
	if len(manager.deletes) != 1 || manager.deletes[0] != "1" {
		t.Fatalf("deletes = %#v", manager.deletes)
	}
	if len(manager.statuses) != 1 || manager.statuses[0] != (recordStatus{id: "1", status: "DISABLE"}) {
		t.Fatalf("statuses = %#v", manager.statuses)
	}
}

func TestServiceRejectsProtectedMissingAndInvalidRecords(t *testing.T) {
	base := testRecordSnapshot(t, dnsmodel.Record{ProviderRecordID: "acme-1", Name: "_acme-challenge", Type: "TXT", TTL: 600, Value: "token", Line: "default", Status: "ENABLE"})
	manager := &fakeZoneManager{snapshots: []dnsmodel.Snapshot{base, base, base}}
	service := NewService(fakeCredentialStore{credential: Credential{accessKeyID: "id", accessKeySecret: "secret"}}, &fakeReaderFactory{reader: manager})

	if _, err := service.CreateRecord(context.Background(), 1, "example.com", RecordInput{Name: "_acme-challenge.api", Type: "TXT", TTL: 600, Value: "token", Line: "default"}); !errors.Is(err, ErrProtectedRecord) {
		t.Fatalf("create protected err = %v", err)
	}
	if _, err := service.UpdateRecord(context.Background(), 1, "example.com", "missing", RecordInput{Name: "api", Type: "A", TTL: 600, Value: "192.0.2.20", Line: "default"}); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("update missing err = %v", err)
	}
	if _, err := service.DeleteRecord(context.Background(), 1, "example.com", "acme-1"); !errors.Is(err, ErrProtectedRecord) {
		t.Fatalf("delete protected err = %v", err)
	}
}

func TestServiceAllowsWritesWhenOtherRecordsUseSpecialLinesOrAreDisabled(t *testing.T) {
	before := testRecordSnapshot(t, dnsmodel.Record{ProviderRecordID: "legacy", Name: "legacy", Type: "A", TTL: 600, Value: "192.0.2.10", Line: "telecom", Status: "DISABLE", Metadata: map[string]string{"remark": "keep"}})
	after := testRecordSnapshot(t,
		dnsmodel.Record{ProviderRecordID: "legacy", Name: "legacy", Type: "A", TTL: 600, Value: "192.0.2.10", Line: "telecom", Status: "DISABLE", Metadata: map[string]string{"remark": "keep"}},
		dnsmodel.Record{ProviderRecordID: "api", Name: "api", Type: "A", TTL: 600, Value: "192.0.2.20", Line: "default", Status: "ENABLE"},
	)
	manager := &fakeZoneManager{snapshots: []dnsmodel.Snapshot{before, after, after}}
	service := NewService(fakeCredentialStore{credential: Credential{accessKeyID: "id", accessKeySecret: "secret"}}, &fakeReaderFactory{reader: manager})

	if _, err := service.CreateRecord(context.Background(), 1, "example.com", RecordInput{Name: "api", Type: "A", TTL: 600, Value: "192.0.2.20", Line: "default"}); err != nil {
		t.Fatalf("special existing records blocked create: %v", err)
	}
}

func TestServiceDoesNotRetryUncertainWrite(t *testing.T) {
	before := testRecordSnapshot(t)
	manager := &fakeZoneManager{snapshots: []dnsmodel.Snapshot{before}, addErr: ErrWriteUncertain}
	service := NewService(fakeCredentialStore{credential: Credential{accessKeyID: "id", accessKeySecret: "secret"}}, &fakeReaderFactory{reader: manager})

	if _, err := service.CreateRecord(context.Background(), 1, "example.com", RecordInput{Name: "api", Type: "A", TTL: 600, Value: "192.0.2.20", Line: "default"}); !errors.Is(err, ErrWriteUncertain) {
		t.Fatalf("write err = %v", err)
	}
	if len(manager.add) != 1 || len(manager.snapshots) != 0 {
		t.Fatalf("unexpected retries: adds=%d reads=%d", len(manager.add), len(manager.snapshots))
	}
}

func TestServiceRejectsWhitespaceOnlyLine(t *testing.T) {
	snapshot := testRecordSnapshot(t)
	manager := &fakeZoneManager{snapshots: []dnsmodel.Snapshot{snapshot, snapshot}}
	service := NewService(fakeCredentialStore{credential: Credential{accessKeyID: "id", accessKeySecret: "secret"}}, &fakeReaderFactory{reader: manager})

	if _, err := service.CreateRecord(context.Background(), 1, "example.com", RecordInput{Name: "api", Type: "A", TTL: 600, Value: "192.0.2.20", Line: "  "}); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("whitespace line err = %v", err)
	}
}

func TestServiceAppliesARecordLoadBalancingPolicy(t *testing.T) {
	weight := int64(20)
	before := testRecordSnapshot(t, dnsmodel.Record{ProviderRecordID: "1", Name: "api", Type: "A", TTL: 600, Value: "192.0.2.10", Line: "default", Status: "ENABLE"})
	after := testRecordSnapshot(t, dnsmodel.Record{ProviderRecordID: "1", Name: "api", Type: "A", TTL: 600, Value: "192.0.2.10", Line: "default", Status: "ENABLE", LoadBalancingPolicy: "weight", LoadBalancingWeight: &weight})
	manager := &fakeZoneManager{snapshots: []dnsmodel.Snapshot{before, after}}
	service := NewService(fakeCredentialStore{credential: Credential{accessKeyID: "id", accessKeySecret: "secret"}}, &fakeReaderFactory{reader: manager})

	if _, err := service.UpdateRecord(context.Background(), 1, "example.com", "1", RecordInput{Name: "api", Type: "A", TTL: 600, Value: "192.0.2.10", Line: "default", LoadBalancingPolicy: "weight", LoadBalancingWeight: &weight}); err != nil {
		t.Fatal(err)
	}
	if len(manager.loadBalances) != 1 || manager.loadBalances[0].id != "1" || manager.loadBalances[0].record.LoadBalancingPolicy != "weight" || manager.loadBalances[0].record.LoadBalancingWeight == nil || *manager.loadBalances[0].record.LoadBalancingWeight != 20 {
		t.Fatalf("load-balancing requests = %#v", manager.loadBalances)
	}
}

func testRecordSnapshot(t *testing.T, records ...dnsmodel.Record) dnsmodel.Snapshot {
	t.Helper()
	snapshot, err := dnsmodel.BuildSnapshot("example.com", records, dnsmodel.Limits{MinTTL: 600, MaxTTL: 86400, Known: true})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
