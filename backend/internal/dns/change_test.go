package dns

import (
	"errors"
	"testing"

	"ALLinSSL/backend/internal/dnsmodel"
)

func createRecordInt64(value int64) *int64 {
	return &value
}

func createRecordSnapshot(t *testing.T, records ...dnsmodel.Record) dnsmodel.Snapshot {
	t.Helper()

	snapshot, err := dnsmodel.BuildSnapshot("example.com", records, dnsmodel.Limits{
		MinTTL: 600,
		MaxTTL: 86400,
		Known:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Compatible {
		t.Fatalf("fixture snapshot is incompatible: %#v", snapshot)
	}
	return snapshot
}

func createRecordFixture() dnsmodel.Record {
	return dnsmodel.Record{
		ProviderRecordID: "record-1",
		Name:             "www",
		Type:             "A",
		TTL:              600,
		Value:            "192.0.2.1",
		Line:             "default",
		Status:           "ENABLE",
	}
}

func TestBuildCreateRecordCandidateNormalizesAndPreservesCompleteSnapshot(t *testing.T) {
	snapshot := createRecordSnapshot(t, createRecordFixture())

	candidate, record, err := BuildCreateRecordCandidate(snapshot, CreateRecordInput{
		Name: "API", Type: "a", TTL: 600, Value: "192.0.2.10",
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.Name != "api" || record.Type != "A" || record.Value != "192.0.2.10" || record.Line != "default" || record.Status != "ENABLE" {
		t.Fatalf("record = %#v", record)
	}
	if !candidate.Compatible || len(candidate.Records) != len(snapshot.Records)+1 {
		t.Fatalf("candidate = %#v", candidate)
	}
	if candidate.Records[0].ProviderRecordID != "record-1" && candidate.Records[1].ProviderRecordID != "record-1" {
		t.Fatalf("candidate lost existing record: %#v", candidate.Records)
	}
}

func TestBuildCreateRecordCandidateRejectsProtectedAndIncompatibleRecords(t *testing.T) {
	base := createRecordSnapshot(t, createRecordFixture())
	nullMX := dnsmodel.Record{Name: "@", Type: "MX", TTL: 600, Value: ".", Priority: createRecordInt64(0), Line: "default", Status: "ENABLE"}
	cname := dnsmodel.Record{Name: "api", Type: "CNAME", TTL: 600, Value: "target.example.net.", Line: "default", Status: "ENABLE"}
	duplicate := dnsmodel.Record{Name: "api", Type: "A", TTL: 600, Value: "192.0.2.10", Line: "default", Status: "ENABLE"}

	for _, testCase := range []struct {
		name     string
		snapshot dnsmodel.Snapshot
		input    CreateRecordInput
		wantErr  error
	}{
		{
			name: "ACME challenge", snapshot: base,
			input: CreateRecordInput{Name: "_acme-challenge.api", Type: "TXT", TTL: 600, Value: "token"}, wantErr: ErrProtectedRecord,
		},
		{
			name: "apex NS", snapshot: base,
			input: CreateRecordInput{Name: "@", Type: "NS", TTL: 600, Value: "ns1.example.net."}, wantErr: ErrProtectedRecord,
		},
		{
			name: "absolute apex NS", snapshot: base,
			input: CreateRecordInput{Name: "example.com.", Type: "NS", TTL: 600, Value: "ns1.example.net."}, wantErr: ErrProtectedRecord,
		},
		{
			name: "apex SOA", snapshot: base,
			input: CreateRecordInput{Name: "@", Type: "SOA", TTL: 600, Value: "ns1.example.net."}, wantErr: ErrProtectedRecord,
		},
		{
			name: "duplicate", snapshot: createRecordSnapshot(t, duplicate),
			input: CreateRecordInput{Name: "api", Type: "A", TTL: 600, Value: "192.0.2.10"}, wantErr: ErrRecordConflict,
		},
		{
			name: "CNAME conflict", snapshot: createRecordSnapshot(t, cname),
			input: CreateRecordInput{Name: "api", Type: "A", TTL: 600, Value: "192.0.2.10"}, wantErr: ErrRecordConflict,
		},
		{
			name: "Null MX conflict", snapshot: createRecordSnapshot(t, nullMX),
			input: CreateRecordInput{Name: "@", Type: "MX", TTL: 600, Value: "mail.example.net.", Priority: createRecordInt64(10)}, wantErr: ErrRecordConflict,
		},
		{
			name: "TTL below minimum", snapshot: base,
			input: CreateRecordInput{Name: "api", Type: "A", TTL: 599, Value: "192.0.2.10"}, wantErr: ErrInvalidChange,
		},
		{
			name: "TTL above maximum", snapshot: base,
			input: CreateRecordInput{Name: "api", Type: "A", TTL: 86401, Value: "192.0.2.10"}, wantErr: ErrInvalidChange,
		},
		{
			name: "invalid SRV name", snapshot: base,
			input: CreateRecordInput{Name: "_sip.api", Type: "SRV", TTL: 600, Value: "service.example.net.", Priority: createRecordInt64(10), Weight: createRecordInt64(20), Port: createRecordInt64(443)}, wantErr: ErrInvalidChange,
		},
		{
			name: "CAA flags exceed range", snapshot: base,
			input: CreateRecordInput{Name: "caa", Type: "CAA", TTL: 600, Value: "letsencrypt.org", CAAFlags: createRecordInt64(256), CAATag: "issue"}, wantErr: ErrInvalidChange,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			candidate, record, err := BuildCreateRecordCandidate(testCase.snapshot, testCase.input)
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("candidate = %#v, record = %#v, err = %v, want %v", candidate, record, err, testCase.wantErr)
			}
		})
	}
}
