package dns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"ALLinSSL/backend/internal/dnsmodel"
	alidns "github.com/go-acme/alidns-20150109/v4/client"
)

// Fixtures replace only the cloud boundary, not normalization or pagination.
type fakeAPI struct {
	info      func(context.Context, *alidns.DescribeDomainInfoRequest) (*alidns.DescribeDomainInfoResponse, error)
	records   func(context.Context, *alidns.DescribeDomainRecordsRequest) (*alidns.DescribeDomainRecordsResponse, error)
	zones     func(context.Context, *alidns.DescribeDomainsRequest) (*alidns.DescribeDomainsResponse, error)
	add       func(context.Context, *alidns.AddDomainRecordRequest) (*alidns.AddDomainRecordResponse, error)
	update    func(context.Context, *alidns.UpdateDomainRecordRequest) (*alidns.UpdateDomainRecordResponse, error)
	delete    func(context.Context, *alidns.DeleteDomainRecordRequest) (*alidns.DeleteDomainRecordResponse, error)
	status    func(context.Context, *alidns.SetDomainRecordStatusRequest) (*alidns.SetDomainRecordStatusResponse, error)
	lba       func(context.Context, *alidns.SetDNSSLBStatusRequest) (*alidns.SetDNSSLBStatusResponse, error)
	lbaWeight func(context.Context, *alidns.UpdateDNSSLBWeightRequest) (*alidns.UpdateDNSSLBWeightResponse, error)
	remark    func(context.Context, *alidns.UpdateDomainRecordRemarkRequest) (*alidns.UpdateDomainRecordRemarkResponse, error)
}

func (f fakeAPI) DomainInfo(c context.Context, q *alidns.DescribeDomainInfoRequest) (*alidns.DescribeDomainInfoResponse, error) {
	if f.info != nil {
		return f.info(c, q)
	}
	return decode[alidns.DescribeDomainInfoResponse](`{"Body":{"DomainName":"example.com","MinTtl":600,"VersionCode":"version_free","DnsServers":{"DnsServer":["ns1.example.net"]}}}`), nil
}
func (f fakeAPI) DomainRecords(c context.Context, q *alidns.DescribeDomainRecordsRequest) (*alidns.DescribeDomainRecordsResponse, error) {
	return f.records(c, q)
}
func (f fakeAPI) Domains(c context.Context, q *alidns.DescribeDomainsRequest) (*alidns.DescribeDomainsResponse, error) {
	return f.zones(c, q)
}
func (f fakeAPI) AddRecord(c context.Context, q *alidns.AddDomainRecordRequest) (*alidns.AddDomainRecordResponse, error) {
	return f.add(c, q)
}
func (f fakeAPI) UpdateRecord(c context.Context, q *alidns.UpdateDomainRecordRequest) (*alidns.UpdateDomainRecordResponse, error) {
	return f.update(c, q)
}
func (f fakeAPI) DeleteRecord(c context.Context, q *alidns.DeleteDomainRecordRequest) (*alidns.DeleteDomainRecordResponse, error) {
	return f.delete(c, q)
}
func (f fakeAPI) SetRecordStatus(c context.Context, q *alidns.SetDomainRecordStatusRequest) (*alidns.SetDomainRecordStatusResponse, error) {
	return f.status(c, q)
}
func (f fakeAPI) SetDNSSLBStatus(c context.Context, q *alidns.SetDNSSLBStatusRequest) (*alidns.SetDNSSLBStatusResponse, error) {
	if f.lba == nil {
		return &alidns.SetDNSSLBStatusResponse{}, nil
	}
	return f.lba(c, q)
}
func (f fakeAPI) UpdateDNSSLBWeight(c context.Context, q *alidns.UpdateDNSSLBWeightRequest) (*alidns.UpdateDNSSLBWeightResponse, error) {
	if f.lbaWeight == nil {
		return &alidns.UpdateDNSSLBWeightResponse{}, nil
	}
	return f.lbaWeight(c, q)
}
func (f fakeAPI) UpdateRecordRemark(c context.Context, q *alidns.UpdateDomainRecordRemarkRequest) (*alidns.UpdateDomainRecordRemarkResponse, error) {
	if f.remark == nil {
		return &alidns.UpdateDomainRecordRemarkResponse{}, nil
	}
	return f.remark(c, q)
}
func decode[T any](s string) *T {
	var v T
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		panic(err)
	}
	return &v
}
func page(number, total int64, records string) *alidns.DescribeDomainRecordsResponse {
	return decode[alidns.DescribeDomainRecordsResponse](fmt.Sprintf(`{"Body":{"PageNumber":%d,"PageSize":1,"TotalCount":%d,"DomainRecords":{"Record":[%s]}}}`, number, total, records))
}

