package dns

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"ALLinSSL/backend/internal/dnsmodel"
	openapiutil "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	"github.com/alibabacloud-go/tea/dara"
	alidns "github.com/go-acme/alidns-20150109/v4/client"
)

type credentialStore interface {
	List(context.Context) ([]CredentialSummary, error)
	Resolve(context.Context, int64) (Credential, error)
}

type zoneManager interface {
	ListZones(context.Context) ([]ZoneSummary, error)
	ReadZone(context.Context, string) (dnsmodel.Snapshot, error)
	AddRecord(context.Context, string, dnsmodel.Record) error
	UpdateRecord(context.Context, string, string, dnsmodel.Record) error
	DeleteRecord(context.Context, string, string) error
	SetRecordStatus(context.Context, string, string, string) error
	SetRecordLoadBalancing(context.Context, string, string, dnsmodel.Record) error
}

type readerFactory interface {
	New(Credential) (zoneManager, error)
}

type Service struct {
	credentials credentialStore
	readers     readerFactory
}

type RecordInput struct {
	Name, Type, Value, Line string
	TTL                     int64
	Priority, Weight, Port  *int64
	CAAFlags                *int64
	CAATag                  string
	LoadBalancingPolicy     string
	LoadBalancingWeight     *int64
}

func NewService(credentials credentialStore, readers readerFactory) Service {
	return Service{credentials: credentials, readers: readers}
}

func NewSQLService(db *sql.DB) Service {
	return NewService(SQLCredentialStore{DB: db}, aliDNSReaderFactory{})
}

func (s Service) ListCredentials(ctx context.Context) ([]CredentialSummary, error) {
	if s.credentials == nil || ctx == nil {
		return nil, ErrCredential
	}
	return s.credentials.List(ctx)
}

func (s Service) ListZones(ctx context.Context, credentialID int64) ([]ZoneSummary, error) {
	reader, err := s.reader(ctx, credentialID)
	if err != nil {
		return nil, err
	}
	return reader.ListZones(ctx)
}

func (s Service) ReadZone(ctx context.Context, credentialID int64, zone string) (dnsmodel.Snapshot, error) {
	reader, err := s.reader(ctx, credentialID)
	if err != nil {
		return dnsmodel.Snapshot{}, err
	}
	return reader.ReadZone(ctx, zone)
}

func (s Service) CreateRecord(ctx context.Context, credentialID int64, zone string, input RecordInput) (dnsmodel.Snapshot, error) {
	manager, err := s.reader(ctx, credentialID)
	if err != nil {
		return dnsmodel.Snapshot{}, err
	}
	current, err := manager.ReadZone(ctx, zone)
	if err != nil {
		return dnsmodel.Snapshot{}, err
	}
	record, err := normalizeRecordInput(current, input, "ENABLE")
	if err != nil {
		return dnsmodel.Snapshot{}, err
	}
	if err := validateNewRecord(current, record); err != nil {
		return dnsmodel.Snapshot{}, err
	}
	if err := manager.AddRecord(ctx, zone, record); err != nil {
		return dnsmodel.Snapshot{}, err
	}
	updated, err := manager.ReadZone(ctx, zone)
	if err != nil {
		return dnsmodel.Snapshot{}, err
	}
	created, found := findNewRecord(updated, record)
	if !found {
		return dnsmodel.Snapshot{}, ErrWriteUncertain
	}
	created.LoadBalancingPolicy = record.LoadBalancingPolicy
	created.LoadBalancingWeight = record.LoadBalancingWeight
	if err := setRecordLoadBalancing(ctx, manager, zone, created.ProviderRecordID, created); err != nil {
		return dnsmodel.Snapshot{}, err
	}
	return manager.ReadZone(ctx, zone)
}

