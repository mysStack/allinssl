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

type zoneReader interface {
	ListZones(context.Context) ([]ZoneSummary, error)
	ReadZone(context.Context, string) (dnsmodel.Snapshot, error)
}

type readerFactory interface {
	New(Credential) (zoneReader, error)
}

type Service struct {
	credentials credentialStore
	readers     readerFactory
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

func (s Service) reader(ctx context.Context, credentialID int64) (zoneReader, error) {
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

func (aliDNSReaderFactory) New(credential Credential) (zoneReader, error) {
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

func aliDNSRuntimeOptions() *dara.RuntimeOptions { return &dara.RuntimeOptions{} }

func stringPtr(value string) *string { return &value }
