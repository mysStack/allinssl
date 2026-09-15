package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"ALLinSSL/backend/internal/dns"
	"ALLinSSL/backend/internal/dnsmodel"
	"github.com/gin-gonic/gin"
)

type fakeDNSService struct {
	credentials []dns.CredentialSummary
	zones       []dns.ZoneSummary
	snapshot    dnsmodel.Snapshot
	err         error
	create      dns.RecordInput
	update      dns.RecordInput
	deleteID    string
	status      string
}

func (s fakeDNSService) ListCredentials(context.Context) ([]dns.CredentialSummary, error) {
	return s.credentials, s.err
}

func (s fakeDNSService) ListZones(context.Context, int64) ([]dns.ZoneSummary, error) {
	return s.zones, s.err
}

func (s fakeDNSService) ReadZone(context.Context, int64, string) (dnsmodel.Snapshot, error) {
	return s.snapshot, s.err
}

func (s *fakeDNSService) CreateRecord(_ context.Context, _ int64, _ string, input dns.RecordInput) (dnsmodel.Snapshot, error) {
	s.create = input
	return s.snapshot, s.err
}

func (s *fakeDNSService) UpdateRecord(_ context.Context, _ int64, _ string, recordID string, input dns.RecordInput) (dnsmodel.Snapshot, error) {
	s.update = input
	s.deleteID = recordID
	return s.snapshot, s.err
}

func (s *fakeDNSService) DeleteRecord(_ context.Context, _ int64, _ string, recordID string) (dnsmodel.Snapshot, error) {
	s.deleteID = recordID
	return s.snapshot, s.err
}

func (s *fakeDNSService) SetRecordStatus(_ context.Context, _ int64, _ string, recordID, status string) (dnsmodel.Snapshot, error) {
	s.deleteID, s.status = recordID, status
	return s.snapshot, s.err
}

func TestDNSHandlerReturnsCredentialSummaries(t *testing.T) {
	handler := NewDNSHandler(&fakeDNSService{credentials: []dns.CredentialSummary{{ID: 1, Name: "AliDNS", Type: "aliyun"}}})
	router := gin.New()
	router.POST("/credentials", handler.GetCredentials)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/credentials", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"AliDNS"`) || strings.Contains(response.Body.String(), "access_key") {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestDNSHandlerReturnsSnapshot(t *testing.T) {
	handler := NewDNSHandler(&fakeDNSService{snapshot: dnsmodel.Snapshot{Zone: "example.com", SnapshotHash: "snapshot"}})
	router := gin.New()
	router.POST("/snapshot", handler.GetSnapshot)

	request := httptest.NewRequest(http.MethodPost, "/snapshot", strings.NewReader(url.Values{"credential_id": {"1"}, "zone": {"example.com"}}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"zone":"example.com"`) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestDNSHandlerCreatesStrictRecordForm(t *testing.T) {
	service := &fakeDNSService{snapshot: dnsmodel.Snapshot{Zone: "example.com"}}
	handler := NewDNSHandler(service)
	router := gin.New()
	router.POST("/create_record", handler.CreateRecord)

	form := url.Values{"credential_id": {"1"}, "zone": {"example.com"}, "name": {"api"}, "type": {"A"}, "ttl": {"600"}, "value": {"192.0.2.20"}, "line": {"default"}, "remark": {"入口服务"}, "load_balancing_policy": {"weight"}, "load_balancing_weight": {"20"}, "csrf_token": {"csrf-a"}}
	response := performDNSFormRequest(router, "/create_record", form)
	if response.Code != http.StatusOK || service.create.Name != "api" || service.create.Line != "default" || service.create.Remark != "入口服务" || service.create.LoadBalancingPolicy != "weight" || service.create.LoadBalancingWeight == nil || *service.create.LoadBalancingWeight != 20 {
		t.Fatalf("response/input = %d/%#v", response.Code, service.create)
	}

	form.Set("unknown", "value")
	response = performDNSFormRequest(router, "/create_record", form)
	if response.Code == http.StatusOK {
		t.Fatalf("unknown field accepted: %s", response.Body.String())
	}
}

func TestDNSHandlerChangesRecordByID(t *testing.T) {
	service := &fakeDNSService{snapshot: dnsmodel.Snapshot{Zone: "example.com"}}
	handler := NewDNSHandler(service)
	router := gin.New()
	router.POST("/update_record", handler.UpdateRecord)
	router.POST("/delete_record", handler.DeleteRecord)
	router.POST("/set_record_status", handler.SetRecordStatus)

	base := url.Values{"credential_id": {"1"}, "zone": {"example.com"}, "record_id": {"record-1"}, "csrf_token": {"csrf-a"}}
	update := cloneDNSForm(base)
	update.Set("name", "api")
	update.Set("type", "MX")
	update.Set("ttl", "600")
	update.Set("value", "mail.example.net")
	update.Set("line", "default")
	update.Set("priority", "10")
	if response := performDNSFormRequest(router, "/update_record", update); response.Code != http.StatusOK || service.deleteID != "record-1" || service.update.Priority == nil || *service.update.Priority != 10 {
		t.Fatalf("update response/input = %d/%#v", response.Code, service.update)
	}
	if response := performDNSFormRequest(router, "/delete_record", base); response.Code != http.StatusOK || service.deleteID != "record-1" {
		t.Fatalf("delete response = %d", response.Code)
	}
	status := cloneDNSForm(base)
	status.Set("status", "DISABLE")
	if response := performDNSFormRequest(router, "/set_record_status", status); response.Code != http.StatusOK || service.status != "DISABLE" {
		t.Fatalf("status response/input = %d/%q", response.Code, service.status)
	}
}

func performDNSFormRequest(router http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func cloneDNSForm(values url.Values) url.Values {
	clone := make(url.Values, len(values))
	for key, items := range values {
		clone[key] = append([]string(nil), items...)
	}
	return clone
}
