package dns

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"time"

	"github.com/gin-contrib/sessions"
)

const (
	dnsActorKey       = "dns_actor"
	dnsSessionIDKey   = "dns_session_id"
	dnsAuthEpochKey   = "dns_auth_epoch"
	dnsCSRFTokenKey   = "dns_csrf_token"
	defaultDNSActorID = "local-admin"
	SessionCookieName = "__Secure-allinssl-dns-session"
)

var ErrDNSSession = errors.New("DNS_SESSION_REQUIRED")

type SessionIdentity struct {
	ActorID   string
	SessionID string
	AuthEpoch string
	CSRFToken string
}

func RotateSession(session sessions.Session) (SessionIdentity, error) {
	if session == nil {
		return SessionIdentity{}, ErrDNSSession
	}
	sessionID, err := newSessionValue()
	if err != nil {
		return SessionIdentity{}, err
	}
	authEpoch, err := newSessionValue()
	if err != nil {
		return SessionIdentity{}, err
	}
	csrfToken, err := newSessionValue()
	if err != nil {
		return SessionIdentity{}, err
	}
	identity := SessionIdentity{ActorID: defaultDNSActorID, SessionID: sessionID, AuthEpoch: authEpoch, CSRFToken: csrfToken}
	session.Set(dnsActorKey, identity.ActorID)
	session.Set(dnsSessionIDKey, identity.SessionID)
	session.Set(dnsAuthEpochKey, identity.AuthEpoch)
	session.Set(dnsCSRFTokenKey, identity.CSRFToken)
	return identity, nil
}

func ClearSession(session sessions.Session) {
	if session == nil {
		return
	}
	session.Delete(dnsActorKey)
	session.Delete(dnsSessionIDKey)
	session.Delete(dnsAuthEpochKey)
	session.Delete(dnsCSRFTokenKey)
}

func GetSessionIdentity(session sessions.Session) (SessionIdentity, error) {
	if session == nil {
		return SessionIdentity{}, ErrDNSSession
	}
	identity := SessionIdentity{
		ActorID:   sessionString(session.Get(dnsActorKey)),
		SessionID: sessionString(session.Get(dnsSessionIDKey)),
		AuthEpoch: sessionString(session.Get(dnsAuthEpochKey)),
		CSRFToken: sessionString(session.Get(dnsCSRFTokenKey)),
	}
	if identity.ActorID != defaultDNSActorID || identity.SessionID == "" || identity.AuthEpoch == "" || identity.CSRFToken == "" {
		return SessionIdentity{}, ErrDNSSession
	}
	return identity, nil
}

func ValidateCSRF(identity SessionIdentity, token string) bool {
	return identity.CSRFToken != "" && token != "" && subtle.ConstantTimeCompare([]byte(identity.CSRFToken), []byte(token)) == 1
}

func ValidateSessionBinding(identity SessionIdentity, binding string) bool {
	return identity.SessionID != "" && binding != "" && subtle.ConstantTimeCompare([]byte(identity.SessionID), []byte(binding)) == 1
}

func NewSessionCookie(identity SessionIdentity) *http.Cookie {
	return dnsSessionCookie(identity.SessionID, 0)
}

func ExpiredSessionCookie() *http.Cookie {
	cookie := dnsSessionCookie("", -1)
	cookie.Expires = time.Unix(1, 0)
	return cookie
}

func dnsSessionCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    value,
		Path:     "/v1/dns",
		MaxAge:   maxAge,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}
}

func newSessionValue() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func sessionString(value any) string {
	stringValue, _ := value.(string)
	return stringValue
}
