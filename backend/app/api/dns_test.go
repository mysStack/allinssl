package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"ALLinSSL/backend/internal/dns"
	"ALLinSSL/backend/internal/dnscontrol"
	"ALLinSSL/backend/internal/dnsmodel"
	"ALLinSSL/backend/middleware"
	"github.com/gin-gonic/gin"
)

type fakeDNSService struct {
	credentials []dns.CredentialSummary
	zones       []dns.ZoneSummary
	snapshot    dnsmodel.Snapshot
	err         error
}

type fakeDNSAdoptService struct {
	job               dns.AdoptJob
	startErr          error
	createErr         error
	createCalls       int
	createRecordInput dns.CreateRecordPreviewInput
}

func (service fakeDNSAdoptService) Health(context.Context) (dnscontrol.EngineInfo, error) {
	return dnscontrol.EngineInfo{Version: dnscontrol.ExpectedVersion}, nil
}

func (service fakeDNSAdoptService) Start(context.Context, dns.AdoptStartInput) (dns.AdoptJob, error) {
	return service.job, service.startErr
}

func (service *fakeDNSAdoptService) StartCreateRecordPreview(_ context.Context, input dns.CreateRecordPreviewInput) (dns.AdoptJob, error) {
	service.createCalls++
	service.createRecordInput = input
	return service.job, service.createErr
}

func (service fakeDNSAdoptService) GetJob(context.Context, string, dns.SessionIdentity) (dns.AdoptJob, error) {
	return service.job, service.startErr
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

func TestDNSHandlerReturnsCredentialSummaries(t *testing.T) {
	handler := NewDNSHandler(fakeDNSService{credentials: []dns.CredentialSummary{{ID: 1, Name: "AliDNS", Type: "aliyun"}}})
	router := gin.New()
	router.POST("/credentials", handler.GetCredentials)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/credentials", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"name":"AliDNS"`) || strings.Contains(response.Body.String(), "access_key") {
		t.Fatalf("unexpected response: %s", response.Body.String())
	}
}

func TestDNSHandlerRejectsMissingReadParameters(t *testing.T) {
	handler := NewDNSHandler(fakeDNSService{})
	router := gin.New()
	router.POST("/zones", handler.GetZones)
	router.POST("/snapshot", handler.GetSnapshot)

	for _, path := range []string{"/zones", "/snapshot"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
		if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "credential_id") {
			t.Fatalf("%s response = %d %s", path, response.Code, response.Body.String())
		}
	}

	response := httptest.NewRecorder()
	body := url.Values{"credential_id": {"1"}}.Encode()
	request := httptest.NewRequest(http.MethodPost, "/snapshot", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	router.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "zone") {
		t.Fatalf("snapshot response = %d %s", response.Code, response.Body.String())
	}
}

func TestDNSHandlerReturnsSnapshot(t *testing.T) {
	handler := NewDNSHandler(fakeDNSService{snapshot: dnsmodel.Snapshot{Zone: "example.com", SnapshotHash: "snapshot"}})
	router := gin.New()
	router.POST("/snapshot", handler.GetSnapshot)

	body := url.Values{"credential_id": {"1"}, "zone": {"example.com"}}.Encode()
	request := httptest.NewRequest(http.MethodPost, "/snapshot", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"zone":"example.com"`) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestBindZoneReturnsQueuedJobWithoutSensitiveFields(t *testing.T) {
	handler := NewDNSHandler(
		fakeDNSService{},
		&fakeDNSAdoptService{job: dns.AdoptJob{ID: "job-1", State: dns.JobQueued}},
	)
	router := gin.New()
	router.Use(func(context *gin.Context) {
		context.Set(middleware.DNSIdentityContextKey, dns.SessionIdentity{
			ActorID: "local-admin", SessionID: "session-a", AuthEpoch: "epoch-a", CSRFToken: "csrf-a",
		})
	})
	router.POST("/bind_zone", handler.BindZone)

	request := httptest.NewRequest(http.MethodPost, "/bind_zone", strings.NewReader(url.Values{
		"credential_id":   {"1"},
		"zone":            {"example.com"},
		"snapshot_hash":   {"hash-a"},
		"adopt_all":       {"true"},
		"idempotency_key": {"key-a"},
		"csrf_token":      {"csrf-a"},
	}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"job_id":"job-1"`) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	for _, forbidden := range []string{"credentials", "config", "report", "path", "secret"} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("response exposes %q: %s", forbidden, response.Body.String())
		}
	}
}

