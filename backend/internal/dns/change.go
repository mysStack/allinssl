package dns

import (
	"crypto/rand"
	"encoding/hex"
	"strings"

	"ALLinSSL/backend/internal/dnsmodel"
)

const createRecordMarkerPrefix = "dnscontrol-create-candidate"

type CreateRecordInput struct {
	Name     string
	Type     string
	TTL      int64
	Value    string
	Priority *int64
	Weight   *int64
	Port     *int64
	CAAFlags *int64
	CAATag   string
}

func BuildCreateRecordCandidate(snapshot dnsmodel.Snapshot, input CreateRecordInput) (dnsmodel.Snapshot, dnsmodel.Record, error) {
	recordType := strings.ToUpper(strings.TrimSpace(input.Type))
	marker, err := createCandidateMarker(snapshot.Records)
	if err != nil {
		return dnsmodel.Snapshot{}, dnsmodel.Record{}, ErrInvalidChange
	}

	record := dnsmodel.Record{
		ProviderRecordID: marker,
		Name:             input.Name,
		Type:             recordType,
		TTL:              input.TTL,
		Value:            input.Value,
		Priority:         input.Priority,
		Weight:           input.Weight,
		Port:             input.Port,
		CAAFlags:         input.CAAFlags,
		CAATag:           input.CAATag,
		Line:             "default",
		Status:           "ENABLE",
	}
	records := append(append([]dnsmodel.Record(nil), snapshot.Records...), record)
	candidate, err := dnsmodel.BuildSnapshot(snapshot.Zone, records, snapshot.Limits)
	if err != nil {
		return dnsmodel.Snapshot{}, dnsmodel.Record{}, ErrInvalidChange
	}
	if len(candidate.Records) != len(snapshot.Records)+1 {
		return dnsmodel.Snapshot{}, dnsmodel.Record{}, ErrInvalidChange
	}

	normalized, candidateIndex, found := candidateRecordByMarker(candidate, marker)
	if !found {
		return dnsmodel.Snapshot{}, dnsmodel.Record{}, ErrInvalidChange
	}
	if normalized.Protected {
		return dnsmodel.Snapshot{}, dnsmodel.Record{}, ErrProtectedRecord
	}
	if !createRecordTypeAllowed(recordType) || !candidate.Compatible {
		return dnsmodel.Snapshot{}, dnsmodel.Record{}, changeError(candidate)
	}
	normalized.ProviderRecordID = ""
	candidate.Records[candidateIndex].ProviderRecordID = ""

	candidate, err = dnsmodel.BuildSnapshot(candidate.Zone, candidate.Records, candidate.Limits)
	if err != nil || !candidate.Compatible || len(candidate.Records) != len(snapshot.Records)+1 {
		return dnsmodel.Snapshot{}, dnsmodel.Record{}, changeError(candidate)
	}
	return candidate, normalized, nil
}

func createRecordTypeAllowed(recordType string) bool {
	switch recordType {
	case "A", "AAAA", "CNAME", "TXT", "MX", "SRV", "CAA":
		return true
	default:
		return false
	}
}

func createCandidateMarker(records []dnsmodel.Record) (string, error) {
	existing := make(map[string]struct{}, len(records))
	for _, record := range records {
		existing[record.ProviderRecordID] = struct{}{}
	}
	for attempts := 0; attempts < 8; attempts++ {
		bytes := make([]byte, 16)
		if _, err := rand.Read(bytes); err != nil {
			return "", err
		}
		marker := createRecordMarkerPrefix + ":" + hex.EncodeToString(bytes)
		if _, found := existing[marker]; !found {
			return marker, nil
		}
	}
	return "", ErrInvalidChange
}

func candidateRecordByMarker(candidate dnsmodel.Snapshot, marker string) (dnsmodel.Record, int, bool) {
	for index, record := range candidate.Records {
		if record.ProviderRecordID == marker {
			return record, index, true
		}
	}
	return dnsmodel.Record{}, 0, false
}

func changeError(snapshot dnsmodel.Snapshot) error {
	for _, reason := range snapshot.ReadOnlyReasons {
		switch reason {
		case "DUPLICATE_RECORD", "CNAME_CONFLICT", "NULL_MX_CONFLICT":
			return ErrRecordConflict
		}
	}
	return ErrInvalidChange
}
