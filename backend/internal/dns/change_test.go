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

func TestBuildCreateRecordCandidateRejectsUnsupportedTypesAndAbsoluteACME(t *testing.T) {
	snapshot := createRecordSnapshot(t, createRecordFixture())

	for _, testCase := range []struct {
		name  string
		input CreateRecordInput
		want  error
	}{
		{
			name: "NS", input: CreateRecordInput{Name: "delegated", Type: "NS", TTL: 600, Value: "ns1.example.net."}, want: ErrInvalidChange,
		},
		{
			name: "unsupported type", input: CreateRecordInput{Name: "api", Type: "HTTPS", TTL: 600, Value: "1 ."}, want: ErrInvalidChange,
		},
		{
			name: "absolute ACME challenge", input: CreateRecordInput{Name: "_acme-challenge.example.com.", Type: "TXT", TTL: 600, Value: "token"}, want: ErrProtectedRecord,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, _, err := BuildCreateRecordCandidate(snapshot, testCase.input)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("err = %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestBuildCreateRecordCandidateAcceptsDocumentedBoundaries(t *testing.T) {
	providerLimits := dnsmodel.Limits{MinTTL: 1200, MaxTTL: 1200, Known: true}
	providerLimited, err := dnsmodel.BuildSnapshot("example.com", nil, providerLimits)
	if err != nil || !providerLimited.Compatible {
		t.Fatalf("provider-limited snapshot = %#v, err = %v", providerLimited, err)
	}

	for _, testCase := range []struct {
		name     string
		snapshot dnsmodel.Snapshot
		input    CreateRecordInput
		wantErr  error
	}{
		{
			name: "maximum documented TTL", snapshot: createRecordSnapshot(t),
			input: CreateRecordInput{Name: "ttl-max", Type: "A", TTL: 86400, Value: "192.0.2.10"},
		},
		{
			name: "effective provider TTL intersection", snapshot: providerLimited,
			input: CreateRecordInput{Name: "provider-ttl", Type: "A", TTL: 1200, Value: "192.0.2.10"},
		},
		{
			name: "provider TTL above intersection", snapshot: providerLimited,
			input: CreateRecordInput{Name: "provider-ttl", Type: "A", TTL: 1201, Value: "192.0.2.10"}, wantErr: ErrInvalidChange,
		},
		{
			name: "SRV zero numeric fields", snapshot: createRecordSnapshot(t),
			input: CreateRecordInput{Name: "_sip._tcp", Type: "SRV", TTL: 600, Value: "service.example.net.", Priority: createRecordInt64(0), Weight: createRecordInt64(0), Port: createRecordInt64(0)},
		},
		{
			name: "SRV maximum numeric fields", snapshot: createRecordSnapshot(t),
			input: CreateRecordInput{Name: "_sip._tcp", Type: "SRV", TTL: 600, Value: "service.example.net.", Priority: createRecordInt64(65535), Weight: createRecordInt64(65535), Port: createRecordInt64(65535)},
		},
		{
			name: "CAA zero flag", snapshot: createRecordSnapshot(t),
			input: CreateRecordInput{Name: "caa-zero", Type: "CAA", TTL: 600, Value: "letsencrypt.org", CAAFlags: createRecordInt64(0), CAATag: "issue"},
		},
		{
			name: "CAA maximum flag", snapshot: createRecordSnapshot(t),
			input: CreateRecordInput{Name: "caa-maximum", Type: "CAA", TTL: 600, Value: "letsencrypt.org", CAAFlags: createRecordInt64(255), CAATag: "issue"},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			candidate, record, err := BuildCreateRecordCandidate(testCase.snapshot, testCase.input)
			if testCase.wantErr != nil {
				if !errors.Is(err, testCase.wantErr) {
					t.Fatalf("candidate = %#v, record = %#v, err = %v, want %v", candidate, record, err, testCase.wantErr)
				}
				return
			}
			if err != nil || !candidate.Compatible || len(candidate.Records) != len(testCase.snapshot.Records)+1 {
				t.Fatalf("candidate = %#v, record = %#v, err = %v", candidate, record, err)
			}
		})
	}
}

func TestBuildCreateRecordCandidatePreservesExistingMarkerLikeProviderID(t *testing.T) {
	existing := createRecordFixture()
	existing.ProviderRecordID = "dnscontrol-create-candidate"
	existing.Name = "aaa"
	snapshot := createRecordSnapshot(t, existing)

	candidate, record, err := BuildCreateRecordCandidate(snapshot, CreateRecordInput{
		Name: "zzz", Type: "A", TTL: 600, Value: "192.0.2.10",
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.Name != "zzz" {
		t.Fatalf("returned record = %#v, want zzz candidate", record)
	}
	foundExisting := false
	for _, candidateRecord := range candidate.Records {
		if candidateRecord.Name == "aaa" && candidateRecord.ProviderRecordID != "dnscontrol-create-candidate" {
			t.Fatalf("existing ProviderRecordID changed: %#v", candidateRecord)
		}
		if candidateRecord.Name == "aaa" {
			foundExisting = true
		}
	}
	if !foundExisting {
		t.Fatalf("candidate lost existing record: %#v", candidate.Records)
	}
}

func TestBuildCreateRecordCandidateClassifiesUnicodeAbsoluteProtectedOwners(t *testing.T) {
	snapshot, err := dnsmodel.BuildSnapshot("例子.中国", nil, dnsmodel.Limits{MinTTL: 600, MaxTTL: 86400, Known: true})
	if err != nil || !snapshot.Compatible {
		t.Fatalf("snapshot = %#v, err = %v", snapshot, err)
	}

	for _, testCase := range []CreateRecordInput{
		{Name: "例子.中国.", Type: "NS", TTL: 600, Value: "ns1.example.net."},
		{Name: "_acme-challenge.例子.中国.", Type: "TXT", TTL: 600, Value: "token"},
	} {
		_, _, err := BuildCreateRecordCandidate(snapshot, testCase)
		if !errors.Is(err, ErrProtectedRecord) {
			t.Fatalf("input = %#v, err = %v, want %v", testCase, err, ErrProtectedRecord)
		}
	}
}