func TestCreateRecordPreviewAcceptsStrictTypedForms(t *testing.T) {
	priority, weight, port, caaFlags := int64(10), int64(20), int64(443), int64(0)
	tests := []struct {
		name       string
		recordName string
		recordType string
		value      string
		extra      url.Values
		wantRecord dns.CreateRecordInput
	}{
		{name: "A", recordName: "api", recordType: "A", value: "192.0.2.10", wantRecord: dns.CreateRecordInput{Name: "api", Type: "A", TTL: 600, Value: "192.0.2.10"}},
		{name: "AAAA", recordName: "api", recordType: "AAAA", value: "2001:db8::10", wantRecord: dns.CreateRecordInput{Name: "api", Type: "AAAA", TTL: 600, Value: "2001:db8::10"}},
		{name: "CNAME", recordName: "api", recordType: "CNAME", value: "target.example.net", wantRecord: dns.CreateRecordInput{Name: "api", Type: "CNAME", TTL: 600, Value: "target.example.net"}},
		{name: "TXT", recordName: "api", recordType: "TXT", value: "v=spf1 -all", wantRecord: dns.CreateRecordInput{Name: "api", Type: "TXT", TTL: 600, Value: "v=spf1 -all"}},
		{name: "MX", recordName: "api", recordType: "MX", value: "mail.example.net", extra: url.Values{"priority": {"10"}}, wantRecord: dns.CreateRecordInput{Name: "api", Type: "MX", TTL: 600, Value: "mail.example.net", Priority: &priority}},
		{name: "SRV", recordName: "_sip._tcp", recordType: "SRV", value: "service.example.net", extra: url.Values{"priority": {"10"}, "weight": {"20"}, "port": {"443"}}, wantRecord: dns.CreateRecordInput{Name: "_sip._tcp", Type: "SRV", TTL: 600, Value: "service.example.net", Priority: &priority, Weight: &weight, Port: &port}},
		{name: "CAA", recordName: "api", recordType: "CAA", value: "letsencrypt.org", extra: url.Values{"caa_flags": {"0"}, "caa_tag": {"issue"}}, wantRecord: dns.CreateRecordInput{Name: "api", Type: "CAA", TTL: 600, Value: "letsencrypt.org", CAAFlags: &caaFlags, CAATag: "issue"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			identity := dns.SessionIdentity{ActorID: "local-admin", SessionID: "session-a", AuthEpoch: "epoch-a", CSRFToken: "csrf-a"}
			service := &fakeDNSAdoptService{job: dns.AdoptJob{
				ID: "job-1", State: dns.JobQueued, ActorID: "secret-actor", AuthEpoch: "secret-epoch",
			}}
			router := createRecordPreviewRouter(service, identity)
			form := validCreateRecordPreviewForm(test.recordType)
			form.Set("name", test.recordName)
			form.Set("value", test.value)
			for key, values := range test.extra {
				form[key] = values
			}

			response := performDNSFormRequest(router, "/create_record_preview", form)
			if response.Code != http.StatusOK {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
			var body struct {
				Data map[string]any `json:"data"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if len(body.Data) != 2 || body.Data["job_id"] != "job-1" || body.Data["state"] != string(dns.JobQueued) {
				t.Fatalf("response data = %#v", body.Data)
			}
			if service.createCalls != 1 {
				t.Fatalf("create calls = %d, want 1", service.createCalls)
			}
			if service.createRecordInput.Identity != identity || service.createRecordInput.CredentialID != 1 || service.createRecordInput.Zone != "example.com" ||
				service.createRecordInput.BaseSnapshotHash != "hash-a" || service.createRecordInput.IdempotencyKey != "key-a" ||
				!reflect.DeepEqual(service.createRecordInput.Record, test.wantRecord) {
				t.Fatalf("create input = %#v", service.createRecordInput)
			}
			assertResponseOmitsSensitiveDNSData(t, response.Body.String())
		})
	}
}

func TestCreateRecordPreviewRejectsBlankRecordValue(t *testing.T) {
	service := &fakeDNSAdoptService{job: dns.AdoptJob{ID: "job-1", State: dns.JobQueued}}
	form := validCreateRecordPreviewForm("TXT")
	form.Set("value", "")
	response := performDNSFormRequest(createRecordPreviewRouter(service, testDNSIdentity()), "/create_record_preview", form)
	if response.Code == http.StatusOK || service.createCalls != 0 {
		t.Fatalf("blank value accepted: status/calls = %d/%d", response.Code, service.createCalls)
	}
}

func TestCreateRecordPreviewRejectsUnknownAndDuplicateFields(t *testing.T) {
	forbiddenFields := []string{
		"line", "status", "metadata", "config", "credentials", "credential", "path", "token", "dsl", "args", "ignore",
	}
	for _, field := range forbiddenFields {
		t.Run(field, func(t *testing.T) {
			service := &fakeDNSAdoptService{job: dns.AdoptJob{ID: "job-1", State: dns.JobQueued}}
			form := validCreateRecordPreviewForm("A")
			form.Set(field, "attacker-controlled")
			response := performDNSFormRequest(createRecordPreviewRouter(service, testDNSIdentity()), "/create_record_preview", form)
			if response.Code == http.StatusOK || service.createCalls != 0 {
				t.Fatalf("field %q accepted: status/calls = %d/%d", field, response.Code, service.createCalls)
			}
		})
	}

	service := &fakeDNSAdoptService{job: dns.AdoptJob{ID: "job-1", State: dns.JobQueued}}
	form := validCreateRecordPreviewForm("A")
	form["name"] = []string{"api", "other"}
	response := performDNSFormRequest(createRecordPreviewRouter(service, testDNSIdentity()), "/create_record_preview", form)
	if response.Code == http.StatusOK || service.createCalls != 0 {
		t.Fatalf("duplicate field accepted: status/calls = %d/%d", response.Code, service.createCalls)
	}

	service = &fakeDNSAdoptService{job: dns.AdoptJob{ID: "job-1", State: dns.JobQueued}}
	response = performDNSFormRequest(createRecordPreviewRouter(service, testDNSIdentity()), "/create_record_preview?config=attacker-controlled", validCreateRecordPreviewForm("A"))
	if response.Code == http.StatusOK || service.createCalls != 0 {
		t.Fatalf("query field accepted: status/calls = %d/%d", response.Code, service.createCalls)
	}
}

func TestCreateRecordPreviewRequiresFieldsForEachRecordType(t *testing.T) {
	tests := []struct {
		name       string
		recordType string
		fields     url.Values
	}{
		{name: "unsupported type", recordType: "NS"},
		{name: "MX missing priority", recordType: "MX"},
		{name: "MX non decimal priority", recordType: "MX", fields: url.Values{"priority": {"+10"}}},
		{name: "A rejects priority", recordType: "A", fields: url.Values{"priority": {"10"}}},
		{name: "SRV missing weight and port", recordType: "SRV", fields: url.Values{"priority": {"10"}}},
		{name: "SRV port overflow", recordType: "SRV", fields: url.Values{"priority": {"10"}, "weight": {"20"}, "port": {"65536"}}},
		{name: "CAA missing tag", recordType: "CAA", fields: url.Values{"caa_flags": {"0"}}},
		{name: "CAA flags overflow", recordType: "CAA", fields: url.Values{"caa_flags": {"256"}, "caa_tag": {"issue"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeDNSAdoptService{job: dns.AdoptJob{ID: "job-1", State: dns.JobQueued}}
			form := validCreateRecordPreviewForm(test.recordType)
			for key, values := range test.fields {
				form[key] = values
			}
			response := performDNSFormRequest(createRecordPreviewRouter(service, testDNSIdentity()), "/create_record_preview", form)
			if response.Code == http.StatusOK || service.createCalls != 0 {
				t.Fatalf("invalid %s form accepted: status/calls = %d/%d", test.recordType, response.Code, service.createCalls)
			}
		})
	}
}

func TestGetJobReturnsOnlySafeCreateRecordPreviewSummary(t *testing.T) {
	service := &fakeDNSAdoptService{job: dns.AdoptJob{
		ID: "job-1", Kind: dns.JobKindCreateRecordPreview, Zone: "example.com", CredentialID: 1,
		ActorID: "secret-actor", AuthEpoch: "secret-epoch", State: dns.JobPreviewed,
		SnapshotHash: "hash-a", PlanHash: "plan-a",
		CandidateRecord: dns.CandidateRecordSummary{Name: "api", Type: "TXT", TTL: 600},
		ChangeSummary:   dns.ChangeSummary{Corrections: 1, Details: []string{"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
	}}
	handler := NewDNSHandler(fakeDNSService{}, service)
	router := gin.New()
	router.Use(func(context *gin.Context) { context.Set(middleware.DNSIdentityContextKey, testDNSIdentity()) })
	router.POST("/get_job", handler.GetJob)

	response := performDNSFormRequest(router, "/get_job", url.Values{"job_id": {"job-1"}})
	if response.Code != http.StatusOK {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	for _, expected := range []string{`"kind":"create_record_preview"`, `"candidate_record":{"name":"api","type":"TXT","ttl":600}`, `"change_summary":{"corrections":1`} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Fatalf("response missing %s: %s", expected, response.Body.String())
		}
	}
	assertResponseOmitsSensitiveDNSData(t, response.Body.String())
}

func TestGetJobOmitsEmptyAdoptOnlySummaries(t *testing.T) {
	service := &fakeDNSAdoptService{job: dns.AdoptJob{ID: "job-1", Kind: dns.JobKindAdopt, State: dns.JobAdopted}}
	response := performGetJobRequest(service)
	if response.Code != http.StatusOK {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	for _, field := range []string{"candidate_record", "change_summary"} {
		if strings.Contains(response.Body.String(), field) {
			t.Fatalf("empty %s exposed: %s", field, response.Body.String())
		}
	}
}

func TestGetJobOmitsUnsafeStoredChangeSummary(t *testing.T) {
	service := &fakeDNSAdoptService{job: dns.AdoptJob{
		ID: "job-1", Kind: dns.JobKindCreateRecordPreview, State: dns.JobPreviewed,
		CandidateRecord: dns.CandidateRecordSummary{Name: "api", Type: "TXT", TTL: 600},
		ChangeSummary:   dns.ChangeSummary{Corrections: 1, Details: []string{"secret provider detail"}},
	}}
	response := performGetJobRequest(service)
	if response.Code != http.StatusOK {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	assertResponseOmitsSensitiveDNSData(t, response.Body.String())
}

func performGetJobRequest(service *fakeDNSAdoptService) *httptest.ResponseRecorder {
	handler := NewDNSHandler(fakeDNSService{}, service)
	router := gin.New()
	router.Use(func(context *gin.Context) { context.Set(middleware.DNSIdentityContextKey, testDNSIdentity()) })
	router.POST("/get_job", handler.GetJob)
	return performDNSFormRequest(router, "/get_job", url.Values{"job_id": {"job-1"}})
}

func createRecordPreviewRouter(service *fakeDNSAdoptService, identity dns.SessionIdentity) *gin.Engine {
	gin.SetMode(gin.TestMode)
	handler := NewDNSHandler(fakeDNSService{}, service)
	router := gin.New()
	router.Use(func(context *gin.Context) { context.Set(middleware.DNSIdentityContextKey, identity) })
	router.POST("/create_record_preview", handler.CreateRecordPreview)
	return router
}

func validCreateRecordPreviewForm(recordType string) url.Values {
	return url.Values{
		"credential_id": {"1"}, "zone": {"example.com"}, "base_snapshot_hash": {"hash-a"},
		"name": {"api"}, "type": {recordType}, "ttl": {"600"}, "value": {"192.0.2.10"},
		"idempotency_key": {"key-a"}, "csrf_token": {"csrf-a"},
	}
}

func testDNSIdentity() dns.SessionIdentity {
	return dns.SessionIdentity{ActorID: "local-admin", SessionID: "session-a", AuthEpoch: "epoch-a", CSRFToken: "csrf-a"}
}

func performDNSFormRequest(router http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func assertResponseOmitsSensitiveDNSData(t *testing.T, response string) {
	t.Helper()
	for _, forbidden := range []string{
		"secret", "config", "credentials", "path", "token", "dnsconfig", "--config", "--creds", "--report", "IGNORE(",
	} {
		if strings.Contains(strings.ToLower(response), strings.ToLower(forbidden)) {
			t.Fatalf("response exposes %q: %s", forbidden, response)
		}
	}
}
