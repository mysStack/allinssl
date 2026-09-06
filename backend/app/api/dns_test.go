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
