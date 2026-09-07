package middleware

import (
	"ALLinSSL/backend/internal/dns"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/memstore"
	"github.com/gin-gonic/gin"
)

func TestDNSRequestPreflightRejectsAPITokenAndOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(DNSRequestPreflight())
	called := false
	router.POST("/v1/dns/bind_zone", func(context *gin.Context) {
		called = true
		context.Status(http.StatusNoContent)
	})

	apiTokenRequest := httptest.NewRequest(http.MethodPost, "/v1/dns/bind_zone", strings.NewReader(url.Values{
		"api_token": {"token"},
		"timestamp": {"1"},
	}.Encode()))
	apiTokenRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	apiTokenResponse := httptest.NewRecorder()
	router.ServeHTTP(apiTokenResponse, apiTokenRequest)
	if apiTokenResponse.Code != http.StatusUnauthorized || called {
		t.Fatalf("API token request status/called = %d/%t", apiTokenResponse.Code, called)
	}

	called = false
	emptyTokenRequest := httptest.NewRequest(http.MethodPost, "/v1/dns/bind_zone", strings.NewReader("api_token=&zone=example.com"))
	emptyTokenRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	emptyTokenResponse := httptest.NewRecorder()
	router.ServeHTTP(emptyTokenResponse, emptyTokenRequest)
	if emptyTokenResponse.Code != http.StatusUnauthorized || called {
		t.Fatalf("empty API token request status/called = %d/%t", emptyTokenResponse.Code, called)
	}

	called = false
	largeRequest := httptest.NewRequest(http.MethodPost, "/v1/dns/bind_zone", strings.NewReader("zone="+strings.Repeat("a", 64*1024)))
	largeRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	largeResponse := httptest.NewRecorder()
	router.ServeHTTP(largeResponse, largeRequest)
	if largeResponse.Code != http.StatusRequestEntityTooLarge || called {
		t.Fatalf("oversized request status/called = %d/%t", largeResponse.Code, called)
	}
}

func TestDNSRequestPreflightAllowsEmptyReadRequestWithoutContentType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(DNSRequestPreflight())
	router.POST("/v1/dns/get_credentials", func(context *gin.Context) {
		context.Status(http.StatusNoContent)
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/dns/get_credentials", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("empty read request status = %d, want %d", response.Code, http.StatusNoContent)
	}
}

func TestDNSSessionRequiredRejectsMissingCSRF(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(sessions.Sessions("session", memstore.NewStore([]byte("test-secret"))))
	router.POST("/setup", func(context *gin.Context) {
		identity, err := dns.RotateSession(sessions.Default(context))
		if err != nil || sessions.Default(context).Save() != nil {
			context.Status(http.StatusInternalServerError)
			return
		}
		context.JSON(http.StatusOK, gin.H{"csrf": identity.CSRFToken})
	})
	dnsRoutes := router.Group("/v1/dns")
	dnsRoutes.Use(DNSSessionRequired())
	called := false
	dnsRoutes.POST("/bind_zone", func(context *gin.Context) {
		called = true
		context.Status(http.StatusNoContent)
	})

	setupResponse := httptest.NewRecorder()
	router.ServeHTTP(setupResponse, httptest.NewRequest(http.MethodPost, "/setup", nil))
	if setupResponse.Code != http.StatusOK {
		t.Fatalf("setup status = %d", setupResponse.Code)
	}
	var setupData map[string]string
	if err := json.Unmarshal(setupResponse.Body.Bytes(), &setupData); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/dns/bind_zone", strings.NewReader("zone=example.com"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(setupResponse.Result().Cookies()[0])
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || called {
		t.Fatalf("missing CSRF status/called = %d/%t", response.Code, called)
	}
}

func TestCreateRecordPreviewRejectsCrossOriginRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(DNSRequestPreflight())
	called := false
	router.POST("/v1/dns/create_record_preview", func(context *gin.Context) {
		called = true
		context.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodPost, "http://allinssl.test/v1/dns/create_record_preview", strings.NewReader(url.Values{
		"csrf_token": {"csrf-a"},
	}.Encode()))
	request.Host = "allinssl.test"
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://attacker.example")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || called {
		t.Fatalf("cross-origin status/called = %d/%t", response.Code, called)
	}
}

func TestCreateRecordPreviewRequiresSessionAndMatchingCSRF(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(sessions.Sessions("session", memstore.NewStore([]byte("test-secret"))))
	router.POST("/setup", func(context *gin.Context) {
		identity, err := dns.RotateSession(sessions.Default(context))
		if err != nil || sessions.Default(context).Save() != nil {
			context.Status(http.StatusInternalServerError)
			return
		}
		context.JSON(http.StatusOK, gin.H{"csrf": identity.CSRFToken})
	})
	dnsRoutes := router.Group("/v1/dns")
	dnsRoutes.Use(DNSSessionRequired())
	called := false
	dnsRoutes.POST("/create_record_preview", func(context *gin.Context) {
		called = true
		context.Status(http.StatusNoContent)
	})

	missingSession := httptest.NewRequest(http.MethodPost, "/v1/dns/create_record_preview", strings.NewReader("csrf_token=csrf-a"))
	missingSession.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	missingSessionResponse := httptest.NewRecorder()
	router.ServeHTTP(missingSessionResponse, missingSession)
	if missingSessionResponse.Code != http.StatusUnauthorized || called {
		t.Fatalf("missing-session status/called = %d/%t", missingSessionResponse.Code, called)
	}

	setupResponse := httptest.NewRecorder()
	router.ServeHTTP(setupResponse, httptest.NewRequest(http.MethodPost, "/setup", nil))
	if setupResponse.Code != http.StatusOK {
		t.Fatalf("setup status = %d", setupResponse.Code)
	}
	var setupData map[string]string
	if err := json.Unmarshal(setupResponse.Body.Bytes(), &setupData); err != nil {
		t.Fatal(err)
	}
	cookie := setupResponse.Result().Cookies()[0]

	for _, test := range []struct {
		name   string
		token  string
		origin string
		want   int
	}{
		{name: "missing", want: http.StatusForbidden},
		{name: "wrong", token: "csrf-wrong", want: http.StatusForbidden},
		{name: "cross origin", token: setupData["csrf"], origin: "https://attacker.example", want: http.StatusForbidden},
		{name: "matching", token: setupData["csrf"], want: http.StatusNoContent},
	} {
		t.Run(test.name, func(t *testing.T) {
			called = false
			request := httptest.NewRequest(http.MethodPost, "/v1/dns/create_record_preview", strings.NewReader(url.Values{
				"csrf_token": {test.token},
			}.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.Header.Set("Origin", test.origin)
			request.AddCookie(cookie)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.want || called != (test.want == http.StatusNoContent) {
				t.Fatalf("status/called = %d/%t, want %d/%t", response.Code, called, test.want, test.want == http.StatusNoContent)
			}
		})
	}
}
