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
	record := dnsmodel.Record{
		ProviderRecordID: createRecordMarker,
		Name:             input.Name,
		Type:             input.Type,
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
	if protectedCreateRecord(snapshot.Zone, record) {
		return dnsmodel.Snapshot{}, dnsmodel.Record{}, ErrProtectedRecord
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

func protectedCreateRecord(zone string, record dnsmodel.Record) bool {
	name := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(record.Name), "."))
	zone = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(zone), "."))
	if name == zone {
		name = "@"
	}
	typ := strings.ToUpper(strings.TrimSpace(record.Type))
	return name == "_acme-challenge" || strings.HasPrefix(name, "_acme-challenge.") || (name == "@" && (typ == "NS" || typ == "SOA"))
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
