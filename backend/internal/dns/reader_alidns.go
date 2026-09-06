// Package dns implements isolated business DNS services. It does not initialize
// the application database or register HTTP routes.
package dns

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"ALLinSSL/backend/internal/dnsmodel"
	alidns "github.com/go-acme/alidns-20150109/v4/client"
)

var (
	ErrReadFailed  = errors.New("DNS_READ_FAILED")
	ErrIncomplete  = errors.New("DNS_INCOMPLETE_RESPONSE")
	ErrReadLimit   = errors.New("DNS_READ_LIMIT")
	ErrRemoteDrift = errors.New("REMOTE_DRIFT")
)

// AliDNSAPI deliberately exposes only read operations.
type AliDNSAPI interface {
	DomainInfo(context.Context, *alidns.DescribeDomainInfoRequest) (*alidns.DescribeDomainInfoResponse, error)
	DomainRecords(context.Context, *alidns.DescribeDomainRecordsRequest) (*alidns.DescribeDomainRecordsResponse, error)
	Domains(context.Context, *alidns.DescribeDomainsRequest) (*alidns.DescribeDomainsResponse, error)
}

type ReaderOptions struct {
	PageSize   int64
	MaxRecords int64
	MaxPages   int64
	Timeout    time.Duration
}

const (
	defaultPageSize   int64 = 100
	defaultMaxRecords int64 = 10000
	defaultMaxPages   int64 = 100
	defaultTimeout          = 30 * time.Second
	maxAliDNSPageSize int64 = 500
	maxZonePageSize   int64 = 100
	readAttempts            = 3
)

type AliDNSReader struct {
	api     AliDNSAPI
	options ReaderOptions
}
type ZoneSummary struct {
	Name           string `json:"name"`
	ProviderZoneID string `json:"provider_zone_id"`
}

func NewAliDNSReader(api AliDNSAPI, options ReaderOptions) (*AliDNSReader, error) {
	if api == nil {
		return nil, ErrReadFailed
	}
	options, err := normalizeReaderOptions(options)
	if err != nil {
		return nil, err
	}
	return &AliDNSReader{api: api, options: options}, nil
}

func (r *AliDNSReader) ReadZone(ctx context.Context, zone string) (dnsmodel.Snapshot, error) {
	if r == nil || r.api == nil || ctx == nil {
		return dnsmodel.Snapshot{}, ErrReadFailed
	}
	zone, err := dnsmodel.NormalizeZone(zone)
	if err != nil {
		return dnsmodel.Snapshot{}, ErrReadFailed
	}
	ctx, cancel := context.WithTimeout(ctx, r.options.Timeout)
	defer cancel()

	previous, err := r.readZoneOnce(ctx, zone)
	if err != nil {
		return dnsmodel.Snapshot{}, err
	}
	for attempt := 1; attempt < readAttempts; attempt++ {
		current, err := r.readZoneOnce(ctx, zone)
		if err != nil {
			return dnsmodel.Snapshot{}, err
		}
		if current.SnapshotHash == previous.SnapshotHash && current.BusinessIdentityHash == previous.BusinessIdentityHash {
			return current, nil
		}
		previous = current
	}
	return dnsmodel.Snapshot{}, ErrRemoteDrift
}

