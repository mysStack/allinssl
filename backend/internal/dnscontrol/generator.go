package dnscontrol

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"ALLinSSL/backend/internal/dnsmodel"
)

const defaultAliDNSRegion = "cn-hangzhou"

type AliDNSCredentials struct {
	AccessKeyID     string
	AccessKeySecret string
	RegionID        string
}

type Artifacts struct {
	Config      []byte
	Credentials []byte
}

type aliDNSAuthorization struct {
	Type            string `json:"TYPE"`
	AccessKeyID     string `json:"access_key_id"`
	AccessKeySecret string `json:"access_key_secret"`
	RegionID        string `json:"region_id"`
}

type credentialsFile struct {
	AliDNS aliDNSAuthorization `json:"alidns"`
}

func GenerateArtifacts(snapshot dnsmodel.Snapshot, credentials AliDNSCredentials) (Artifacts, error) {
	if !snapshot.Compatible {
		return Artifacts{}, ErrIncompatibleSnapshot
	}
	zone, err := dnsmodel.NormalizeZone(snapshot.Zone)
	if err != nil || zone != snapshot.Zone {
		return Artifacts{}, ErrInvalidZone
	}
	if strings.TrimSpace(credentials.AccessKeyID) == "" || strings.TrimSpace(credentials.AccessKeySecret) == "" {
		return Artifacts{}, ErrInvalidCredentials
	}

	records := make([]dnsmodel.Record, 0, len(snapshot.Records))
	for _, record := range snapshot.Records {
		if !record.Protected {
			records = append(records, record)
		}
	}
	sort.Slice(records, func(left, right int) bool {
		return recordSortKey(records[left]) < recordSortKey(records[right])
	})

	defaultTTL := minimumTTL(records, snapshot.Limits.MinTTL)
	lines := make([]string, 0, len(records)+7)
	lines = append(lines,
		`var REG_NONE = NewRegistrar("none");`,
		`var DSP_ALIDNS = NewDnsProvider("alidns");`,
		`function CAA_FLAGS(value) {`,
		`  return function(record) {`,
		`    record.caaflag = value;`,
		`  };`,
		`}`,
		"",
		fmt.Sprintf("D(%s, REG_NONE, DnsProvider(DSP_ALIDNS), DefaultTTL(%d),", quote(zone), defaultTTL),
		`  IGNORE("_acme-challenge", "*"),`,
		`  IGNORE("_acme-challenge.**", "*"),`,
		`  IGNORE("@", "NS,SOA"),`,
	)
	for _, record := range records {
		line, err := recordDSL(record, defaultTTL)
		if err != nil {
			return Artifacts{}, err
		}
		lines = append(lines, "  "+line+",")
	}
	lines = append(lines, `);`, "")

	regionID := credentials.RegionID
	if regionID == "" {
		regionID = defaultAliDNSRegion
	}
	credentialJSON, err := json.Marshal(credentialsFile{AliDNS: aliDNSAuthorization{
		Type:            "ALIDNS",
		AccessKeyID:     credentials.AccessKeyID,
		AccessKeySecret: credentials.AccessKeySecret,
		RegionID:        regionID,
	}})
	if err != nil {
		return Artifacts{}, fmt.Errorf("marshal AliDNS credentials: %w", err)
	}

	return Artifacts{
		Config:      []byte(strings.Join(lines, "\n")),
		Credentials: credentialJSON,
	}, nil
}

func minimumTTL(records []dnsmodel.Record, minimum int64) int64 {
	defaultTTL := minimum
	if defaultTTL < 600 {
		defaultTTL = 600
	}
	for _, record := range records {
		if record.TTL < defaultTTL {
			defaultTTL = record.TTL
		}
	}
	return defaultTTL
}

func recordSortKey(record dnsmodel.Record) string {
	return strings.Join([]string{
		record.Name,
		record.Type,
		record.Value,
		strconv.FormatInt(valueOrZero(record.Priority), 10),
		strconv.FormatInt(valueOrZero(record.Weight), 10),
		strconv.FormatInt(valueOrZero(record.Port), 10),
		strconv.FormatInt(valueOrZero(record.CAAFlags), 10),
		record.CAATag,
		strconv.FormatInt(record.TTL, 10),
	}, "\x00")
}

func valueOrZero(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func recordDSL(record dnsmodel.Record, defaultTTL int64) (string, error) {
	arguments := []string{quote(record.Name)}
	switch record.Type {
	case "A", "AAAA", "CNAME", "NS", "TXT":
		arguments = append(arguments, quote(record.Value))
	case "MX":
		if record.Priority == nil {
			return "", ErrUnrepresentableRecord
		}
		arguments = append(arguments, strconv.FormatInt(*record.Priority, 10), quote(record.Value))
	case "SRV":
		if record.Priority == nil || record.Weight == nil || record.Port == nil {
			return "", ErrUnrepresentableRecord
		}
		arguments = append(arguments,
			strconv.FormatInt(*record.Priority, 10),
			strconv.FormatInt(*record.Weight, 10),
			strconv.FormatInt(*record.Port, 10),
			quote(record.Value),
		)
	case "CAA":
		if record.CAAFlags == nil || record.CAATag == "" {
			return "", ErrUnrepresentableRecord
		}
		arguments = append(arguments, quote(record.CAATag), quote(record.Value))
		if *record.CAAFlags != 0 {
			arguments = append(arguments, fmt.Sprintf("CAA_FLAGS(%d)", *record.CAAFlags))
		}
	default:
		return "", ErrUnrepresentableRecord
	}
	if record.TTL != defaultTTL {
		arguments = append(arguments, fmt.Sprintf("TTL(%d)", record.TTL))
	}
	return record.Type + "(" + strings.Join(arguments, ", ") + ")", nil
}

func quote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