func (s Service) UpdateRecord(ctx context.Context, credentialID int64, zone, recordID string, input RecordInput) (dnsmodel.Snapshot, error) {
	manager, err := s.reader(ctx, credentialID)
	if err != nil {
		return dnsmodel.Snapshot{}, err
	}
	current, err := manager.ReadZone(ctx, zone)
	if err != nil {
		return dnsmodel.Snapshot{}, err
	}
	old, found := findRecord(current, recordID)
	if !found {
		return dnsmodel.Snapshot{}, ErrRecordNotFound
	}
	if old.Protected {
		return dnsmodel.Snapshot{}, ErrProtectedRecord
	}
	record, err := normalizeRecordInput(current, input, old.Status)
	if err != nil {
		return dnsmodel.Snapshot{}, err
	}
	if err := validateNewRecord(removeRecord(current, recordID), record); err != nil {
		return dnsmodel.Snapshot{}, err
	}
	if err := manager.UpdateRecord(ctx, zone, recordID, record); err != nil {
		return dnsmodel.Snapshot{}, err
	}
	if err := setRecordLoadBalancing(ctx, manager, zone, recordID, record); err != nil {
		return dnsmodel.Snapshot{}, err
	}
	return manager.ReadZone(ctx, zone)
}

func findNewRecord(snapshot dnsmodel.Snapshot, wanted dnsmodel.Record) (dnsmodel.Record, bool) {
	for _, record := range snapshot.Records {
		if !record.Protected && sameRecordContent(record, wanted) {
			return record, true
		}
	}
	return dnsmodel.Record{}, false
}

func setRecordLoadBalancing(ctx context.Context, manager zoneManager, zone, recordID string, record dnsmodel.Record) error {
	if record.Type != "A" && record.Type != "AAAA" {
		return nil
	}
	return manager.SetRecordLoadBalancing(ctx, zone, recordID, record)
}

func (s Service) DeleteRecord(ctx context.Context, credentialID int64, zone, recordID string) (dnsmodel.Snapshot, error) {
	manager, err := s.reader(ctx, credentialID)
	if err != nil {
		return dnsmodel.Snapshot{}, err
	}
	current, err := manager.ReadZone(ctx, zone)
	if err != nil {
		return dnsmodel.Snapshot{}, err
	}
	record, found := findRecord(current, recordID)
	if !found {
		return dnsmodel.Snapshot{}, ErrRecordNotFound
	}
	if record.Protected {
		return dnsmodel.Snapshot{}, ErrProtectedRecord
	}
	if err := manager.DeleteRecord(ctx, zone, recordID); err != nil {
		return dnsmodel.Snapshot{}, err
	}
	return manager.ReadZone(ctx, zone)
}

func (s Service) SetRecordStatus(ctx context.Context, credentialID int64, zone, recordID, status string) (dnsmodel.Snapshot, error) {
	manager, err := s.reader(ctx, credentialID)
	if err != nil {
		return dnsmodel.Snapshot{}, err
	}
	current, err := manager.ReadZone(ctx, zone)
	if err != nil {
		return dnsmodel.Snapshot{}, err
	}
	record, found := findRecord(current, recordID)
	if !found {
		return dnsmodel.Snapshot{}, ErrRecordNotFound
	}
	if record.Protected {
		return dnsmodel.Snapshot{}, ErrProtectedRecord
	}
	if status != "ENABLE" && status != "DISABLE" {
		return dnsmodel.Snapshot{}, ErrInvalidRecord
	}
	if err := manager.SetRecordStatus(ctx, zone, recordID, status); err != nil {
		return dnsmodel.Snapshot{}, err
	}
	return manager.ReadZone(ctx, zone)
}

func (s Service) reader(ctx context.Context, credentialID int64) (zoneManager, error) {
	if s.credentials == nil || s.readers == nil || ctx == nil || credentialID <= 0 {
		return nil, ErrCredential
	}
	credential, err := s.credentials.Resolve(ctx, credentialID)
	if err != nil {
		return nil, ErrCredential
	}
	reader, err := s.readers.New(credential)
	if err != nil {
		return nil, ErrReadFailed
	}
	return reader, nil
}

type aliDNSReaderFactory struct{}

