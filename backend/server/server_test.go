package server

import (
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ALLinSSL/backend/internal/dns"
	"ALLinSSL/backend/public"
	_ "modernc.org/sqlite"
)

func TestRegisteredServerRejectsUnsafeDNSMutationsBeforeAuthentication(t *testing.T) {
	router := newTestRegisteredRouter(t)
	for _, path := range []string{"/v1/dns/bind_zone", "/v1/dns/create_record_preview"} {
		t.Run(path+"/cross_origin", func(t *testing.T) {
			request := newDNSRequest(path, "csrf_token=token")
			request.Header.Set("Origin", "https://attacker.example")
			assertServerStatus(t, router, request, http.StatusForbidden)
		})
		t.Run(path+"/content_type", func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "https://allinssl.test"+path, strings.NewReader(`{"csrf_token":"token"}`))
			request.Header.Set("Content-Type", "application/json")
			assertServerStatus(t, router, request, http.StatusUnsupportedMediaType)
		})
		t.Run(path+"/oversized", func(t *testing.T) {
			request := newDNSRequest(path, "value="+strings.Repeat("a", 64*1024))
			assertServerStatus(t, router, request, http.StatusRequestEntityTooLarge)
		})
		t.Run(path+"/api_token", func(t *testing.T) {
			request := newDNSRequest(path+"?api_token=token&timestamp=1", "csrf_token=token")
			assertServerStatus(t, router, request, http.StatusUnauthorized)
		})
		t.Run(path+"/form_api_token", func(t *testing.T) {
			request := newDNSRequest(path, "api_token=token&timestamp=1")
			assertServerStatus(t, router, request, http.StatusUnauthorized)
		})
	}
}

func TestRegisteredLoginRotatesDNSBindingAndRealRoutesRequireSessionAndCSRF(t *testing.T) {
	router := newTestRegisteredRouter(t)

	preLogin := serveRequest(router, httptest.NewRequest(http.MethodGet, "https://allinssl.test/v1/login/get_code", nil))
	applicationCookie := namedCookie(t, preLogin, public.SessionKey)

	login := loginRequest(applicationCookie)
	fixedDNSCookie := &http.Cookie{Name: dns.SessionCookieName, Value: "attacker-fixed-binding", Path: "/v1/dns"}
	login.AddCookie(fixedDNSCookie)
	loginResponse := serveRequest(router, login)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("login status/body = %d/%s", loginResponse.Code, loginResponse.Body.String())
	}
	applicationCookie = namedCookie(t, loginResponse, public.SessionKey)
	firstDNSCookie := namedCookie(t, loginResponse, dns.SessionCookieName)
	assertSecureDNSCookie(t, firstDNSCookie)
	if firstDNSCookie.Value == fixedDNSCookie.Value {
		t.Fatal("successful login accepted a fixed DNS session binding")
	}

	fixedRequest := newDNSRequest("/v1/dns/get_credentials", "")
	fixedRequest.AddCookie(applicationCookie)
	fixedRequest.AddCookie(fixedDNSCookie)
	assertServerStatus(t, router, fixedRequest, http.StatusUnauthorized)

	authorizedRequest := newDNSRequest("/v1/dns/get_credentials", "")
	authorizedRequest.AddCookie(applicationCookie)
	authorizedRequest.AddCookie(firstDNSCookie)
	assertServerStatus(t, router, authorizedRequest, http.StatusOK)
	csrfToken := registeredServerCSRF(t, router, applicationCookie, firstDNSCookie)

	reloginResponse := serveRequest(router, loginRequest(applicationCookie))
	if reloginResponse.Code != http.StatusOK {
		t.Fatalf("relogin status/body = %d/%s", reloginResponse.Code, reloginResponse.Body.String())
	}
	applicationCookie = namedCookie(t, reloginResponse, public.SessionKey)
	secondDNSCookie := namedCookie(t, reloginResponse, dns.SessionCookieName)
	if firstDNSCookie.Value == secondDNSCookie.Value {
		t.Fatal("successful login reused the DNS session binding")
	}
	csrfToken = registeredServerCSRF(t, router, applicationCookie, secondDNSCookie)

	staleRequest := newDNSRequest("/v1/dns/get_credentials", "")
	staleRequest.AddCookie(applicationCookie)
	staleRequest.AddCookie(firstDNSCookie)
	assertServerStatus(t, router, staleRequest, http.StatusUnauthorized)

	for _, path := range []string{"/v1/dns/bind_zone", "/v1/dns/create_record_preview"} {
		t.Run(path+"/missing_csrf", func(t *testing.T) {
			request := newDNSRequest(path, "zone=example.com")
			request.AddCookie(applicationCookie)
			request.AddCookie(secondDNSCookie)
			assertServerStatus(t, router, request, http.StatusForbidden)
		})
		t.Run(path+"/wrong_csrf", func(t *testing.T) {
			request := newDNSRequest(path, "csrf_token=wrong")
			request.AddCookie(applicationCookie)
			request.AddCookie(secondDNSCookie)
			assertServerStatus(t, router, request, http.StatusForbidden)
		})
		t.Run(path+"/missing_origin", func(t *testing.T) {
			request := newDNSRequest(path, "csrf_token="+url.QueryEscape(csrfToken))
			request.Header.Del("Origin")
			request.AddCookie(applicationCookie)
			request.AddCookie(secondDNSCookie)
			assertServerStatus(t, router, request, http.StatusForbidden)
		})
	}

	logoutRequest := httptest.NewRequest(http.MethodPost, "https://allinssl.test/v1/login/sign-out", nil)
	logoutRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	logoutRequest.AddCookie(applicationCookie)
	logoutResponse := serveRequest(router, logoutRequest)
	expired := namedCookie(t, logoutResponse, dns.SessionCookieName)
	if expired.MaxAge >= 0 || expired.Path != "/v1/dns" || !expired.Secure || !expired.HttpOnly || expired.SameSite != http.SameSiteStrictMode {
		t.Fatalf("logout cookie = %#v", expired)
	}
}

