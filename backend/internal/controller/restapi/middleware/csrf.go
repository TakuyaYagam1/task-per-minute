package middleware

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

const (
	PlayerCSRFCookieName       = "tpm_player_csrf"
	AdminAccessCSRFCookieName  = "tpm_admin_access_csrf"
	AdminRefreshCSRFCookieName = "tpm_admin_refresh_csrf"

	CSRFHeaderName             = "X-CSRF-Token"
	AdminRefreshCSRFHeaderName = "X-Admin-Refresh-CSRF-Token"

	playerCSRFNonceBytes = 32
	playerCSRFSigBytes   = sha256.Size
	playerCSRFPathPrefix = "/api/v1/players/"
	adminCSRFPathPrefix  = "/api/v1/admin/"
)

var errInvalidCSRFBinding = errors.New("invalid csrf binding")

// CSRFGuard protects cookie-authenticated REST mutations. The first player
// join and admin login requests can bootstrap sessions without a token; once a
// session cookie exists, unsafe requests must echo a session-bound CSRF token
// in X-CSRF-Token.
func CSRFGuard() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if requiresPlayerCSRF(r) {
				if !validatePlayerCSRF(r) {
					writeProblem(w, r, http.StatusForbidden, http.StatusText(http.StatusForbidden), "csrf token invalid")
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			if cookieName, secret, ok := adminCSRFBinding(r); ok {
				if !validateAdminCSRF(r, cookieName, secret) {
					writeProblem(w, r, http.StatusForbidden, http.StatusText(http.StatusForbidden), "csrf token invalid")
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func validatePlayerCSRF(r *http.Request) bool {
	sessionToken, ok := PlayerSessionTokenFromRequest(r)
	if !ok {
		return false
	}

	cookieToken, ok := PlayerCSRFTokenFromRequest(r, sessionToken)
	if !ok {
		return false
	}

	headerToken := strings.TrimSpace(r.Header.Get(CSRFHeaderName))
	return validPlayerCSRFToken(sessionToken, headerToken) &&
		subtle.ConstantTimeCompare([]byte(cookieToken), []byte(headerToken)) == 1
}

func requiresPlayerCSRF(r *http.Request) bool {
	if r == nil || !isUnsafeMethod(r.Method) || r.URL == nil {
		return false
	}
	if !strings.HasPrefix(r.URL.Path, playerCSRFPathPrefix) {
		return false
	}
	if r.URL.Path == "/api/v1/players/join" {
		return false
	}
	_, ok := PlayerSessionTokenFromRequest(r)
	return ok
}

func adminCSRFBinding(r *http.Request) (string, string, bool) {
	if r == nil || !isUnsafeMethod(r.Method) || r.URL == nil {
		return "", "", false
	}
	if !strings.HasPrefix(r.URL.Path, adminCSRFPathPrefix) {
		return "", "", false
	}
	if r.URL.Path == "/api/v1/admin/login" {
		return "", "", false
	}

	switch r.URL.Path {
	case "/api/v1/admin/refresh", "/api/v1/admin/logout":
		if token, ok := tokenCookieValue(r, AdminRefreshCookieName); ok {
			return AdminRefreshCSRFCookieName, token, true
		}
	default:
		if token, ok := tokenCookieValue(r, AdminAccessCookieName); ok {
			return AdminAccessCSRFCookieName, token, true
		}
	}
	return "", "", false
}

func NewPlayerCSRFToken(sessionToken uuid.UUID) (string, error) {
	if sessionToken == uuid.Nil {
		return "", errInvalidCSRFBinding
	}
	nonce := make([]byte, playerCSRFNonceBytes)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return encodePlayerCSRFPart(nonce) + "." + encodePlayerCSRFPart(signPlayerCSRFToken(sessionToken, nonce)), nil
}

func PlayerCSRFTokenFromRequest(r *http.Request, sessionToken uuid.UUID) (string, bool) {
	if r == nil {
		return "", false
	}
	cookie, err := r.Cookie(PlayerCSRFCookieName)
	if err != nil {
		return "", false
	}
	token := strings.TrimSpace(cookie.Value)
	if !validPlayerCSRFToken(sessionToken, token) {
		return "", false
	}
	return token, true
}

func EnsurePlayerCSRFCookie(w http.ResponseWriter, r *http.Request, sessionToken uuid.UUID) error {
	if token, ok := PlayerCSRFTokenFromRequest(r, sessionToken); ok {
		w.Header().Set(CSRFHeaderName, token)
		return nil
	}
	token, err := NewPlayerCSRFToken(sessionToken)
	if err != nil {
		return err
	}
	SetPlayerCSRFCookie(w, r, token)
	return nil
}

func SetPlayerCSRFCookie(w http.ResponseWriter, r *http.Request, token string) {
	w.Header().Set(CSRFHeaderName, token)
	http.SetCookie(w, playerCSRFCookie(r, token, 0))
}

func ClearPlayerCSRFCookie(w http.ResponseWriter, r *http.Request) {
	//nolint:gosec,nolintlint // G124 in newer gosec: CSRF cookie is intentionally readable; Secure follows trusted TLS/proxy scheme.
	cookie := playerCSRFCookie(r, "", -1)
	cookie.Expires = expiredCookieTime()
	http.SetCookie(w, cookie)
}

func playerCSRFCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	//nolint:gosec,nolintlint // G124 in newer gosec: double-submit CSRF token must be readable by JS; SameSite is set and Secure is scheme-aware.
	return &http.Cookie{
		Name:     PlayerCSRFCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: false,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteLaxMode,
	}
}

func validPlayerCSRFToken(sessionToken uuid.UUID, token string) bool {
	nonce, signature, ok := splitPlayerCSRFToken(token)
	if !ok {
		return false
	}
	expected := signPlayerCSRFToken(sessionToken, nonce)
	return subtle.ConstantTimeCompare(signature, expected) == 1
}

func splitPlayerCSRFToken(token string) ([]byte, []byte, bool) {
	nonceText, sigText, ok := strings.Cut(strings.TrimSpace(token), ".")
	if !ok || nonceText == "" || sigText == "" {
		return nil, nil, false
	}
	nonce, err := base64.RawURLEncoding.DecodeString(nonceText)
	if err != nil || len(nonce) != playerCSRFNonceBytes {
		return nil, nil, false
	}
	signature, err := base64.RawURLEncoding.DecodeString(sigText)
	if err != nil || len(signature) != playerCSRFSigBytes {
		return nil, nil, false
	}
	return nonce, signature, true
}

func signPlayerCSRFToken(sessionToken uuid.UUID, nonce []byte) []byte {
	mac := hmac.New(sha256.New, sessionToken[:])
	mac.Write([]byte(PlayerCSRFCookieName))
	mac.Write([]byte{0})
	mac.Write(nonce)
	return mac.Sum(nil)
}

func encodePlayerCSRFPart(value []byte) string {
	return base64.RawURLEncoding.EncodeToString(value)
}

func NewAdminCSRFToken(cookieName, secret string) (string, error) {
	if strings.TrimSpace(cookieName) == "" || strings.TrimSpace(secret) == "" {
		return "", errInvalidCSRFBinding
	}
	nonce := make([]byte, playerCSRFNonceBytes)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return encodePlayerCSRFPart(nonce) + "." + encodePlayerCSRFPart(signAdminCSRFToken(cookieName, secret, nonce)), nil
}

func AdminCSRFTokenFromRequest(r *http.Request, cookieName, secret string) (string, bool) {
	if r == nil {
		return "", false
	}
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return "", false
	}
	token := strings.TrimSpace(cookie.Value)
	if !validAdminCSRFToken(cookieName, secret, token) {
		return "", false
	}
	return token, true
}

func SetAdminCSRFCookie(w http.ResponseWriter, r *http.Request, cookieName, token string, maxAge int) {
	switch cookieName {
	case AdminAccessCSRFCookieName:
		w.Header().Set(CSRFHeaderName, token)
	case AdminRefreshCSRFCookieName:
		w.Header().Set(AdminRefreshCSRFHeaderName, token)
	}
	http.SetCookie(w, adminCSRFCookie(r, cookieName, token, maxAge))
}

func ClearAdminCSRFCookies(w http.ResponseWriter, r *http.Request) {
	for _, name := range []string{AdminAccessCSRFCookieName, AdminRefreshCSRFCookieName} {
		//nolint:gosec,nolintlint // G124 in newer gosec: CSRF cookie is intentionally readable; Secure follows trusted TLS/proxy scheme.
		cookie := adminCSRFCookie(r, name, "", -1)
		cookie.Expires = expiredCookieTime()
		http.SetCookie(w, cookie)
	}
}

func adminCSRFCookie(r *http.Request, name, value string, maxAge int) *http.Cookie {
	//nolint:gosec,nolintlint // G124 in newer gosec: double-submit CSRF token must be readable by JS; SameSite is set and Secure is scheme-aware.
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/api/v1/admin",
		MaxAge:   maxAge,
		HttpOnly: false,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteLaxMode,
	}
}

func validateAdminCSRF(r *http.Request, cookieName, secret string) bool {
	cookieToken, ok := AdminCSRFTokenFromRequest(r, cookieName, secret)
	if !ok {
		return false
	}

	headerToken := strings.TrimSpace(r.Header.Get(CSRFHeaderName))
	return validAdminCSRFToken(cookieName, secret, headerToken) &&
		subtle.ConstantTimeCompare([]byte(cookieToken), []byte(headerToken)) == 1
}

func validAdminCSRFToken(cookieName, secret, token string) bool {
	nonce, signature, ok := splitPlayerCSRFToken(token)
	if !ok {
		return false
	}
	expected := signAdminCSRFToken(cookieName, secret, nonce)
	return subtle.ConstantTimeCompare(signature, expected) == 1
}

func signAdminCSRFToken(cookieName, secret string, nonce []byte) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(cookieName))
	mac.Write([]byte{0})
	mac.Write(nonce)
	return mac.Sum(nil)
}
