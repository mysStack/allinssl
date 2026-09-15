package middleware

import (
	"ALLinSSL/backend/public"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/memstore"
	"github.com/gin-gonic/gin"
)

func TestDNSRoutesDoNotAllowAPITokenAuthentication(t *testing.T) {
	for _, path := range []string{
		"/v1/dns/get_credentials",
		"/v1/dns/get_zones",
		"/v1/dns/get_snapshot",
	} {
		if canUseAPIToken(path) {
			t.Fatalf("DNS route accepts API token authentication: %s", path)
		}
	}
	if !canUseAPIToken("/v1/access/get_list") {
		t.Fatal("existing non-DNS route unexpectedly rejects API token authentication")
	}
}

func TestSessionAuthMiddlewareAllowsStaticAssetsWithoutSecureSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalSecure, originalSessionKey := public.Secure, public.SessionKey
	public.Secure = "/secure-entry"
	public.SessionKey = "session"
	t.Cleanup(func() {
		public.Secure = originalSecure
		public.SessionKey = originalSessionKey
	})

	router := gin.New()
	router.Use(sessions.Sessions(public.SessionKey, memstore.NewStore([]byte("test-secret"))))
	router.Use(SessionAuthMiddleware())
	router.GET("/static/images/logo.png", func(context *gin.Context) {
		context.Status(http.StatusNoContent)
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/static/images/logo.png", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("static asset status = %d, want %d", response.Code, http.StatusNoContent)
	}
}
