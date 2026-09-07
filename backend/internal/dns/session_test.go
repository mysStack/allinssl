package dns

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/memstore"
	"github.com/gin-gonic/gin"
)

func TestRotateSessionChangesEveryDNSCredentialAndBindsScopedCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(sessions.Sessions("session", memstore.NewStore([]byte("test-secret"))))
	var firstIdentity SessionIdentity
	var secondIdentity SessionIdentity
	router.POST("/rotate", func(context *gin.Context) {
		identity, err := RotateSession(sessions.Default(context))
		if err != nil || sessions.Default(context).Save() != nil {
			context.Status(http.StatusInternalServerError)
			return
		}
		if firstIdentity == (SessionIdentity{}) {
			firstIdentity = identity
		} else {
			secondIdentity = identity
		}
		context.Status(http.StatusNoContent)
	})

	firstResponse := httptest.NewRecorder()
	router.ServeHTTP(firstResponse, httptest.NewRequest(http.MethodPost, "/rotate", nil))
	secondRequest := httptest.NewRequest(http.MethodPost, "/rotate", nil)
	secondRequest.AddCookie(firstResponse.Result().Cookies()[0])
	router.ServeHTTP(httptest.NewRecorder(), secondRequest)
	if firstIdentity.ActorID != "local-admin" || secondIdentity.ActorID != "local-admin" {
		t.Fatalf("actor IDs = %q/%q", firstIdentity.ActorID, secondIdentity.ActorID)
	}
	if firstIdentity.SessionID == secondIdentity.SessionID || firstIdentity.AuthEpoch == secondIdentity.AuthEpoch || firstIdentity.CSRFToken == secondIdentity.CSRFToken {
		t.Fatalf("DNS credentials were not fully rotated: first=%#v second=%#v", firstIdentity, secondIdentity)
	}
	if ValidateSessionBinding(secondIdentity, firstIdentity.SessionID) || !ValidateSessionBinding(secondIdentity, secondIdentity.SessionID) {
		t.Fatal("rotated DNS session accepts the fixed pre-login binding")
	}

	cookie := NewSessionCookie(secondIdentity)
	if cookie.Name != SessionCookieName || cookie.Value != secondIdentity.SessionID || cookie.Path != "/v1/dns" ||
		!cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge != 0 {
		t.Fatalf("unsafe DNS session cookie = %#v", cookie)
	}
	cleared := ExpiredSessionCookie()
	if cleared.Name != SessionCookieName || cleared.Value != "" || cleared.Path != cookie.Path || cleared.MaxAge >= 0 ||
		!cleared.Secure || !cleared.HttpOnly || cleared.SameSite != http.SameSiteStrictMode {
		t.Fatalf("unsafe expired DNS session cookie = %#v", cleared)
	}
}

func TestRotateSessionCreatesIdentityAndClearSessionRemovesIt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(sessions.Sessions("session", memstore.NewStore([]byte("test-secret"))))
	router.POST("/rotate", func(context *gin.Context) {
		identity, err := RotateSession(sessions.Default(context))
		if err != nil {
			context.Status(http.StatusInternalServerError)
			return
		}
		if identity.ActorID != "local-admin" || identity.SessionID == "" || identity.AuthEpoch == "" {
			context.Status(http.StatusInternalServerError)
			return
		}
		if err := sessions.Default(context).Save(); err != nil {
			context.Status(http.StatusInternalServerError)
			return
		}
		context.Status(http.StatusNoContent)
	})
	router.POST("/clear", func(context *gin.Context) {
		ClearSession(sessions.Default(context))
		if err := sessions.Default(context).Save(); err != nil {
			context.Status(http.StatusInternalServerError)
			return
		}
		context.Status(http.StatusNoContent)
	})
	router.GET("/identity", func(context *gin.Context) {
		if _, err := GetSessionIdentity(sessions.Default(context)); err != nil {
			context.Status(http.StatusUnauthorized)
			return
		}
		context.Status(http.StatusNoContent)
	})

	rotateResponse := httptest.NewRecorder()
	router.ServeHTTP(rotateResponse, httptest.NewRequest(http.MethodPost, "/rotate", nil))
	if rotateResponse.Code != http.StatusNoContent {
		t.Fatalf("rotate status = %d", rotateResponse.Code)
	}
	cookie := rotateResponse.Result().Cookies()[0]

	identityRequest := httptest.NewRequest(http.MethodGet, "/identity", nil)
	identityRequest.AddCookie(cookie)
	identityResponse := httptest.NewRecorder()
	router.ServeHTTP(identityResponse, identityRequest)
	if identityResponse.Code != http.StatusNoContent {
		t.Fatalf("identity status = %d", identityResponse.Code)
	}

	clearRequest := httptest.NewRequest(http.MethodPost, "/clear", nil)
	clearRequest.AddCookie(cookie)
	clearResponse := httptest.NewRecorder()
	router.ServeHTTP(clearResponse, clearRequest)
	if clearResponse.Code != http.StatusNoContent {
		t.Fatalf("clear status = %d", clearResponse.Code)
	}

	clearedIdentityRequest := httptest.NewRequest(http.MethodGet, "/identity", nil)
	clearedIdentityRequest.AddCookie(clearResponse.Result().Cookies()[0])
	clearedIdentityResponse := httptest.NewRecorder()
	router.ServeHTTP(clearedIdentityResponse, clearedIdentityRequest)
	if clearedIdentityResponse.Code != http.StatusUnauthorized {
		t.Fatalf("cleared identity status = %d", clearedIdentityResponse.Code)
	}
}