func (r *AliDNSReader) ListZones(ctx context.Context) ([]ZoneSummary, error) {
	if r == nil || r.api == nil || ctx == nil {
		return nil, ErrReadFailed
	}
	ctx, cancel := context.WithTimeout(ctx, r.options.Timeout)
	defer cancel()

	pageSize := min(r.options.PageSize, maxZonePageSize)
	zones := make([]ZoneSummary, 0)
	seenZoneIDs := make(map[string]struct{})
	seenZoneNames := make(map[string]struct{})
	var totalCount int64 = -1
	for pageNumber := int64(1); ; pageNumber++ {
		if pageNumber > r.options.MaxPages {
			return nil, ErrReadLimit
		}
		response, err := r.api.Domains(ctx, &alidns.DescribeDomainsRequest{PageNumber: &pageNumber, PageSize: &pageSize})
		if err != nil {
			return nil, readError(ctx)
		}
		if response == nil || response.Body == nil || response.Body.PageNumber == nil || response.Body.PageSize == nil || response.Body.TotalCount == nil || *response.Body.PageNumber != pageNumber || *response.Body.PageSize != pageSize {
			return nil, ErrIncomplete
		}
		if *response.Body.TotalCount < 0 || *response.Body.TotalCount > r.options.MaxRecords {
			return nil, ErrReadLimit
		}
		if totalCount == -1 {
			totalCount = *response.Body.TotalCount
		} else if totalCount != *response.Body.TotalCount {
			return nil, ErrIncomplete
		}

		page := []*alidns.DescribeDomainsResponseBodyDomainsDomain(nil)
		if response.Body.Domains != nil {
			page = response.Body.Domains.Domain
		}
		if len(page) > int(pageSize) || (len(page) == 0 && int64(len(zones)) < totalCount) || int64(len(zones)+len(page)) > totalCount {
			return nil, ErrIncomplete
		}
		for _, domain := range page {
			if domain == nil || domain.DomainId == nil || domain.DomainName == nil || *domain.DomainId == "" {
				return nil, ErrIncomplete
			}
			name, err := dnsmodel.NormalizeZone(*domain.DomainName)
			if err != nil {
				return nil, ErrIncomplete
			}
			if _, exists := seenZoneIDs[*domain.DomainId]; exists {
				return nil, ErrIncomplete
			}
			if _, exists := seenZoneNames[name]; exists {
				return nil, ErrIncomplete
			}
			seenZoneIDs[*domain.DomainId] = struct{}{}
			seenZoneNames[name] = struct{}{}
			zones = append(zones, ZoneSummary{Name: name, ProviderZoneID: *domain.DomainId})
		}
		if int64(len(zones)) == totalCount {
			break
		}
	}

	sort.Slice(zones, func(i, j int) bool {
		if zones[i].Name == zones[j].Name {
			return zones[i].ProviderZoneID < zones[j].ProviderZoneID
		}
		return zones[i].Name < zones[j].Name
	})
	return zones, nil
}

func (r *AliDNSReader) readZoneOnce(ctx context.Context, zone string) (dnsmodel.Snapshot, error) {
	zoneInfo, err := r.api.DomainInfo(ctx, &alidns.DescribeDomainInfoRequest{
		DomainName:           &zone,
		NeedDetailAttributes: boolPtr(true),
	})
	if err != nil {
		return dnsmodel.Snapshot{}, readError(ctx)
	}
	if zoneInfo == nil || zoneInfo.Body == nil || zoneInfo.Body.DomainName == nil || zoneInfo.Body.MinTtl == nil || *zoneInfo.Body.MinTtl <= 0 {
		return dnsmodel.Snapshot{}, ErrIncomplete
	}
	infoZone, err := dnsmodel.NormalizeZone(*zoneInfo.Body.DomainName)
	if err != nil || infoZone != zone {
		return dnsmodel.Snapshot{}, ErrIncomplete
	}

	limits := dnsmodel.Limits{MinTTL: *zoneInfo.Body.MinTtl, MaxTTL: 86400, Known: true}
	records, err := r.readRecords(ctx, zone)
	if err != nil {
		return dnsmodel.Snapshot{}, err
	}
	nameServers, err := nameServerRecords(zoneInfo.Body.DnsServers, limits)
	if err != nil {
		return dnsmodel.Snapshot{}, err
	}
	records = append(records, nameServers...)

	snapshot, err := dnsmodel.BuildSnapshot(zone, records, limits)
	if err != nil {
		return dnsmodel.Snapshot{}, ErrIncomplete
	}
	return snapshot, nil
}

func (r *AliDNSReader) readRecords(ctx context.Context, zone string) ([]dnsmodel.Record, error) {
	records := make([]dnsmodel.Record, 0)
	seenIDs := make(map[string]struct{})
	var totalCount int64 = -1
	for pageNumber := int64(1); ; pageNumber++ {
		if pageNumber > r.options.MaxPages {
			return nil, ErrReadLimit
		}
		pageSize := r.options.PageSize
		response, err := r.api.DomainRecords(ctx, &alidns.DescribeDomainRecordsRequest{
			DomainName: &zone,
			PageNumber: &pageNumber,
			PageSize:   &pageSize,
		})
		if err != nil {
			return nil, readError(ctx)
		}
		if response == nil || response.Body == nil || response.Body.PageNumber == nil || response.Body.PageSize == nil || response.Body.TotalCount == nil || *response.Body.PageNumber != pageNumber || *response.Body.PageSize != pageSize {
			return nil, ErrIncomplete
		}
		if *response.Body.TotalCount < 0 || *response.Body.TotalCount > r.options.MaxRecords {
			return nil, ErrReadLimit
		}
		if totalCount == -1 {
			totalCount = *response.Body.TotalCount
		} else if totalCount != *response.Body.TotalCount {
			return nil, ErrIncomplete
		}

		page := []*alidns.DescribeDomainRecordsResponseBodyDomainRecordsRecord(nil)
		if response.Body.DomainRecords != nil {
			page = response.Body.DomainRecords.Record
		}
		if len(page) > int(pageSize) || (len(page) == 0 && int64(len(records)) < totalCount) || int64(len(records)+len(page)) > totalCount {
			return nil, ErrIncomplete
		}
		for _, source := range page {
			record, err := convertRecord(zone, source)
			if err != nil {
				return nil, err
			}
			if _, exists := seenIDs[record.ProviderRecordID]; exists {
				return nil, ErrIncomplete
			}
			seenIDs[record.ProviderRecordID] = struct{}{}
			records = append(records, record)
		}
		if int64(len(records)) == totalCount {
			return records, nil
		}
	}
}