func registeredServerCSRF(t *testing.T, router http.Handler, applicationCookie, dnsCookie *http.Cookie) string {
	t.Helper()
	request := newDNSRequest("/v1/dns/get_health", "")
	request.AddCookie(applicationCookie)
	request.AddCookie(dnsCookie)
	response := serveRequest(router, request)
	if response.Code != http.StatusOK {
		t.Fatalf("get_health status/body = %d/%s", response.Code, response.Body.String())
	}
	var body struct {
		Data struct {
			CSRFToken string `json:"csrf_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Data.CSRFToken == "" {
		t.Fatalf("get_health response = %s, error = %v", response.Body.String(), err)
	}
	return body.Data.CSRFToken
}

func newTestRegisteredRouter(t *testing.T) http.Handler {
	t.Helper()
	originalDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	temporaryDirectory := t.TempDir()
	if err := os.Mkdir(filepath.Join(temporaryDirectory, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(temporaryDirectory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	createServerTestDatabases(t)
	public.Secure = ""
	public.TimeOut = 3600
	public.LoginKey = "server-test-login-key"
	public.SessionKey = "server-test-session"
	return newRouter()
}

func createServerTestDatabases(t *testing.T) {
	t.Helper()
	settings, err := sql.Open("sqlite", "data/settings.db")
	if err != nil {
		t.Fatal(err)
	}
	password := md5.Sum([]byte("password" + "test-salt"))
	if _, err := settings.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, username TEXT, password TEXT, salt TEXT);
		INSERT INTO users (id, username, password, salt) VALUES (1, 'admin', ?, 'test-salt')`, hex.EncodeToString(password[:])); err != nil {
		settings.Close()
		t.Fatal(err)
	}
	if err := settings.Close(); err != nil {
		t.Fatal(err)
	}

	database, err := sql.Open("sqlite", "data/data.db")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE access (id INTEGER PRIMARY KEY, config TEXT NOT NULL, type TEXT NOT NULL, name TEXT NOT NULL)`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
}

func loginRequest(applicationCookie *http.Cookie) *http.Request {
	form := url.Values{"username": {"admin"}, "password": {"password"}}
	request := httptest.NewRequest(http.MethodPost, "https://allinssl.test/v1/login/sign", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(applicationCookie)
	return request
}

func newDNSRequest(path, body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "https://allinssl.test"+path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://allinssl.test")
	return request
}

func serveRequest(handler http.Handler, request *http.Request) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertServerStatus(t *testing.T, handler http.Handler, request *http.Request, expected int) {
	t.Helper()
	response := serveRequest(handler, request)
	if response.Code != expected {
		t.Fatalf("status/body = %d/%s, want %d", response.Code, response.Body.String(), expected)
	}
}

func namedCookie(t *testing.T, response *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("response has no %q cookie: %v", name, response.Header().Values("Set-Cookie"))
	return nil
}

func assertSecureDNSCookie(t *testing.T, cookie *http.Cookie) {
	t.Helper()
	if cookie.Path != "/v1/dns" || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge != 0 {
		t.Fatalf("DNS session cookie = %#v", cookie)
	}
}
