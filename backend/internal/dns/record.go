package dns

import (
	"fmt"
	"strconv"
	"strings"

	"ALLinSSL/backend/internal/dnsmodel"
	alidns "github.com/go-acme/alidns-20150109/v4/client"
)

var (
	ErrInvalidRecord   = fmt.Errorf("DNS_RECORD_INVALID")
	ErrRecordNotFound  = fmt.Errorf("DNS_RECORD_NOT_FOUND")
	ErrProtectedRecord = fmt.Errorf("DNS_PROTECTED_RECORD")
	ErrRecordConflict  = fmt.Errorf("DNS_RECORD_CONFLICT")
)

func normalizeRecordInput(snapshot dnsmodel.Snapshot, input RecordInput, status string) (dnsmodel.Record, error) {
	line := strings.TrimSpace(input.Line)
	if line == "" {
		return dnsmodel.Record{}, ErrInvalidRecord
	}
	record := dnsmodel.Record{
		Name: input.Name, Type: strings.ToUpper(strings.TrimSpace(input.Type)), TTL: input.TTL, Value: input.Value,
		Priority: input.Priority, Weight: input.Weight, Port: input.Port, CAAFlags: input.CAAFlags, CAATag: input.CAATag,
		Remark:              strings.TrimSpace(input.Remark),
		LoadBalancingPolicy: input.LoadBalancingPolicy, LoadBalancingWeight: input.LoadBalancingWeight,
		Line: "default", Status: "ENABLE",
	}
	if record.Type == "A" || record.Type == "AAAA" {
		if record.LoadBalancingPolicy == "" {
			record.LoadBalancingPolicy = "round_robin"
		}
		if record.LoadBalancingPolicy == "round_robin" {
			record.LoadBalancingWeight = nil
		}
	}
	probe, err := dnsmodel.BuildSnapshot(snapshot.Zone, []dnsmodel.Record{record}, snapshot.Limits)
	if err != nil || !probe.Compatible || len(probe.Records) != 1 {
		return dnsmodel.Record{}, ErrInvalidRecord
	}
	normalized := probe.Records[0]
	normalized.Line, normalized.Status = line, status
	return normalized, nil
}

func validateNewRecord(snapshot dnsmodel.Snapshot, record dnsmodel.Record) error {
	if record.Protected {
		return ErrProtectedRecord
	}
	if !allowedRecordType(record.Type) {
		return ErrInvalidRecord
	}
	for _, existing := range snapshot.Records {
		if existing.ProviderRecordID == record.ProviderRecordID {
			continue
		}
		if sameRecordContent(existing, record) {
			return ErrRecordConflict
		}
		if existing.Name == record.Name && existing.Line == record.Line && existing.Type == "CNAME" && record.Type != "CNAME" {
			return ErrRecordConflict
		}
		if existing.Name == record.Name && existing.Line == record.Line && existing.Type != "CNAME" && record.Type == "CNAME" {
			return ErrRecordConflict
		}
		if existing.Name == record.Name && existing.Line == record.Line && existing.Type == "MX" && record.Type == "MX" && existing.Value == "." {
			return ErrRecordConflict
		}
	}
	return nil
}

func allowedRecordType(recordType string) bool {
	switch recordType {
	case "A", "AAAA", "CNAME", "TXT", "MX", "SRV", "CAA":
		return true
	default:
		return false
	}
}

func sameRecordContent(left, right dnsmodel.Record) bool {
	return left.Name == right.Name && left.Type == right.Type && left.TTL == right.TTL && left.Value == right.Value && left.Line == right.Line && pointersEqual(left.Priority, right.Priority) && pointersEqual(left.Weight, right.Weight) && pointersEqual(left.Port, right.Port) && pointersEqual(left.CAAFlags, right.CAAFlags) && left.CAATag == right.CAATag
}

func pointersEqual(left, right *int64) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func findRecord(snapshot dnsmodel.Snapshot, recordID string) (dnsmodel.Record, bool) {
	for _, record := range snapshot.Records {
		if record.ProviderRecordID == recordID {
			return record, true
		}
	}
	return dnsmodel.Record{}, false
}

func removeRecord(snapshot dnsmodel.Snapshot, recordID string) dnsmodel.Snapshot {
	filtered := snapshot
	filtered.Records = make([]dnsmodel.Record, 0, len(snapshot.Records)-1)
	for _, record := range snapshot.Records {
		if record.ProviderRecordID != recordID {
			filtered.Records = append(filtered.Records, record)
		}
	}
	return filtered
}

func addRecordRequest(zone string, record dnsmodel.Record) (*alidns.AddDomainRecordRequest, error) {
	value, err := providerRecordValue(record)
	if err != nil {
		return nil, err
	}
	return &alidns.AddDomainRecordRequest{DomainName: &zone, RR: &record.Name, Type: &record.Type, TTL: &record.TTL, Value: &value, Line: &record.Line, Priority: record.Priority}, nil
}

func updateRecordRequest(zone, recordID string, record dnsmodel.Record) (*alidns.UpdateDomainRecordRequest, error) {
	value, err := providerRecordValue(record)
	if err != nil {
		return nil, err
	}
	return &alidns.UpdateDomainRecordRequest{RecordId: &recordID, RR: &record.Name, Type: &record.Type, TTL: &record.TTL, Value: &value, Line: &record.Line, Priority: record.Priority}, nil
}

func providerRecordValue(record dnsmodel.Record) (string, error) {
	switch record.Type {
	case "SRV":
		if record.Weight == nil || record.Port == nil {
			return "", ErrInvalidRecord
		}
		return fmt.Sprintf("%d %d %s", *record.Weight, *record.Port, record.Value), nil
	case "CAA":
		if record.CAAFlags == nil || record.CAATag == "" {
			return "", ErrInvalidRecord
		}
		return fmt.Sprintf("%d %s %s", *record.CAAFlags, record.CAATag, strconv.Quote(record.Value)), nil
	default:
		return record.Value, nil
	}
}