func convertRecord(zone string, source *alidns.DescribeDomainRecordsResponseBodyDomainRecordsRecord) (dnsmodel.Record, error) {
	if source == nil || source.DomainName == nil || source.RecordId == nil || source.RR == nil || source.Type == nil || source.TTL == nil || source.Value == nil || source.Line == nil || source.Status == nil || *source.RecordId == "" {
		return dnsmodel.Record{}, ErrIncomplete
	}
	providerZone, err := dnsmodel.NormalizeZone(*source.DomainName)
	if err != nil || providerZone != zone {
		return dnsmodel.Record{}, ErrIncomplete
	}
	record := dnsmodel.Record{
		ProviderRecordID: *source.RecordId,
		Name:             *source.RR,
		Type:             strings.ToUpper(*source.Type),
		TTL:              *source.TTL,
		Value:            *source.Value,
		Line:             *source.Line,
		Status:           strings.ToUpper(*source.Status),
	}
	if source.Priority != nil && (record.Type == "MX" || record.Type == "SRV") {
		record.Priority = int64Ptr(*source.Priority)
	}
	if source.Weight != nil {
		weight := int64(*source.Weight)
		if record.Type == "SRV" {
			record.Weight = &weight
		} else if weight != 1 {
			setMetadata(&record, "weight", fmt.Sprint(weight))
		}
	}
	if source.Remark != nil && *source.Remark != "" {
		setMetadata(&record, "remark", *source.Remark)
	}
	if source.Locked != nil && *source.Locked {
		setMetadata(&record, "locked", "true")
	}
	if source.LbaStatus != nil && *source.LbaStatus {
		setMetadata(&record, "lba_status", "true")
	}
	return record, nil
}

func nameServerRecords(servers *alidns.DescribeDomainInfoResponseBodyDnsServers, limits dnsmodel.Limits) ([]dnsmodel.Record, error) {
	if servers == nil || len(servers.DnsServer) == 0 {
		return nil, ErrIncomplete
	}
	records := make([]dnsmodel.Record, 0, len(servers.DnsServer))
	for _, server := range servers.DnsServer {
		if server == nil || strings.TrimSpace(*server) == "" {
			return nil, ErrIncomplete
		}
		records = append(records, dnsmodel.Record{
			Name:   "@",
			Type:   "NS",
			TTL:    limits.MinTTL,
			Value:  *server,
			Line:   "default",
			Status: "ENABLE",
		})
	}
	return records, nil
}

func normalizeReaderOptions(options ReaderOptions) (ReaderOptions, error) {
	if options.PageSize == 0 {
		options.PageSize = defaultPageSize
	}
	if options.MaxRecords == 0 {
		options.MaxRecords = defaultMaxRecords
	}
	if options.MaxPages == 0 {
		options.MaxPages = defaultMaxPages
	}
	if options.Timeout == 0 {
		options.Timeout = defaultTimeout
	}
	if options.PageSize < 1 || options.PageSize > maxAliDNSPageSize || options.MaxRecords < 1 || options.MaxPages < 1 || options.Timeout <= 0 {
		return ReaderOptions{}, ErrReadLimit
	}
	return options, nil
}

func readError(ctx context.Context) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return ErrReadFailed
}

func boolPtr(value bool) *bool { return &value }

func int64Ptr(value int64) *int64 { return &value }

func setMetadata(record *dnsmodel.Record, key, value string) {
	if record.Metadata == nil {
		record.Metadata = make(map[string]string)
	}
	record.Metadata[key] = value
}
