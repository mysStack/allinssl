package dns

import (
	"strings"

	"ALLinSSL/backend/internal/dnsmodel"
)

const createRecordMarker = "dnscontrol-create-candidate"

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
	if protectedCreateRecord(snapshot.Zone, input.Name, recordType) {
		return dnsmodel.Snapshot{}, dnsmodel.Record{}, ErrProtectedRecord
	}
	if !createRecordTypeAllowed(recordType) {
		return dnsmodel.Snapshot{}, dnsmodel.Record{}, ErrInvalidChange
	}

	record := dnsmodel.Record{
		ProviderRecordID: createRecordMarker,
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
	if err != nil || !candidate.Compatible {
		return dnsmodel.Snapshot{}, dnsmodel.Record{}, changeError(candidate)
	}
	if len(candidate.Records) != len(snapshot.Records)+1 {
		return dnsmodel.Snapshot{}, dnsmodel.Record{}, ErrInvalidChange
	}

	var normalized dnsmodel.Record
	found := false
	for index := range candidate.Records {
		if candidate.Records[index].ProviderRecordID != createRecordMarker {
			continue
		}
		normalized = candidate.Records[index]
		normalized.ProviderRecordID = ""
		candidate.Records[index] = normalized
		found = true
		break
	}
	if !found {
		return dnsmodel.Snapshot{}, dnsmodel.Record{}, ErrInvalidChange
	}

	candidate, err = dnsmodel.BuildSnapshot(candidate.Zone, candidate.Records, candidate.Limits)
	if err != nil || !candidate.Compatible || len(candidate.Records) != len(snapshot.Records)+1 {
		return dnsmodel.Snapshot{}, dnsmodel.Record{}, changeError(candidate)
	}
	return candidate, normalized, nil
}

func protectedCreateRecord(zone, owner, recordType string) bool {
	name := normalizeCreateOwner(zone, owner)
	return name == "_acme-challenge" || strings.HasPrefix(name, "_acme-challenge.") || (name == "@" && (recordType == "NS" || recordType == "SOA"))
}

func createRecordTypeAllowed(recordType string) bool {
	switch recordType {
	case "A", "AAAA", "CNAME", "TXT", "MX", "SRV", "CAA":
		return true
	default:
		return false
	}
}

func normalizeCreateOwner(zone, owner string) string {
	owner = strings.TrimSpace(owner)
	absolute := strings.HasSuffix(owner, ".")
	name := strings.ToLower(strings.TrimSuffix(owner, "."))
	zone = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(zone), "."))
	if !absolute {
		return name
	}
	if name == zone {
		return "@"
	}
	if strings.HasSuffix(name, "."+zone) {
		return strings.TrimSuffix(name, "."+zone)
	}
	return name
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
