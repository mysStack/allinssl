package dns

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"ALLinSSL/backend/internal/dnsmodel"
	"github.com/alibabacloud-go/tea/dara"
)

type fakeCredentialStore struct {
	credentials []CredentialSummary
	credential  Credential
	resolveErr  error
}

func (s fakeCredentialStore) List(context.Context) ([]CredentialSummary, error) {
	return s.credentials, nil
}

func (s fakeCredentialStore) Resolve(context.Context, int64) (Credential, error) {
	if s.resolveErr != nil {
		return Credential{}, s.resolveErr
	}
	return s.credential, nil
}

type fakeZoneReader struct {
	zones    []ZoneSummary
	snapshot dnsmodel.Snapshot
}

func (r fakeZoneReader) AddRecord(context.Context, string, dnsmodel.Record) error { return nil }
func (r fakeZoneReader) UpdateRecord(context.Context, string, string, dnsmodel.Record) error {
	return nil
}
func (r fakeZoneReader) DeleteRecord(context.Context, string, string) error            { return nil }
func (r fakeZoneReader) SetRecordStatus(context.Context, string, string, string) error { return nil }

func (r fakeZoneReader) ListZones(context.Context) ([]ZoneSummary, error) {
	return r.zones, nil
}

func (r fakeZoneReader) ReadZone(context.Context, string) (dnsmodel.Snapshot, error) {
	return r.snapshot, nil
}

type fakeReaderFactory struct {
	reader zoneManager
	err    error
	calls  int
}

func (f *fakeReaderFactory) New(Credential) (zoneManager, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.reader, nil
}

func TestServiceListsCredentialSummaries(t *testing.T) {
	service := NewService(
		fakeCredentialStore{credentials: []CredentialSummary{{ID: 7, Name: "AliDNS production", Type: "aliyun"}}},
		&fakeReaderFactory{},
	)

	credentials, err := service.ListCredentials(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 1 || credentials[0] != (CredentialSummary{ID: 7, Name: "AliDNS production", Type: "aliyun"}) {
		t.Fatalf("unexpected credential response: %#v", credentials)
	}
}

func TestServiceRejectsUnavailableCredentialBeforeCreatingReader(t *testing.T) {
	factory := &fakeReaderFactory{}
	service := NewService(fakeCredentialStore{resolveErr: ErrCredential}, factory)

	_, err := service.ListZones(context.Background(), 99)
	if !errors.Is(err, ErrCredential) {
		t.Fatalf("expected credential error, got %v", err)
	}
	if factory.calls != 0 {
		t.Fatalf("reader factory called %d times", factory.calls)
	}
}

func TestServiceReadsZoneUsingResolvedCredential(t *testing.T) {
	want := dnsmodel.Snapshot{Zone: "example.com", SnapshotHash: "snapshot"}
	factory := &fakeReaderFactory{reader: fakeZoneReader{snapshot: want}}
	service := NewService(fakeCredentialStore{credential: Credential{accessKeyID: "id", accessKeySecret: "secret"}}, factory)

	got, err := service.ReadZone(context.Background(), 3, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %#v, want %#v", got, want)
	}
	if factory.calls != 1 {
		t.Fatalf("reader factory called %d times", factory.calls)
	}
}

func TestAliDNSRuntimeOptionsAreAlwaysPresent(t *testing.T) {
	runtime := aliDNSRuntimeOptions()
	if runtime == nil {
		t.Fatal("AliDNS context call requires runtime options")
	}
	if *runtime != (dara.RuntimeOptions{}) {
		t.Fatalf("unexpected default runtime options: %#v", runtime)
	}
}