const businessRecord = `{"DomainName":"example.com","RecordId":"r1","RR":"www","Type":"A","TTL":600,"Value":"192.0.2.10","Line":"default","Status":"ENABLE","Locked":false,"LbaStatus":false,"Weight":1}`

func reader(t *testing.T, api AliDNSAPI) *AliDNSReader {
	t.Helper()
	r, err := NewAliDNSReader(api, ReaderOptions{PageSize: 1, MaxRecords: 10, MaxPages: 10, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestReaderOptionsFailClosed(t *testing.T) {
	if _, err := NewAliDNSReader(nil, ReaderOptions{}); !errors.Is(err, ErrReadFailed) {
		t.Fatalf("nil API accepted: %v", err)
	}
	for _, options := range []ReaderOptions{{PageSize: 501}, {PageSize: 1, MaxRecords: -1}, {PageSize: 1, MaxRecords: 1, MaxPages: -1}, {PageSize: 1, MaxRecords: 1, MaxPages: 1, Timeout: -time.Second}} {
		if _, err := NewAliDNSReader(fakeAPI{}, options); !errors.Is(err, ErrReadLimit) {
			t.Fatalf("unsafe options accepted: %#v err=%v", options, err)
		}
	}
	defaults, err := NewAliDNSReader(fakeAPI{}, ReaderOptions{})
	if err != nil || defaults.options.PageSize != defaultPageSize || defaults.options.MaxRecords != defaultMaxRecords || defaults.options.MaxPages != defaultMaxPages || defaults.options.Timeout != defaultTimeout {
		t.Fatalf("defaults not applied: %#v err=%v", defaults.options, err)
	}
}

func TestReaderFullPaginationKeepsUnsupportedRecords(t *testing.T) {
	calls := 0
	f := fakeAPI{records: func(c context.Context, q *alidns.DescribeDomainRecordsRequest) (*alidns.DescribeDomainRecordsResponse, error) {
		calls++
		if q.Status != nil || q.Line != nil || q.Type != nil || *q.DomainName != "example.com" || *q.PageSize != 1 {
			t.Fatal("filtered or incorrectly scoped query")
		}
		if *q.PageNumber == 1 {
			return page(1, 2, businessRecord), nil
		}
		return page(2, 2, `{"DomainName":"example.com","RecordId":"r2","RR":"legacy","Type":"A","TTL":600,"Value":"192.0.2.20","Line":"telecom","Status":"DISABLE","Remark":"keep me"}`), nil
	}}
	s, err := reader(t, f).ReadZone(context.Background(), "EXAMPLE.COM.")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 4 || len(s.Records) != 3 || s.Compatible || s.WriteEnabled {
		t.Fatalf("partial or unsafe snapshot: calls=%d records=%d compatible=%v", calls, len(s.Records), s.Compatible)
	}
	for _, r := range s.Records {
		if r.Name == "legacy" && (r.Status != "DISABLE" || r.Line != "telecom" || r.Remark != "keep me") {
			t.Fatal("lost provider attributes")
		}
	}
}

func TestReaderStabilityUsesAdjacentBusinessIdentity(t *testing.T) {
	for _, mode := range []string{"stable", "acme", "settles", "value-drift", "identity-drift"} {
		t.Run(mode, func(t *testing.T) {
			pass := 0
			f := fakeAPI{records: func(c context.Context, q *alidns.DescribeDomainRecordsRequest) (*alidns.DescribeDomainRecordsResponse, error) {
				if *q.PageNumber == 1 {
					pass++
					rec := businessRecord
					if mode == "value-drift" || (mode == "settles" && pass == 1) {
						rec = strings.ReplaceAll(rec, "192.0.2.10", fmt.Sprintf("192.0.2.%d", pass))
					}
					if mode == "identity-drift" {
						rec = strings.ReplaceAll(rec, `"r1"`, fmt.Sprintf(`"r%d"`, pass))
					}
					total := int64(1)
					if mode == "acme" {
						total = 2
					}
					return page(1, total, rec), nil
				}
				return page(2, 2, fmt.Sprintf(`{"DomainName":"example.com","RecordId":"acme-%d","RR":"_acme-challenge.a.b","Type":"TXT","TTL":60,"Value":"token-%d","Line":"default","Status":"ENABLE"}`, pass, pass)), nil
			}}
			s, err := reader(t, f).ReadZone(context.Background(), "example.com")
			if strings.HasSuffix(mode, "drift") {
				if !errors.Is(err, ErrRemoteDrift) || len(s.Records) != 0 || pass != 3 {
					t.Fatalf("drift accepted: pass=%d err=%v", pass, err)
				}
				return
			}
			want := 2
			if mode == "settles" {
				want = 3
			}
			if err != nil || !s.Compatible || pass != want {
				t.Fatalf("stable snapshot rejected: pass=%d err=%v reasons=%v", pass, err, s.ReadOnlyReasons)
			}
		})
	}
}

func TestReaderRejectsPartialMalformedAndOversizedResults(t *testing.T) {
	for _, mode := range []string{"nil", "missing-count", "duplicate", "changed-total", "empty-page", "out-of-zone", "missing-field", "provider-error", "limit", "wrong-page"} {
		t.Run(mode, func(t *testing.T) {
			f := fakeAPI{records: func(c context.Context, q *alidns.DescribeDomainRecordsRequest) (*alidns.DescribeDomainRecordsResponse, error) {
				n := *q.PageNumber
				switch mode {
				case "nil":
					return nil, nil
				case "missing-count":
					return decode[alidns.DescribeDomainRecordsResponse](`{"Body":{"DomainRecords":{"Record":[]}}}`), nil
				case "out-of-zone":
					return page(n, 1, strings.ReplaceAll(businessRecord, "example.com", "other.example")), nil
				case "missing-field":
					return page(n, 1, strings.ReplaceAll(businessRecord, `"TTL":600,`, "")), nil
				case "provider-error":
					if n == 2 {
						return nil, errors.New("AccessKeySecret=fixture-secret")
					}
				case "limit":
					return page(n, 999, businessRecord), nil
				case "wrong-page":
					return page(42, 2, businessRecord), nil
				}
				if n == 1 {
					return page(1, 2, businessRecord), nil
				}
				if mode == "empty-page" {
					return page(n, 2, ""), nil
				}
				if mode == "changed-total" {
					return page(n, 3, strings.ReplaceAll(businessRecord, "r1", "r2")), nil
				}
				return page(n, 2, businessRecord), nil
			}}
			s, err := reader(t, f).ReadZone(context.Background(), "example.com")
			if err == nil || len(s.Records) != 0 || strings.Contains(fmt.Sprintf("%+v", err), "fixture-secret") {
				t.Fatalf("partial/unsafe response: err=%v records=%d", err, len(s.Records))
			}
		})
	}
}

func TestReaderCancellationAndTimeout(t *testing.T) {
	f := fakeAPI{info: func(c context.Context, q *alidns.DescribeDomainInfoRequest) (*alidns.DescribeDomainInfoResponse, error) {
		<-c.Done()
		return nil, fmt.Errorf("fixture-secret: %w", c.Err())
	}}
	c, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := reader(t, f).ReadZone(c, "example.com")
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "fixture-secret") {
		t.Fatalf("cancellation not sanitized: %v", err)
	}
	r, _ := NewAliDNSReader(f, ReaderOptions{Timeout: time.Millisecond})
	_, err = r.ReadZone(context.Background(), "example.com")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline not propagated: %v", err)
	}
}

func TestZoneListIsCompleteSortedAndSummaryOnly(t *testing.T) {
	f := fakeAPI{zones: func(c context.Context, q *alidns.DescribeDomainsRequest) (*alidns.DescribeDomainsResponse, error) {
		n := *q.PageNumber
		name := "z.example"
		if n == 2 {
			name = "a.example"
		}
		return decode[alidns.DescribeDomainsResponse](fmt.Sprintf(`{"Body":{"PageNumber":%d,"PageSize":1,"TotalCount":2,"Domains":{"Domain":[{"DomainId":"%d","DomainName":"%s","RegistrantEmail":"private@example.test"}]}}}`, n, n, name)), nil
	}}
	zones, err := reader(t, f).ListZones(context.Background())
	if err != nil || len(zones) != 2 {
		t.Fatalf("incomplete list: %v", err)
	}
	b, _ := json.Marshal(zones)
	if zones[0].Name != "a.example" || strings.Contains(string(b), "private") {
		t.Fatalf("unsorted or overexposed list: %s", b)
	}
}

func TestAliDNSRecordRequestsUseCanonicalProviderFields(t *testing.T) {
	priority, weight, port, flags := int64(10), int64(20), int64(443), int64(0)
	var add *alidns.AddDomainRecordRequest
	var update *alidns.UpdateDomainRecordRequest
	var deleteRequest *alidns.DeleteDomainRecordRequest
	var status *alidns.SetDomainRecordStatusRequest
	api := fakeAPI{
		add: func(_ context.Context, request *alidns.AddDomainRecordRequest) (*alidns.AddDomainRecordResponse, error) {
			add = request
			return &alidns.AddDomainRecordResponse{}, nil
		},
		update: func(_ context.Context, request *alidns.UpdateDomainRecordRequest) (*alidns.UpdateDomainRecordResponse, error) {
			update = request
			return &alidns.UpdateDomainRecordResponse{}, nil
		},
		delete: func(_ context.Context, request *alidns.DeleteDomainRecordRequest) (*alidns.DeleteDomainRecordResponse, error) {
			deleteRequest = request
			return &alidns.DeleteDomainRecordResponse{}, nil
		},
		status: func(_ context.Context, request *alidns.SetDomainRecordStatusRequest) (*alidns.SetDomainRecordStatusResponse, error) {
			status = request
			return &alidns.SetDomainRecordStatusResponse{}, nil
		},
	}
	reader, err := NewAliDNSReader(api, ReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.AddRecord(context.Background(), "example.com", dnsmodel.Record{Name: "_sip._tcp", Type: "SRV", TTL: 600, Value: "service.example.net.", Priority: &priority, Weight: &weight, Port: &port, Line: "default"}); err != nil {
		t.Fatal(err)
	}
	if *add.Priority != 10 || *add.Value != "20 443 service.example.net." {
		t.Fatalf("SRV add = %#v", add)
	}
	if err := reader.UpdateRecord(context.Background(), "example.com", "record-1", dnsmodel.Record{Name: "caa", Type: "CAA", TTL: 600, Value: "letsencrypt.org", CAAFlags: &flags, CAATag: "issue", Line: "telecom"}); err != nil {
		t.Fatal(err)
	}
	if *update.RecordId != "record-1" || *update.Value != `0 issue "letsencrypt.org"` || *update.Line != "telecom" {
		t.Fatalf("CAA update = %#v", update)
	}
	if err := reader.DeleteRecord(context.Background(), "example.com", "record-1"); err != nil {
		t.Fatal(err)
	}
	if err := reader.SetRecordStatus(context.Background(), "example.com", "record-1", "DISABLE"); err != nil {
		t.Fatal(err)
	}
	if *deleteRequest.RecordId != "record-1" || *status.RecordId != "record-1" || *status.Status != "Disable" {
		t.Fatalf("delete/status = %#v/%#v", deleteRequest, status)
	}
}

func TestAliDNSRecordTypeChangeUpdatesTheSameRecord(t *testing.T) {
	var update *alidns.UpdateDomainRecordRequest
	reader, err := NewAliDNSReader(fakeAPI{
		update: func(_ context.Context, request *alidns.UpdateDomainRecordRequest) (*alidns.UpdateDomainRecordResponse, error) {
			update = request
			return &alidns.UpdateDomainRecordResponse{}, nil
		},
	}, ReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.UpdateRecord(context.Background(), "example.com", "record-cname", dnsmodel.Record{Name: "argo", Type: "A", TTL: 600, Value: "45.252.106.38", Line: "default"}); err != nil {
		t.Fatal(err)
	}
	if update == nil || update.RecordId == nil || *update.RecordId != "record-cname" || update.Type == nil || *update.Type != "A" || update.Value == nil || *update.Value != "45.252.106.38" || update.Line == nil || *update.Line != "default" {
		t.Fatalf("type change did not update the original record: %#v", update)
	}
}

func TestAliDNSLoadBalancingUsesRecordSetPolicyAndRecordWeight(t *testing.T) {
	weight := int64(20)
	var status *alidns.SetDNSSLBStatusRequest
	var updateWeight *alidns.UpdateDNSSLBWeightRequest
	reader, err := NewAliDNSReader(fakeAPI{
		lba: func(_ context.Context, request *alidns.SetDNSSLBStatusRequest) (*alidns.SetDNSSLBStatusResponse, error) {
			status = request
			return &alidns.SetDNSSLBStatusResponse{}, nil
		},
		lbaWeight: func(_ context.Context, request *alidns.UpdateDNSSLBWeightRequest) (*alidns.UpdateDNSSLBWeightResponse, error) {
			updateWeight = request
			return &alidns.UpdateDNSSLBWeightResponse{}, nil
		},
	}, ReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	record := dnsmodel.Record{Name: "api", Type: "A", TTL: 600, Value: "192.0.2.10", Line: "default", LoadBalancingPolicy: "weight", LoadBalancingWeight: &weight}
	if err := reader.SetRecordLoadBalancing(context.Background(), "example.com", "record-1", record); err != nil {
		t.Fatal(err)
	}
	if status == nil || status.DomainName == nil || *status.DomainName != "example.com" || status.SubDomain == nil || *status.SubDomain != "api.example.com" || status.Type == nil || *status.Type != "A" || status.Line == nil || *status.Line != "default" || status.Open == nil || !*status.Open {
		t.Fatalf("status request = %#v", status)
	}
	if updateWeight == nil || updateWeight.RecordId == nil || *updateWeight.RecordId != "record-1" || updateWeight.Weight == nil || *updateWeight.Weight != 20 {
		t.Fatalf("weight request = %#v", updateWeight)
	}
}

func TestReaderReturnsAliDNSLoadBalancingState(t *testing.T) {
	f := fakeAPI{records: func(_ context.Context, request *alidns.DescribeDomainRecordsRequest) (*alidns.DescribeDomainRecordsResponse, error) {
		return page(*request.PageNumber, 1, strings.ReplaceAll(strings.ReplaceAll(businessRecord, `"LbaStatus":false`, `"LbaStatus":true`), `"Weight":1`, `"Weight":20`)), nil
	}}
	snapshot, err := reader(t, f).ReadZone(context.Background(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	record, found := findRecord(snapshot, "r1")
	if !found || !snapshot.Compatible || record.LoadBalancingPolicy != "weight" || record.LoadBalancingWeight == nil || *record.LoadBalancingWeight != 20 {
		t.Fatalf("load-balancing snapshot = %#v", snapshot)
	}
}

func TestReaderDisplaysCNAMELoadBalancingStateWithoutMetadataFallback(t *testing.T) {
	f := fakeAPI{records: func(_ context.Context, request *alidns.DescribeDomainRecordsRequest) (*alidns.DescribeDomainRecordsResponse, error) {
		record := strings.ReplaceAll(businessRecord, `"Type":"A"`, `"Type":"CNAME"`)
		record = strings.ReplaceAll(record, `"Value":"192.0.2.10"`, `"Value":"target.example.net."`)
		record = strings.ReplaceAll(record, `"LbaStatus":false`, `"LbaStatus":true`)
		record = strings.ReplaceAll(record, `"Weight":1`, `"Weight":20`)
		return page(*request.PageNumber, 1, record), nil
	}}

	snapshot, err := reader(t, f).ReadZone(context.Background(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Compatible || snapshot.Records[1].LoadBalancingPolicy != "weight" || snapshot.Records[1].LoadBalancingWeight == nil || *snapshot.Records[1].LoadBalancingWeight != 20 {
		t.Fatalf("CNAME DNSLB state = %#v", snapshot)
	}
}

func TestReaderKeepsRemarkAsManagedRecordData(t *testing.T) {
	f := fakeAPI{records: func(_ context.Context, request *alidns.DescribeDomainRecordsRequest) (*alidns.DescribeDomainRecordsResponse, error) {
		record := strings.Replace(businessRecord, `"Weight":1`, `"Weight":1,"Remark":"入口服务"`, 1)
		return page(*request.PageNumber, 1, record), nil
	}}

	snapshot, err := reader(t, f).ReadZone(context.Background(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Compatible || len(snapshot.Records) != 2 || snapshot.Records[1].Remark != "入口服务" {
		t.Fatalf("remark was not retained as managed data: %#v", snapshot)
	}
}