func (aliDNSReaderFactory) New(credential Credential) (zoneManager, error) {
	if !validCredentialValue(credential.accessKeyID) || !validCredentialValue(credential.accessKeySecret) {
		return nil, ErrCredential
	}
	timeout := int(defaultTimeout / time.Millisecond)
	config := &openapiutil.Config{
		AccessKeyId:     &credential.accessKeyID,
		AccessKeySecret: &credential.accessKeySecret,
		RegionId:        stringPtr("cn-hangzhou"),
		ConnectTimeout:  &timeout,
		ReadTimeout:     &timeout,
	}
	client, err := alidns.NewClient(config)
	if err != nil {
		return nil, ErrReadFailed
	}
	return NewAliDNSReader(aliDNSClient{client: client}, ReaderOptions{})
}

type aliDNSClient struct{ client *alidns.Client }

func (c aliDNSClient) DomainInfo(ctx context.Context, request *alidns.DescribeDomainInfoRequest) (*alidns.DescribeDomainInfoResponse, error) {
	if c.client == nil {
		return nil, errors.New("nil AliDNS client")
	}
	return alidns.DescribeDomainInfoWithContext(ctx, c.client, request, aliDNSRuntimeOptions())
}

func (c aliDNSClient) DomainRecords(ctx context.Context, request *alidns.DescribeDomainRecordsRequest) (*alidns.DescribeDomainRecordsResponse, error) {
	if c.client == nil {
		return nil, errors.New("nil AliDNS client")
	}
	return alidns.DescribeDomainRecordsWithContext(ctx, c.client, request, aliDNSRuntimeOptions())
}

func (c aliDNSClient) Domains(ctx context.Context, request *alidns.DescribeDomainsRequest) (*alidns.DescribeDomainsResponse, error) {
	if c.client == nil {
		return nil, errors.New("nil AliDNS client")
	}
	return alidns.DescribeDomainsWithContext(ctx, c.client, request, aliDNSRuntimeOptions())
}

func (c aliDNSClient) AddRecord(ctx context.Context, request *alidns.AddDomainRecordRequest) (*alidns.AddDomainRecordResponse, error) {
	if c.client == nil {
		return nil, errors.New("nil AliDNS client")
	}
	return alidns.AddDomainRecordWithContext(ctx, c.client, request, aliDNSRuntimeOptions())
}

func (c aliDNSClient) UpdateRecord(ctx context.Context, request *alidns.UpdateDomainRecordRequest) (*alidns.UpdateDomainRecordResponse, error) {
	if c.client == nil {
		return nil, errors.New("nil AliDNS client")
	}
	return alidns.UpdateDomainRecordWithContext(ctx, c.client, request, aliDNSRuntimeOptions())
}

func (c aliDNSClient) DeleteRecord(ctx context.Context, request *alidns.DeleteDomainRecordRequest) (*alidns.DeleteDomainRecordResponse, error) {
	if c.client == nil {
		return nil, errors.New("nil AliDNS client")
	}
	return alidns.DeleteDomainRecordWithContext(ctx, c.client, request, aliDNSRuntimeOptions())
}

func (c aliDNSClient) SetRecordStatus(ctx context.Context, request *alidns.SetDomainRecordStatusRequest) (*alidns.SetDomainRecordStatusResponse, error) {
	if c.client == nil {
		return nil, errors.New("nil AliDNS client")
	}
	return alidns.SetDomainRecordStatusWithContext(ctx, c.client, request, aliDNSRuntimeOptions())
}

func (c aliDNSClient) SetDNSSLBStatus(ctx context.Context, request *alidns.SetDNSSLBStatusRequest) (*alidns.SetDNSSLBStatusResponse, error) {
	if c.client == nil {
		return nil, errors.New("nil AliDNS client")
	}
	return alidns.SetDNSSLBStatusWithContext(ctx, c.client, request, aliDNSRuntimeOptions())
}

func (c aliDNSClient) UpdateDNSSLBWeight(ctx context.Context, request *alidns.UpdateDNSSLBWeightRequest) (*alidns.UpdateDNSSLBWeightResponse, error) {
	if c.client == nil {
		return nil, errors.New("nil AliDNS client")
	}
	return alidns.UpdateDNSSLBWeightWithContext(ctx, c.client, request, aliDNSRuntimeOptions())
}

func aliDNSRuntimeOptions() *dara.RuntimeOptions { return &dara.RuntimeOptions{} }

func stringPtr(value string) *string { return &value }
