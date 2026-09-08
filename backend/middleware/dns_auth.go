package middleware

import (
	"ALLinSSL/backend/internal/dns"
	"ALLinSSL/backend/public"
	"errors"
	"mime"
	"net/http"
	"strings"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

const maxDNSRequestBodyBytes int64 = 64 * 1024

const DNSIdentityContextKey = "dns_identity"

func DNSRequestPreflight() gin.HandlerFunc {
	return func(context *gin.Context) {
		if !strings.HasPrefix(context.Request.URL.Path, "/v1/dns/") {
			context.Next()
			return
		}
		if context.Request.Method != http.MethodPost {
			context.AbortWithStatus(http.StatusMethodNotAllowed)
			return
		}

		contentType, _, err := mime.ParseMediaType(context.GetHeader("Content-Type"))
		if !emptyDNSReadRequest(context.Request) && (err != nil || contentType != "application/x-www-form-urlencoded") {
			context.AbortWithStatus(http.StatusUnsupportedMediaType)
			return
		}
		if hasAPIAuthenticationFields(context.Request.URL.Query()) {
			context.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		context.Request.Body = http.MaxBytesReader(context.Writer, context.Request.Body, maxDNSRequestBodyBytes)
		if err := context.Request.ParseForm(); err != nil {
			var maxBytesError *http.MaxBytesError
			if errors.As(err, &maxBytesError) {
				context.AbortWithStatus(http.StatusRequestEntityTooLarge)
				return
			}
			context.AbortWithStatus(http.StatusBadRequest)
			return
		}
		if hasAPIAuthenticationFields(context.Request.PostForm) {
			context.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		if protectedDNSMutation(context.Request.URL.Path) && !sameOrigin(context.Request) {
			context.AbortWithStatus(http.StatusForbidden)
			return
		}
		context.Next()
	}
}

func protectedDNSMutation(path string) bool {
	return path == "/v1/dns/create_record" || path == "/v1/dns/update_record" || path == "/v1/dns/delete_record" || path == "/v1/dns/set_record_status"
}

func emptyDNSReadRequest(request *http.Request) bool {
	return request.ContentLength == 0 && (request.URL.Path == "/v1/dns/get_credentials" || request.URL.Path == "/v1/dns/get_session")
}

func hasAPIAuthenticationFields(values map[string][]string) bool {
	_, hasToken := values["api_token"]
	_, hasTimestamp := values["timestamp"]
	return hasToken || hasTimestamp
}

func sameOrigin(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return false
	}
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	return origin == scheme+"://"+request.Host
}

func DNSSessionRequired() gin.HandlerFunc {
	return func(context *gin.Context) {
		session := sessions.Default(context)
		loggedIn, ok := session.Get("login").(bool)
		if !ok || !loggedIn || session.Get("__login_key") != public.LoginKey {
			context.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		identity, err := dns.GetSessionIdentity(session)
		if err != nil {
			context.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		binding, err := context.Request.Cookie(dns.SessionCookieName)
		if err != nil || !dns.ValidateSessionBinding(identity, binding.Value) {
			context.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		if protectedDNSMutation(context.Request.URL.Path) {
			if !sameOrigin(context.Request) || !dns.ValidateCSRF(identity, context.PostForm("csrf_token")) {
				context.AbortWithStatus(http.StatusForbidden)
				return
			}
		}
		context.Set(DNSIdentityContextKey, identity)
		context.Next()
	}
}

func DNSIdentity(context *gin.Context) (dns.SessionIdentity, bool) {
	if context == nil {
		return dns.SessionIdentity{}, false
	}
	identity, ok := context.Get(DNSIdentityContextKey)
	value, ok := identity.(dns.SessionIdentity)
	return value, ok
}
