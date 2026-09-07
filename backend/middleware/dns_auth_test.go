package middleware

import (
	"ALLinSSL/backend/internal/dns"
	"ALLinSSL/backend/public"
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

func TestDNSSessionRequiredRejectsMissingOrStaleApplicationLogin(t *testing.T) {
	for _, test := range []struct {
		name     string
		login    any
		loginKey string
	}{
		{name: "missing login", loginKey: public.LoginKey},
		{name: "stale login key", login: true, loginKey: "stale-login-key"},
	} {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.Use(sessions.Sessions("session", memstore.NewStore([]byte("test-secret"))))
			router.POST("/setup", func(context *gin.Context) {
				session := sessions.Default(context)
				identity, err := dns.RotateSession(session)
				if test.login != nil {
					session.Set("login", test.login)
				}
				session.Set("__login_key", test.loginKey)
				if err != nil || session.Save() != nil {
					context.Status(http.StatusInternalServerError)
					return
				}
				http.SetCookie(context.Writer, dns.NewSessionCookie(identity))
				context.Status(http.StatusNoContent)
			})
			dnsRoutes := router.Group("/v1/dns")
			dnsRoutes.Use(DNSSessionRequired())
			dnsRoutes.POST("/get_credentials", func(context *gin.Context) {
				context.Status(http.StatusNoContent)
			})

			setupResponse := httptest.NewRecorder()
			router.ServeHTTP(setupResponse, httptest.NewRequest(http.MethodPost, "/setup", nil))
			request := httptest.NewRequest(http.MethodPost, "/v1/dns/get_credentials", nil)
			request.AddCookie(testCookie(t, setupResponse, "session"))
			request.AddCookie(testCookie(t, setupResponse, dns.SessionCookieName))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestDNSSessionRequiredRejectsMissingCSRF(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(sessions.Sessions("session", memstore.NewStore([]byte("test-secret"))))
	router.POST("/setup", func(context *gin.Context) {
		session := sessions.Default(context)
		identity, err := dns.RotateSession(session)
		session.Set("login", true)
		session.Set("__login_key", public.LoginKey)
		if err != nil || session.Save() != nil {
			context.Status(http.StatusInternalServerError)
			return
		}
		http.SetCookie(context.Writer, dns.NewSessionCookie(identity))
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
	request.Header.Set("Origin", "http://example.com")
	request.AddCookie(testCookie(t, setupResponse, "session"))
	request.AddCookie(testCookie(t, setupResponse, dns.SessionCookieName))
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
		session := sessions.Default(context)
		identity, err := dns.RotateSession(session)
		session.Set("login", true)
		session.Set("__login_key", public.LoginKey)
		if err != nil || session.Save() != nil {
			context.Status(http.StatusInternalServerError)
			return
		}
		http.SetCookie(context.Writer, dns.NewSessionCookie(identity))
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
	missingSession.Header.Set("Origin", "http://example.com")
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
	applicationCookie := testCookie(t, setupResponse, "session")
	dnsCookie := testCookie(t, setupResponse, dns.SessionCookieName)

	for _, test := range []struct {
		name   string
		token  string
		origin string
		want   int
	}{
		{name: "missing", origin: "http://example.com", want: http.StatusForbidden},
		{name: "wrong", token: "csrf-wrong", origin: "http://example.com", want: http.StatusForbidden},
		{name: "cross origin", token: setupData["csrf"], origin: "https://attacker.example", want: http.StatusForbidden},
		{name: "matching", token: setupData["csrf"], origin: "http://example.com", want: http.StatusNoContent},
	} {
		t.Run(test.name, func(t *testing.T) {
			called = false
			request := httptest.NewRequest(http.MethodPost, "/v1/dns/create_record_preview", strings.NewReader(url.Values{
				"csrf_token": {test.token},
			}.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.Header.Set("Origin", test.origin)
			request.AddCookie(applicationCookie)
			request.AddCookie(dnsCookie)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.want || called != (test.want == http.StatusNoContent) {
				t.Fatalf("status/called = %d/%t, want %d/%t", response.Code, called, test.want, test.want == http.StatusNoContent)
			}
		})
	}
}

func testCookie(t *testing.T, response *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("response has no %q cookie: %v", name, response.Header().Values("Set-Cookie"))
	return nil
}
